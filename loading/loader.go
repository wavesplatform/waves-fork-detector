package loading

import (
	"context"
	"math/big"
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
	timerInterval    = 5 * time.Second
	tickerInterval   = time.Second
	restartThreshold = 10
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

type failureCounter struct {
	m map[netip.Addr]int
}

func (fc *failureCounter) inc(addr netip.Addr) {
	if fc.m == nil {
		fc.m = make(map[netip.Addr]int)
	}
	if _, ok := fc.m[addr]; !ok {
		fc.m[addr] = 0
	}
	fc.m[addr]++
}

func (fc *failureCounter) count(addr netip.Addr) int {
	if fc.m == nil {
		return 0
	}
	if c, ok := fc.m[addr]; ok {
		return c
	}
	return 0
}

func (fc *failureCounter) reset(addr netip.Addr) {
	if fc.m == nil {
		fc.m = make(map[netip.Addr]int)
	}
	fc.m[addr] = 0
}

type Loader struct {
	wait func() error
	ctx  context.Context

	idsCh   <-chan IDsPackage
	blockCh <-chan BlockPackage

	pl       *peerLoader
	prevDiff *big.Int
	fc       failureCounter

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
	l.timer.Reset(timerInterval)
}

func (l *Loader) Shutdown() {
	if err := l.wait(); err != nil {
		zap.S().Warnf("Failed to shutdown Loader: %v", err)
	}
	l.ticker.Stop()
	zap.S().Info("Loader shutdown successfully")
}

func (l *Loader) OK() {
	if l.pl != nil && l.pl.sm.MustState() == stateIdle {
		l.fc.reset(l.pl.peer.ID())
		// Check score again and continue sync if needed.
		p, err := l.registry.Peer(l.pl.peer.ID())
		if err != nil {
			zap.S().Warnf("Failed to get connection: %v", err)
			return
		}
		score, err := l.linkage.LeashScore(l.pl.peer.ID())
		if err != nil {
			zap.S().Warnf("Failed to get peers score: %v", err)
			return
		}
		if diff, lagging := l.isLagging(p.Score, score); lagging {
			zap.S().Debugf("[LDR] Peer '%s' is still lagging, continue loading", l.pl.peer.ID())
			l.prevDiff = diff
			l.continueSync()
		} else {
			zap.S().Debugf("[LDR] Peer '%s' is not lagging anymore, resetting loading peer", l.pl.peer.ID())
			l.resetLoadingPeer()
		}
	}
}

func (l *Loader) Fail() {
	if l.pl != nil && l.pl.sm.MustState() == stateDone {
		zap.S().Debugf("[LDR] Peer '%s' failed to load history", l.pl.peer.ID())
		l.fc.inc(l.pl.peer.ID())
		l.pl = nil
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
			l.ticker.Reset(tickerInterval)
		case <-l.timer.C:
			l.timer.Stop()
			l.sync()
			l.timer.Reset(timerInterval)
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
		zap.S().Warnf("Failed to get connections: %v", err)
		return // Failed to get connections, try again later.
	}
	if len(connections) == 0 {
		zap.S().Debug("[LDR] No connected peers")
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
		zap.S().Infof("No lagging peers")
		return
	}
	zap.S().Infof("Syncing with one of %d lagging peers", len(lagging))
	// Select random lagging peer and start synchronization with it.
	p := lagging[rand2.IntN(len(lagging))] //nolint:gosec //we don't need a crypto rand here.
	l.pl = newPeerLoader(&p, l.linkage, l)
	zap.S().Infof("Start loading history for peer '%s'", p.ID())
	if l.fc.count(l.pl.peer.ID()) >= restartThreshold {
		l.restartSync()
	}
	l.continueSync()
}

func (l *Loader) continueSync() {
	l.ticker.Reset(tickerInterval) // Start ticker.
	if err := l.pl.start(); err != nil {
		zap.S().Warnf("Failed to syncronize with peer '%s': %v", l.pl.peer.ID(), err)
	}
}

func (l *Loader) restartSync() {
	zap.S().Debugf("[LDR] Restarting syncronization with peer '%s'", l.pl.peer.ID())
	l.fc.reset(l.pl.peer.ID())
	l.ticker.Reset(tickerInterval)
	if err := l.pl.restart(); err != nil {
		zap.S().Warnf("Failed to restart syncronization with peer '%s': %v", l.pl.peer.ID(), err)
	}
}

func (l *Loader) tick() {
	if l.pl == nil {
		return
	}
	if err := l.pl.processTick(time.Now()); err != nil {
		zap.S().Warnf("Failed to process tick on peer '%s': %v", l.pl.peer.ID(), err)
	}
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
		zap.S().Warnf("Failed to process IDs for peer '%s': %v", p.Peer.String(), err)
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
		zap.S().Warnf("Failed to process block for peer '%s': %v", p.Peer.String(), err)
	}
}

func (l *Loader) isLagging(peerScore, leashScore *big.Int) (*big.Int, bool) {
	// Determine if difference between peer's score and leash score is changed since last iteration.
	diff := big.NewInt(0).Sub(peerScore, leashScore)
	if l.prevDiff == nil {
		return diff, peerScore.Cmp(leashScore) > 0
	}
	return diff, peerScore.Cmp(leashScore) > 0 && l.prevDiff.Cmp(diff) != 0
}

func (l *Loader) resetLoadingPeer() {
	l.pl = nil
	l.prevDiff = nil
}
