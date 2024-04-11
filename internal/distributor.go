package internal

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	rand2 "math/rand/v2"
	"net"
	"net/netip"
	"time"

	"github.com/rhansen/go-kairos/kairos"
	"github.com/wavesplatform/gowaves/pkg/crypto"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/wavesplatform/gowaves/pkg/p2p/peer"
	"github.com/wavesplatform/gowaves/pkg/proto"

	"github.com/alexeykiselev/waves-fork-detector/internal/blocks"
	"github.com/alexeykiselev/waves-fork-detector/internal/peers"
)

const pingInterval = 1 * time.Minute

type Distributor struct {
	ctx  context.Context
	wait func() error

	scheme   proto.Scheme
	registry *peers.Registry
	drawer   *blocks.Drawer
	parent   peer.Parent

	timer *kairos.Timer

	loaderCh chan struct{}
	loading  bool
}

func NewDistributor(
	scheme proto.Scheme, drawer *blocks.Drawer, registry *peers.Registry, parent peer.Parent,
) (*Distributor, error) {
	return &Distributor{
		scheme:   scheme,
		drawer:   drawer,
		registry: registry,
		parent:   parent,
		timer:    kairos.NewStoppedTimer(),
		loaderCh: make(chan struct{}),
	}, nil
}

func (l *Distributor) Run(ctx context.Context) {
	g, gc := errgroup.WithContext(ctx)
	l.ctx = gc
	l.wait = g.Wait

	g.Go(l.runLoop)
	g.Go(l.runLoader)

	l.timer.Reset(pingInterval)
}

func (l *Distributor) Shutdown() {
	if err := l.wait(); err != nil {
		zap.S().Warnf("Failed to shutdown Distributor: %v", err)
	}
	zap.S().Info("Distributor shutdown successfully")
}

func (l *Distributor) runLoop() error {
	for {
		select {
		case <-l.ctx.Done():
			zap.S().Debugf("[DTR] Distributor shutdown in progress...")
			return nil

		case <-l.timer.C:
			zap.S().Info("[DTR] Ping connections")
			l.pingConnections()
			l.timer.Reset(pingInterval)

		case infoMessage := <-l.parent.InfoCh:
			l.handleInfoMessage(infoMessage)
		case message := <-l.parent.MessageCh:
			l.handleMessage(message)
		}
	}
}

func (l *Distributor) runLoader() error {
	for {
		select {
		case <-l.ctx.Done():
			zap.S().Debugf("[DTR] Loader shutdown in progress...")
			return nil
		case <-l.loaderCh:
			if l.loading {
				continue
			}
			l.loading = true

			// 1. Get Unleashed Connected Peers
			// 2. Separate them by Scores.
			groups := make(map[string][]peers.Peer)
			connections := l.registry.Connections()
			for _, cp := range connections {
				unleashed, err := l.drawer.IsUnleashed(cp.AddressPort.Addr())
				if err != nil {
					zap.S().Warnf("[DTR] Failed to check if peer is unleashed: %v", err)
					continue
				}
				if unleashed {
					k := cp.Score.String()
					if v, ok := groups[k]; ok {
						groups[k] = append(v, cp)
					} else {
						groups[k] = []peers.Peer{cp}
					}
				}
			}

			// 3. Select random peer from every score group.

			for _, group := range groups {
				p := group[rand2.IntN(len(group))]
				p.Send(&proto.GetBlockIdsMessage{Blocks: []proto.BlockID{}})
			}

			// 5. If peers returns the same set of signatures, request blocks from randomly selected one.
			// 6. If peers returns different sets of signatures, request blocks from every peer separately.
			// 7. Repeat after application of all blocks.

		}
	}
}

func (l *Distributor) pingConnections() {
	l.registry.Broadcast(&proto.GetPeersMessage{})
}

func (l *Distributor) handleInfoMessage(msg peer.InfoMessage) {
	switch v := msg.Value.(type) {
	case *peer.Connected:
		l.handleConnected(v)
	case *peer.InternalErr:
		l.handleInternalError(msg.Peer, v)
	}
}

func (l *Distributor) handleConnected(cm *peer.Connected) {
	ap, err := netip.ParseAddrPort(cm.Peer.RemoteAddr().String())
	if err != nil {
		zap.S().Warnf("[DTR] Failed to parse address: %v", err)
		return
	}
	if rpErr := l.registry.RegisterPeer(ap.Addr(), cm.Peer, cm.Peer.Handshake()); rpErr != nil {
		zap.S().Warnf("[DTR] Failed to check peer: %v", rpErr)
		return
	}
}

func (l *Distributor) handleInternalError(peer peer.Peer, ie *peer.InternalErr) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		zap.S().Warnf("[DTR] Failed to parse address: %v", err)
		return
	}
	zap.S().Infof("[DTR] Closing connection with peer %s", ap.String())
	zap.S().Debugf("[DTR] Peer %s failed with error: %v", ap, ie.Err)
	if clErr := peer.Close(); clErr != nil {
		zap.S().Warnf("[DTR] Failed to close peer connection: %v", clErr)
	}
	if urErr := l.registry.UnregisterPeer(ap.Addr()); urErr != nil {
		zap.S().Warnf("[DTR] Failed to unregister peer: %v", urErr)
	}
}

func (l *Distributor) handleMessage(msg peer.ProtoMessage) {
	switch v := msg.Message.(type) {
	case *proto.ScoreMessage:
		l.handleScoreMessage(msg.ID, v.Score)
	case *proto.BlockMessage:
		l.handleBlockMessage(msg.ID, v)
	case *proto.PBBlockMessage:
		l.handleProtoBlockMessage(msg.ID, v)
	case *proto.PeersMessage:
		l.handlePeersMessage(v)
	case *proto.GetPeersMessage:
		l.handleGetPeersMessage(msg.ID)
	case *proto.SignaturesMessage:
		l.handleSignaturesMessage(msg.ID, v.Signatures)
	case *proto.BlockIdsMessage:
		l.handleBlockIDsMessage(msg.ID, v.Blocks)
	}
}

func (l *Distributor) handleScoreMessage(peer peer.Peer, score []byte) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		zap.S().Debugf("[DTR] Failed to parse peer address: %v", err)
		return
	}
	s := big.NewInt(0).SetBytes(score)

	err = l.registry.UpdatePeerScore(ap.Addr(), s)
	if err != nil {
		zap.S().Debugf("[DTR] Failed to update score of peer '%s': %v", ap.String(), err)
		return
	}
	// Notify loader.
	l.loaderCh <- struct{}{}
}

func (l *Distributor) handleBlockMessage(peer peer.Peer, bm *proto.BlockMessage) {
	b := &proto.Block{}
	if err := b.UnmarshalBinary(bm.BlockBytes, l.scheme); err != nil {
		zap.S().Warnf("[DTR] Failed to unmarshal block from peer '%s': %v", peer.RemoteAddr().String(), err)
		return
	}
	zap.S().Infof("[DTR] Block '%s' received from peer '%s'", b.BlockID().String(), peer.RemoteAddr().String())
	if err := l.handleBlock(b, peer.RemoteAddr()); err != nil {
		zap.S().Warnf("[DTR] Failed to handle block from peer '%s': %v", peer.RemoteAddr().String(), err)
	}
}

func (l *Distributor) handleProtoBlockMessage(peer peer.Peer, bm *proto.PBBlockMessage) {
	b := &proto.Block{}
	if err := b.UnmarshalFromProtobuf(bm.PBBlockBytes); err != nil {
		zap.S().Warnf("[DTR] Failed to unmarshal protobuf block from peer '%s': %v",
			peer.RemoteAddr().String(), err)
	}
	zap.S().Infof("[DTR] Block '%s' received from peer '%s'", b.BlockID().String(), peer.RemoteAddr().String())
	if err := l.handleBlock(b, peer.RemoteAddr()); err != nil {
		zap.S().Warnf("[DTR] Failed to handle block from peer '%s': %v", peer.RemoteAddr().String(), err)
	}
}

func (l *Distributor) handleBlock(block *proto.Block, peer proto.TCPAddr) error {
	ap, err := netip.ParseAddrPort(peer.String())
	if err != nil {
		return fmt.Errorf("failed to parse peer address: %w", err)
	}
	if err = l.drawer.PutBlock(block, ap.Addr()); err != nil && !errors.Is(err, blocks.ErrParentNotFound) {
		return fmt.Errorf("failed to append block: %w", err)
	}
	return nil
}

func (l *Distributor) handlePeersMessage(pm *proto.PeersMessage) {
	zap.S().Debugf("[DTR] Received %d peers", len(pm.Peers))
	addresses := make([]*net.TCPAddr, 0, len(pm.Peers))
	for _, pi := range pm.Peers {
		ap, err := netip.ParseAddrPort(pi.String())
		if err != nil {
			zap.S().Debugf("[DTR] Failed to parse peer address: %v", err)
			continue
		}
		addresses = append(addresses, net.TCPAddrFromAddrPort(ap))
	}
	cnt := l.registry.AppendAddresses(addresses)
	if cnt > 0 {
		zap.S().Infof("[DTR] Added %d new peers", cnt)
	}
}

func (l *Distributor) handleGetPeersMessage(peer peer.Peer) {
	zap.S().Debugf("[DTR] Get peers from %s", peer.RemoteAddr().String())
	friendlyPeers, err := l.registry.FriendlyPeers()
	if err != nil {
		zap.S().Warnf("[DTR] Failed to get peers: %v", err)
		return
	}
	infos := make([]proto.PeerInfo, 0, len(friendlyPeers))
	for _, p := range friendlyPeers {
		pi := proto.PeerInfo{
			Addr: p.TCPAddr().IP,
			Port: p.AddressPort.Port(),
		}
		infos = append(infos, pi)
	}
	peersMessage := &proto.PeersMessage{
		Peers: infos,
	}
	peer.SendMessage(peersMessage)
}

func (l *Distributor) handleSignaturesMessage(peer peer.Peer, signatures []crypto.Signature) {
	zap.S().Debugf("[DTR] Signatures received from %s", peer.RemoteAddr().String())
}

func (l *Distributor) handleBlockIDsMessage(peer peer.Peer, ids []proto.BlockID) {
	zap.S().Debugf("[DTR] Block IDs received from %s", peer.RemoteAddr().String())
}
