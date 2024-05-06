package internal

import (
	"context"
	"fmt"
	rand2 "math/rand/v2"
	"net/netip"
	"slices"
	"time"

	"github.com/rhansen/go-kairos/kairos"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/alexeykiselev/waves-fork-detector/internal/chains"
	"github.com/alexeykiselev/waves-fork-detector/internal/peers"
)

// Loader is responsible for restoring forks history.
// After the start, it requests all heads known at the moment.
// If there is a peers group attached to the head, it starts synchronization with one of the peers.
// If the head has not attached peers, Loader selects random peers from the group of unleashed peers and starts
// synchronization with it from the last known block of the head. If the peer advances the head, Loader attaches
// the peer to the head.
// In case of no response from the peer, Loader selects another peer from the group of unleashed peers.

const (
	retryInterval = 5 * time.Second
	idsBatchSize  = 100
)

type IDsPackage struct {
	peer netip.Addr
	ids  []proto.BlockID
}

type BlockPackage struct {
	peer    netip.Addr
	blockID proto.BlockID
}

type item struct {
	requested proto.BlockID
	received  proto.BlockID
}

type queue struct {
	q []item
}

func newQueue(ids []proto.BlockID) queue {
	q := make([]item, len(ids))
	for i, id := range ids {
		q[i] = item{requested: id}
	}
	return queue{q: q}
}

func (q *queue) put(id proto.BlockID) {
	for i, p := range q.q {
		if p.requested == id {
			q.q[i].received = id
			return
		}
	}
}

func (q *queue) ready() bool {
	for _, i := range q.q {
		if i.requested != i.received {
			return false
		}
	}
	return true
}

type peerState struct {
	peer   peers.Peer
	headID uint64
	queue  queue
}

type Loader struct {
	wait func() error
	ctx  context.Context

	idsCh   <-chan IDsPackage
	blockCh <-chan BlockPackage

	peers map[netip.Addr]peerState

	registry *peers.Registry
	linkage  *chains.Linkage

	timer *kairos.Timer
}

func NewLoader(
	registry *peers.Registry, linkage *chains.Linkage, idsCh <-chan IDsPackage, blockCh <-chan BlockPackage,
) *Loader {
	return &Loader{
		idsCh:    idsCh,
		blockCh:  blockCh,
		peers:    make(map[netip.Addr]peerState),
		registry: registry,
		linkage:  linkage,
		timer:    kairos.NewStoppedTimer(),
	}
}

func (l *Loader) Run(ctx context.Context) {
	g, gc := errgroup.WithContext(ctx)
	l.ctx = gc
	l.wait = g.Wait

	g.Go(l.loop)

	l.timer.Reset(retryInterval)
}

func (l *Loader) Shutdown() {
	if err := l.wait(); err != nil {
		zap.S().Warnf("Failed to shutdown Loader: %v", err)
	}
	l.timer.Stop()
	zap.S().Info("Loader shutdown successfully")
}

func (l *Loader) loop() error {
	for {
		select {
		case <-l.ctx.Done():
			return nil
		case ids, ok := <-l.idsCh:
			if ok {
				l.handleIDs(ids)
			}
		case block, ok := <-l.blockCh:
			if ok {
				l.handleBlock(block)
			}
		case <-l.timer.C:
			l.timer.Stop()
			l.sync()
			l.timer.Reset(retryInterval)
		}
	}
}

func (l *Loader) sync() {
	// Get lagging connected peers and separate them by scores.
	groups := make(map[string][]peers.Peer)
	connections := l.registry.Connections()
	for _, cp := range connections {
		if cp.Score == nil {
			continue // For this peer broadcast score is unknown, skip it.
		}
		score, err := l.linkage.LeashScore(cp.AddressPort.Addr())
		if err != nil {
			zap.S().Warnf("Failed to get peers score: %v", err)
			continue
		}
		// Peer's broadcast score is greater than peer's leash score that means we have to restore history chain
		// for that peer.
		if cp.Score.Cmp(score) > 0 {
			k := cp.Score.String() // Use broadcast score as a key.
			if v, ok := groups[k]; ok {
				groups[k] = append(v, cp)
			} else {
				groups[k] = []peers.Peer{cp}
			}
		}
	}
	// Request fork heads.
	heads, err := l.linkage.Heads()
	if err != nil {
		zap.S().Errorf("[LDR] Failed to get heads: %v", err)
		return // Failed to get heads, try again later.
	}
	// Select random peer from every group and request signatures.
	for _, group := range groups {
		p := group[rand2.IntN(len(group))] // Random peer.
		var h chains.Head
		for _, head := range heads {
			if !slices.Contains(p.UnsuccessfulHeads, head.ID) {
				h = head
				break
			}
		}
		if err = l.syncWithHead(h, p); err != nil {
			zap.S().Errorf("[LDR] Synchronization with peer '%s' failed: %v", p.AddressPort.Addr().String(), err)
		}
	}
}

func (l *Loader) syncWithHead(head chains.Head, p peers.Peer) error {
	if err := l.syncByBlock(head.BlockID, p); err != nil {
		return err
	}
	l.peers[p.AddressPort.Addr()] = peerState{peer: p, headID: head.ID}
	return nil
}

func (l *Loader) syncByBlock(id proto.BlockID, p peers.Peer) error {
	ids, err := l.linkage.LastIDs(id, idsBatchSize) // Request last 100 blocks from the head.
	if err != nil {
		return fmt.Errorf("failed to get last 100 blocks from '%s': %w", id.String(), err)
	}
	if len(ids) > 0 { // Do not request block IDs for invalid or non-existent head.
		zap.S().Debugf("[LDR] Requesting signatures from %s for blocks [%s..%s]",
			p.AddressPort.Addr(), ids[0].ShortString(), ids[len(ids)-1].ShortString())
		p.RequestBlockIDs(ids)
		l.peers[p.AddressPort.Addr()] = peerState{peer: p}
	}
	return nil
}

func (l *Loader) handleIDs(p IDsPackage) {
	state, ok := l.peers[p.peer]
	if !ok {
		return // Ignore IDs from unexpected peers.
	}
	if len(p.ids) == 0 {
		return // Ignore empty IDs.
	}
	q := newQueue(p.ids)
	zap.S().Debugf("[LDR] Requesting %d blocks for signatures [%s..%s] from %s",
		len(p.ids), p.ids[0].ShortString(), p.ids[len(p.ids)-1].ShortString(), p.peer)
	for _, id := range p.ids {
		state.peer.RequestBlock(id)
	}
	state.queue = q
	l.peers[p.peer] = state
}

func (l *Loader) handleBlock(p BlockPackage) {
	state, ok := l.peers[p.peer]
	if !ok {
		return // Ignore blocks from unexpected peers.
	}
	state.queue.put(p.blockID) // Put received block in peers queue, unrequested blocks are ignored.
	if state.queue.ready() {
		leash, err := l.linkage.Leash(p.peer)
		if err != nil {
			zap.S().Errorf("[LDR] Failed to get last block of peer '%s': %v", p.peer.String(), err)
			return
		}
		if syncErr := l.syncByBlock(leash, state.peer); syncErr != nil {
			zap.S().Errorf("[LDR] Synchronization with peer '%s' failed: %v", p.peer.String(), syncErr)
		}
	}
}
