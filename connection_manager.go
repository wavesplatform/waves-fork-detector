package main

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"go.uber.org/zap"

	"github.com/wavesplatform/gowaves/pkg/p2p/incoming"
	"github.com/wavesplatform/gowaves/pkg/p2p/outgoing"
	"github.com/wavesplatform/gowaves/pkg/p2p/peer"
	"github.com/wavesplatform/gowaves/pkg/proto"

	"github.com/alexeykiselev/waves-fork-detector/peers"
)

const (
	defaultApplication = "waves"
)

type ConnectionManager struct {
	network   string
	name      string
	nonce     uint32
	declared  proto.TCPAddr
	parent    peer.Parent
	vp        peers.VersionProvider
	discarded map[proto.PeerMessageID]bool
}

func NewConnectionManager(
	scheme byte, name string, nonce uint32, declared proto.TCPAddr, vp peers.VersionProvider, parent peer.Parent,
) *ConnectionManager {
	discardedMessages := map[proto.PeerMessageID]bool{
		proto.ContentIDGetPeers:                  false,
		proto.ContentIDPeers:                     false,
		proto.ContentIDGetSignatures:             true,
		proto.ContentIDSignatures:                false,
		proto.ContentIDGetBlock:                  true,
		proto.ContentIDBlock:                     false,
		proto.ContentIDScore:                     false,
		proto.ContentIDTransaction:               true,
		proto.ContentIDInvMicroblock:             false,
		proto.ContentIDCheckpoint:                true,
		proto.ContentIDMicroblockRequest:         true,
		proto.ContentIDMicroblock:                true,
		proto.ContentIDPBBlock:                   false,
		proto.ContentIDPBMicroBlock:              true,
		proto.ContentIDPBTransaction:             true,
		proto.ContentIDGetBlockIDs:               true,
		proto.ContentIDBlockIDs:                  false,
		proto.ContentIDGetBlockSnapshot:          true,
		proto.ContentIDMicroBlockSnapshot:        true,
		proto.ContentIDBlockSnapshot:             true,
		proto.ContentIDMicroBlockSnapshotRequest: true,
	}
	return &ConnectionManager{
		network:   fmt.Sprintf("%s%c", defaultApplication, scheme),
		name:      name,
		nonce:     nonce,
		declared:  declared,
		parent:    parent,
		vp:        vp,
		discarded: discardedMessages,
	}
}

func (h *ConnectionManager) Accept(ctx context.Context, conn net.Conn) error {
	zap.S().Debugf("New incoming connection from %s", conn.RemoteAddr())

	ap, err := netip.ParseAddrPort(conn.RemoteAddr().String())
	if err != nil {
		return fmt.Errorf("failed to handle incoming connection: %w", err)
	}
	ver, err := h.vp.SuggestVersion(ap.Addr())
	if err != nil {
		return err
	}
	params := incoming.PeerParams{
		WavesNetwork: h.network,
		Conn:         conn,
		Skip:         h.skipFunc,
		Parent:       h.parent,
		DeclAddr:     h.declared,
		NodeName:     h.name,
		NodeNonce:    uint64(h.nonce),
		Version:      ver,
	}

	return incoming.RunIncomingPeer(ctx, params)
}

func (h *ConnectionManager) Connect(ctx context.Context, addr proto.TCPAddr) error {
	zap.S().Debugf("New outgoing connection to %s", addr)
	params := outgoing.EstablishParams{
		Address:      addr,
		WavesNetwork: h.network,
		Parent:       h.parent,
		DeclAddr:     h.declared,
		Skip:         h.skipFunc,
		NodeName:     h.name,
		NodeNonce:    uint64(h.nonce),
	}
	ap, err := netip.ParseAddrPort(addr.String())
	if err != nil {
		return err
	}
	ver, err := h.vp.SuggestVersion(ap.Addr())
	if err != nil {
		return err
	}
	return outgoing.EstablishConnection(ctx, params, ver)
}

func (h *ConnectionManager) skipFunc(header proto.Header) bool {
	if r, ok := h.discarded[header.ContentID]; ok {
		return r
	}
	return false
}
