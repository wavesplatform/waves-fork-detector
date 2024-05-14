package loading

import (
	"context"
	rand2 "math/rand/v2"
	"net/netip"
	"time"

	"github.com/rhansen/go-kairos/kairos"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/alexeykiselev/waves-fork-detector/chains"
	"github.com/alexeykiselev/waves-fork-detector/peers"
)

// Loader is responsible for restoring forks history.
// After the start, it trys to synchronize all connected peers one by one.
// If there is a peers group attached to the head, it starts synchronization with one of the peers.
// If the head has not attached peers, Loader selects random peers from the group of unleashed peers and starts
// synchronization with it from the last known block of the head. If the peer advances the head, Loader attaches
// the peer to the head.
// In case of no response from the peer, Loader selects another peer from the group of unleashed peers.

const (
	timerInterval  = 5 * time.Second
	tickerInterval = time.Second
)

type IDsPackage struct {
	Peer netip.Addr
	IDs  []proto.BlockID
}

type BlockPackage struct {
	Peer  netip.Addr
	Block *proto.Block
}

type Reporter interface {
	OK()
	Fail()
}

type Loader struct {
	wait func() error
	ctx  context.Context

	idsCh   <-chan IDsPackage
	blockCh <-chan BlockPackage

	pl *peerLoader

	registry *peers.Registry
	linkage  *chains.Linkage

	timer  *kairos.Timer
	ticker *kairos.Timer
}

func NewLoader(
	registry *peers.Registry, linkage *chains.Linkage, idsCh <-chan IDsPackage, blockCh <-chan BlockPackage,
) *Loader {
	return &Loader{
		idsCh:    idsCh,
		blockCh:  blockCh,
		registry: registry,
		linkage:  linkage,
		timer:    kairos.NewStoppedTimer(),
		ticker:   kairos.NewStoppedTimer(),
	}
}

func (l *Loader) Run(ctx context.Context) {
	g, gc := errgroup.WithContext(ctx)
	l.ctx = gc
	l.wait = g.Wait

	g.Go(l.loop)
	l.sync()
}

func (l *Loader) Shutdown() {
	if err := l.wait(); err != nil {
		zap.S().Warnf("Failed to shutdown Loader: %v", err)
	}
	l.ticker.Stop()
	zap.S().Info("Loader shutdown successfully")
}

func (l *Loader) OK() {
	if l.pl != nil && l.pl.sm.MustState() == stageIdle {
		l.continueSync()
	}
}

func (l *Loader) Fail() {
	if l.pl != nil && l.pl.sm.MustState() == stageDone {
		l.pl = nil
		l.timer.Reset(timerInterval)
	}
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
		case <-l.ticker.C:
			l.ticker.Stop()
			l.tick()
		case <-l.timer.C:
			l.timer.Stop()
			l.sync()
		}
	}
}

func (l *Loader) sync() {
	// Check that we don't sync with a peer.
	if l.pl != nil {
		return
	}
	// Get lagging connected peers.
	connections, err := l.registry.Connections()
	if err != nil {
		zap.S().Errorf("[LDR] Failed to get connections: %v", err)
		l.timer.Reset(timerInterval)
		return // Failed to get connections, try again later.
	}
	if len(connections) == 0 {
		zap.S().Debug("[LDR] No connected peers")
		l.timer.Reset(timerInterval)
		return // No connections, try again later.
	}
	zap.S().Debugf("[LDR] Trying to sync with %d connections", len(connections))
	lagging := make([]peers.Peer, 0, len(connections))
	for _, cp := range connections {
		if cp.Score == nil {
			zap.S().Debugf("[LDR] Peer '%s' has no score", cp.AddressPort.Addr().String())
			continue // For this peer broadcast score is unknown, skip it.
		}
		score, lsErr := l.linkage.LeashScore(cp.AddressPort.Addr())
		if lsErr != nil {
			zap.S().Warnf("Failed to get peers score: %v", lsErr)
			continue
		}
		// Peer's broadcast score is greater than peer's leash score that means we have to restore history chain
		// for that peer.
		if cp.Score.Cmp(score) > 0 {
			lagging = append(lagging, cp)
		}
	}
	if len(lagging) == 0 {
		zap.S().Debug("[LDR] No peers to sync with")
		l.timer.Reset(timerInterval)
		return
	}
	// Select random lagging peer and start synchronization with it.
	p := lagging[rand2.IntN(len(lagging))]
	l.pl = newPeerLoader(&p, l.linkage, l)
	l.continueSync()
}

func (l *Loader) continueSync() {
	l.ticker.Reset(tickerInterval) // Start ticker.
	if err := l.pl.start(); err != nil {
		zap.S().Warnf("[LDR] Failed to syncronize with peer '%s': %v", l.pl.peer.ID(), err)
	}
}

func (l *Loader) tick() {
	if l.pl == nil {
		return
	}
	if err := l.pl.processTick(time.Now()); err != nil {
		zap.S().Warnf("[LDR] Failed to process tick on peer '%s': %v", l.pl.peer.ID(), err)
	}
	l.ticker.Reset(tickerInterval)
}

func (l *Loader) handleIDs(p IDsPackage) {
	if l.pl == nil {
		return // Not syncing.
	}
	if len(p.IDs) == 0 {
		return // Ignore empty IDs.
	}
	if p.Peer != l.pl.peer.ID() {
		return // Ignore IDs from unexpected peers.
	}
	if err := l.pl.processIDs(p.IDs); err != nil {
		zap.S().Warnf("[LDR] Failed to process IDs for peer '%s': %v", p.Peer.String(), err)
	}
}

func (l *Loader) handleBlock(p BlockPackage) {
	if l.pl == nil {
		return // Not syncing.
	}
	if p.Peer != l.pl.peer.ID() {
		return // Ignore blocks from unexpected peers.
	}
	if err := l.pl.processBlock(p.Block); err != nil {
		zap.S().Warnf("[LDR] Failed to process block for peer '%s': %v", p.Peer.String(), err)
	}
}