package main

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"time"

	"github.com/rhansen/go-kairos/kairos"
	"github.com/wavesplatform/gowaves/pkg/crypto"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

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
}

func NewDistributor(
	scheme proto.Scheme, linkage *chains.Linkage, registry *peers.Registry, parent peer.Parent,
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
		zap.S().Warnf("Failed to shutdown Distributor: %v", err)
	}
	close(d.idsCh)
	close(d.blockCh)
	zap.S().Info("Distributor shutdown successfully")
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
			zap.S().Debugf("[DTR] Distributor shutdown in progress...")
			return nil
		case <-d.timer.C:
			zap.S().Info("[DTR] Ping connections")
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
		zap.S().Warnf("[DTR] Failed to parse address: %v", err)
		return
	}
	if rpErr := d.registry.RegisterPeer(ap.Addr(), cm.Peer, cm.Peer.Handshake()); rpErr != nil {
		zap.S().Warnf("[DTR] Failed to check peer: %v", rpErr)
		return
	}
}

func (d *Distributor) handleInternalError(peer peer.Peer, ie *peer.InternalErr) {
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
	if urErr := d.registry.UnregisterPeer(ap.Addr()); urErr != nil {
		zap.S().Warnf("[DTR] Failed to unregister peer: %v", urErr)
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
	case *proto.BlockIdsMessage:
		d.handleBlockIDsMessage(msg.ID, v.Blocks)
	}
}

func (d *Distributor) handleScoreMessage(peer peer.Peer, score []byte) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		zap.S().Debugf("[DTR] Failed to parse peer address: %v", err)
		return
	}
	s := big.NewInt(0).SetBytes(score)
	zap.S().Debugf("[DTR] New score %s recevied from %s", s.String(), ap.Addr().String())
	err = d.registry.UpdatePeerScore(ap.Addr(), s)
	if err != nil {
		zap.S().Debugf("[DTR] Failed to update score of peer '%s': %v", ap.String(), err)
		return
	}
}

func (d *Distributor) handleBlockMessage(peer peer.Peer, bm *proto.BlockMessage) {
	b := &proto.Block{}
	if err := b.UnmarshalBinary(bm.BlockBytes, d.scheme); err != nil {
		zap.S().Warnf("[DTR] Failed to unmarshal block from peer '%s': %v", peer.RemoteAddr().String(), err)
		return
	}
	zap.S().Infof("[DTR] Block '%s' received from peer '%s'", b.BlockID().String(), peer.RemoteAddr().String())
	if err := d.handleBlock(b, peer.RemoteAddr()); err != nil {
		zap.S().Warnf("[DTR] Failed to handle block from peer '%s': %v", peer.RemoteAddr().String(), err)
	}
}

func (d *Distributor) handleProtoBlockMessage(peer peer.Peer, bm *proto.PBBlockMessage) {
	b := &proto.Block{}
	if err := b.UnmarshalFromProtobuf(bm.PBBlockBytes); err != nil {
		zap.S().Warnf("[DTR] Failed to unmarshal protobuf block from peer '%s': %v",
			peer.RemoteAddr().String(), err)
		return
	}
	zap.S().Infof("[DTR] Block '%s' received from peer '%s'", b.BlockID().String(), peer.RemoteAddr().String())
	if err := d.handleBlock(b, peer.RemoteAddr()); err != nil {
		zap.S().Warnf("[DTR] Failed to handle block from peer '%s': %v", peer.RemoteAddr().String(), err)
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
	zap.S().Debugf("[DTR] Block '%s' from '%s' was appended", block.BlockID().String(), ap.Addr().String())
	d.blockCh <- loading.BlockPackage{Peer: ap.Addr(), Block: block}
	return nil
}

func (d *Distributor) handlePeersMessage(pm *proto.PeersMessage) {
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
	cnt := d.registry.AppendAddresses(addresses)
	if cnt > 0 {
		zap.S().Infof("[DTR] Added %d new peers", cnt)
	}
}

func (d *Distributor) handleGetPeersMessage(peer peer.Peer) {
	zap.S().Debugf("[DTR] Get peers from %s", peer.RemoteAddr().String())
	friendlyPeers, err := d.registry.FriendlyPeers()
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

func (d *Distributor) handleSignaturesMessage(peer peer.Peer, signatures []crypto.Signature) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		zap.S().Errorf("[DTR] Failed to parse peer address: %v", err)
		return
	}
	ids := make([]proto.BlockID, len(signatures))
	for i, s := range signatures {
		ids[i] = proto.NewBlockIDFromSignature(s)
	}
	zap.S().Debugf("[DTR] Signatures received from %s", peer.RemoteAddr().String())
	d.idsCh <- loading.IDsPackage{Peer: ap.Addr(), IDs: ids}
}

func (d *Distributor) handleBlockIDsMessage(peer peer.Peer, ids []proto.BlockID) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		zap.S().Errorf("[DTR] Failed to parse peer address: %v", err)
		return
	}
	zap.S().Debugf("[DTR] Block IDs [%s..%s] received from %s",
		ids[0].ShortString(), ids[len(ids)-1].ShortString(), peer.RemoteAddr().String())
	d.idsCh <- loading.IDsPackage{Peer: ap.Addr(), IDs: ids}
}

func (d *Distributor) handleMicroBlockInvMessage(peer peer.Peer, msg *proto.MicroBlockInvMessage) {
	ap, err := netip.ParseAddrPort(peer.RemoteAddr().String())
	if err != nil {
		zap.S().Errorf("[DTR] Failed to parse peer address: %v", err)
		return
	}
	inv := &proto.MicroBlockInv{}
	if umErr := inv.UnmarshalBinary(msg.Body); umErr != nil {
		zap.S().Errorf("[DTR] Failed to unmarshal MicroBlockInv message: %v", umErr)
		return
	}
	if putErr := d.linkage.PutMicroBlock(inv, ap.Addr()); putErr != nil {
		zap.S().Errorf("[DTR] Failed to append micro-block '%s': %v", inv.TotalBlockID.String(), putErr)
		return
	}
	zap.S().Debugf("[DTR] MicroBlockInv '%s' received from peer '%s'", inv.TotalBlockID.String(), ap.String())
}
