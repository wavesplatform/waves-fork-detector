package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"time"

	"github.com/rhansen/go-kairos/kairos"
	"github.com/wavesplatform/gowaves/pkg/crypto"
	"golang.org/x/sync/errgroup"

	"github.com/wavesplatform/gowaves/pkg/logging"
	"github.com/wavesplatform/gowaves/pkg/p2p/peer"
	"github.com/wavesplatform/gowaves/pkg/proto"

	"github.com/alexeykiselev/waves-fork-detector/chains"
	"github.com/alexeykiselev/waves-fork-detector/loading"
	"github.com/alexeykiselev/waves-fork-detector/peers"
)

const pingInterval = 1 * time.Minute

type Distributor struct {
	ctx  context.Context
	wait func() error

	scheme   proto.Scheme
	registry *peers.Registry
	linkage  *chains.Linkage
	parent   peer.Parent

	idsCh   chan loading.IDsPackage
	blockCh chan loading.BlockPackage

	timer *kairos.Timer

	logger *slog.Logger
}

func NewDistributor(
	scheme proto.Scheme, linkage *chains.Linkage, registry *peers.Registry, parent peer.Parent, logger *slog.Logger,
) *Distributor {
	idsCh := make(chan loading.IDsPackage)
	blockCh := make(chan loading.BlockPackage)
	return &Distributor{
		scheme:   scheme,
		linkage:  linkage,
		registry: registry,
		parent:   parent,
		idsCh:    idsCh,
		blockCh:  blockCh,
		timer:    kairos.NewStoppedTimer(),
		logger:   logger,
	}
}

func (d *Distributor) Run(ctx context.Context) {
	g, gc := errgroup.WithContext(ctx)
	d.ctx = gc
	d.wait = g.Wait

	g.Go(d.runLoop)
	d.timer.Reset(pingInterval)
}

func (d *Distributor) Shutdown() {
	if err := d.wait(); err != nil {
		d.logger.Warn("Failed to shutdown Distributor", logging.Error(err))
	}
	close(d.idsCh)
	close(d.blockCh)
	d.logger.Info("Distributor shutdown successfully")
}

func (d *Distributor) IDsCh() <-chan loading.IDsPackage {
	return d.idsCh
}

func (d *Distributor) BlockCh() <-chan loading.BlockPackage {
	return d.blockCh
}

func (d *Distributor) runLoop() error {
	for {
		select {
		case <-d.ctx.Done():
			d.logger.Debug("Distributor shutdown in progress...")
			return nil
		case <-d.timer.C:
			d.logger.Info("Pinging connections")
			d.pingConnections()
			d.timer.Reset(pingInterval)
		case infoMessage := <-d.parent.InfoCh:
			d.handleInfoMessage(infoMessage)
		case message := <-d.parent.MessageCh:
			d.handleMessage(message)
		}
	}
}

func (d *Distributor) pingConnections() {
	d.registry.Broadcast(&proto.GetPeersMessage{})
}

func (d *Distributor) handleInfoMessage(msg peer.InfoMessage) {
	switch v := msg.Value.(type) {
	case *peer.Connected:
		d.handleConnected(v)
	case *peer.InternalErr:
		d.handleInternalError(msg.Peer, v)
	}
}

func (d *Distributor) handleConnected(cm *peer.Connected) {
	ap, err := netip.ParseAddrPort(cm.Peer.RemoteAddr().String())
	if err != nil {
		d.logger.Warn("Failed to parse address", logging.Error(err))
		return
	}
	if rpErr := d.registry.RegisterPeer(ap.Addr(), cm.Peer, cm.Peer.Handshake()); rpErr != nil {
		d.logger.Warn("Failed to check peer", logging.Error(rpErr))
		return
	}
}

func (d *Distributor) handleInternalError(peer peer.Peer, ie *peer.InternalErr) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		d.logger.Warn("Failed to parse address", logging.Error(err))
		return
	}
	d.logger.Info("Closing connection with peer", slog.String("peer", ap.Addr().String()))
	d.logger.Debug("Peer failed with error", slog.String("peer", ap.Addr().String()), logging.Error(ie.Err))
	if clErr := peer.Close(); clErr != nil {
		d.logger.Warn("Failed to close peer connection", slog.String("peer", ap.Addr().String()), logging.Error(clErr))
	}
	if urErr := d.registry.UnregisterPeer(ap.Addr()); urErr != nil {
		d.logger.Warn("Failed to unregister peer", slog.String("peer", ap.Addr().String()), logging.Error(urErr))
	}
}

func (d *Distributor) handleMessage(msg peer.ProtoMessage) {
	switch v := msg.Message.(type) {
	case *proto.GetPeersMessage:
		d.handleGetPeersMessage(msg.ID)
	case *proto.PeersMessage:
		d.handlePeersMessage(v)
	case *proto.SignaturesMessage:
		d.handleSignaturesMessage(msg.ID, v.Signatures)
	case *proto.BlockMessage:
		d.handleBlockMessage(msg.ID, v)
	case *proto.ScoreMessage:
		d.handleScoreMessage(msg.ID, v.Score)
	case *proto.MicroBlockInvMessage:
		d.handleMicroBlockInvMessage(msg.ID, v)
	case *proto.PBBlockMessage:
		d.handleProtoBlockMessage(msg.ID, v)
	case *proto.BlockIDsMessage:
		d.handleBlockIDsMessage(msg.ID, v.Blocks)
	}
}

func (d *Distributor) handleScoreMessage(peer peer.Peer, score []byte) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		d.logger.Debug("Failed to parse peer address", logging.Error(err))
		return
	}
	s := big.NewInt(0).SetBytes(score)
	d.logger.Debug("New score received", slog.String("score", s.String()), slog.String("peer", ap.Addr().String()))
	err = d.registry.UpdatePeerScore(ap.Addr(), s)
	if err != nil {
		d.logger.Debug("Failed to update score of peer", slog.String("peer", ap.Addr().String()), logging.Error(err))
		return
	}
}

func (d *Distributor) handleBlockMessage(peer peer.Peer, bm *proto.BlockMessage) {
	b := &proto.Block{}
	if err := b.UnmarshalBinary(bm.BlockBytes, d.scheme); err != nil {
		d.logger.Warn("Failed to unmarshal block from peer", slog.String("peer", peer.RemoteAddr().String()),
			logging.Error(err))
		return
	}
	d.logger.Info("Block received from peer", slog.String("block", b.BlockID().String()),
		slog.String("peer", peer.RemoteAddr().String()))
	if err := d.handleBlock(b, peer.RemoteAddr()); err != nil {
		d.logger.Warn("Failed to handle block from peer", slog.String("peer", peer.RemoteAddr().String()), logging.Error(err))
	}
}

func (d *Distributor) handleProtoBlockMessage(peer peer.Peer, bm *proto.PBBlockMessage) {
	b := &proto.Block{}
	if err := b.UnmarshalFromProtobuf(bm.PBBlockBytes); err != nil {
		d.logger.Warn("Failed to unmarshal protobuf block from peer", slog.String("peer", peer.RemoteAddr().String()),
			logging.Error(err))
		return
	}
	d.logger.Info("Block received from peer", slog.String("block", b.BlockID().String()),
		slog.String("peer", peer.RemoteAddr().String()))
	if err := d.handleBlock(b, peer.RemoteAddr()); err != nil {
		d.logger.Warn("Failed to handle block from peer", slog.String("peer", peer.RemoteAddr().String()), logging.Error(err))
	}
}

func (d *Distributor) handleBlock(block *proto.Block, addr proto.TCPAddr) error {
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return fmt.Errorf("failed to parse peer remote address: %w", err)
	}
	if putErr := d.linkage.PutBlock(block, ap.Addr()); putErr != nil && !errors.Is(putErr, chains.ErrParentNotFound) {
		return fmt.Errorf("failed to append block: %w", err)
	}
	d.logger.Debug("Block was appended", slog.String("block", block.BlockID().String()),
		slog.String("peer", ap.Addr().String()))
	d.blockCh <- loading.BlockPackage{Peer: ap.Addr(), Block: block}
	return nil
}

func (d *Distributor) handlePeersMessage(pm *proto.PeersMessage) {
	d.logger.Debug("Peers received", slog.Int("count", len(pm.Peers)))
	addresses := make([]*net.TCPAddr, 0, len(pm.Peers))
	for _, pi := range pm.Peers {
		ap, err := netip.ParseAddrPort(pi.String())
		if err != nil {
			d.logger.Debug("Failed to parse peer address", logging.Error(err))
			continue
		}
		addresses = append(addresses, net.TCPAddrFromAddrPort(ap))
	}
	cnt := d.registry.AppendAddresses(addresses)
	if cnt > 0 {
		d.logger.Info("New peers added", slog.Int("count", cnt))
	}
}

func (d *Distributor) handleGetPeersMessage(peer peer.Peer) {
	d.logger.Debug("Get peers request received", slog.String("peer", peer.RemoteAddr().String()))
	friendlyPeers, err := d.registry.FriendlyPeers()
	if err != nil {
		d.logger.Warn("Failed to get peers", logging.Error(err))
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

func (d *Distributor) handleSignaturesMessage(peer peer.Peer, signatures []crypto.Signature) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		d.logger.Warn("Failed to parse peer address", logging.Error(err))
		return
	}
	ids := make([]proto.BlockID, len(signatures))
	for i, s := range signatures {
		ids[i] = proto.NewBlockIDFromSignature(s)
	}
	d.logger.Debug("Signatures received", slog.String("peer", ap.Addr().String()))
	d.idsCh <- loading.IDsPackage{Peer: ap.Addr(), IDs: ids}
}

func (d *Distributor) handleBlockIDsMessage(peer peer.Peer, ids []proto.BlockID) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		d.logger.Warn("Failed to parse peer address", logging.Error(err))
		return
	}
	if len(ids) == 0 {
		d.logger.Warn("Empty IDs list received", slog.String("peer", ap.Addr().String()))
		return
	}
	d.logger.Debug("Block IDs received", slog.String("first", ids[0].ShortString()),
		slog.String("last", ids[len(ids)-1].ShortString()), slog.String("peer", ap.Addr().String()))
	d.idsCh <- loading.IDsPackage{Peer: ap.Addr(), IDs: ids}
}

func (d *Distributor) handleMicroBlockInvMessage(peer peer.Peer, msg *proto.MicroBlockInvMessage) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		d.logger.Warn("Failed to parse peer address", logging.Error(err))
		return
	}
	inv := &proto.MicroBlockInv{}
	if umErr := inv.UnmarshalBinary(msg.Body); umErr != nil {
		d.logger.Warn("Failed to unmarshal MicroBlockInv message", logging.Error(umErr))
		return
	}
	if putErr := d.linkage.PutMicroBlock(inv, ap.Addr()); putErr != nil {
		d.logger.Warn("Failed to append micro-block", slog.String("block", inv.TotalBlockID.String()),
			slog.String("peer", ap.Addr().String()), logging.Error(putErr))
		return
	}
	d.logger.Info("Micro-block received from peer", slog.String("block", inv.TotalBlockID.String()),
		slog.String("peer", ap.Addr().String()))
}
