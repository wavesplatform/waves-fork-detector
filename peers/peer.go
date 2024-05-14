package peers

import (
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"time"

	"github.com/wavesplatform/gowaves/pkg/crypto"
	"go.uber.org/zap"

	"github.com/wavesplatform/gowaves/pkg/p2p/peer"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

type HistoryRequester interface {
	ID() netip.Addr
	RequestBlockIDs(ids []proto.BlockID)
	RequestBlock(id proto.BlockID)
}

type Peer struct {
	AddressPort netip.AddrPort `json:"address"`
	Nonce       uint64         `json:"nonce"`
	Name        string         `json:"name"`
	Version     proto.Version  `json:"version"`
	State       State          `json:"state"`
	NextAttempt time.Time      `json:"next_attempt"`
	Score       *big.Int       `json:"score"`
	p           peer.Peer
}

func (p *Peer) String() string {
	sc := p.Score
	if sc == nil {
		sc = big.NewInt(0)
	}
	return fmt.Sprintf("%s-%d '%s' v%s (%s; %s; %s)", p.AddressPort.String(), p.Nonce, p.Name, p.Version, p.State,
		p.NextAttempt.Format(time.RFC3339), sc.String())
}

func (p *Peer) ID() netip.Addr {
	return p.AddressPort.Addr()
}

func (p *Peer) TCPAddr() *net.TCPAddr {
	ip := p.AddressPort.Addr().As4()
	return &net.TCPAddr{
		IP:   net.IPv4(ip[0], ip[1], ip[2], ip[3]),
		Port: int(p.AddressPort.Port()),
	}
}

func (p *Peer) Send(msg proto.Message) {
	if p.p != nil {
		zap.S().Debugf("Sending message to %s", p.AddressPort.String())
		p.p.SendMessage(msg)
	}
}

func (p *Peer) RequestBlockIDs(ids []proto.BlockID) {
	protobufVersion := proto.NewVersion(1, 2, 0)
	if p.p.Handshake().Version.Cmp(protobufVersion) < 0 {
		sigs := make([]crypto.Signature, len(ids))
		for i, id := range ids {
			sigs[i] = id.Signature()
		}
		zap.S().Debugf("[%s] Requesting signatures for signatures range [%s...%s]",
			p.p.ID().String(), sigs[0].ShortString(), sigs[len(sigs)-1].ShortString())
		p.p.SendMessage(&proto.GetSignaturesMessage{Signatures: sigs})
	} else {
		zap.S().Debugf("[%s] Requesting blocks IDs for IDs range [%s...%s]",
			p.p.ID().String(), ids[0].ShortString(), ids[len(ids)-1].ShortString())
		p.p.SendMessage(&proto.GetBlockIdsMessage{Blocks: ids})
	}
}

func (p *Peer) RequestBlock(id proto.BlockID) {
	p.p.SendMessage(&proto.GetBlockMessage{BlockID: id})
}
