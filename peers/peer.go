package peers

import (
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"time"

	"github.com/wavesplatform/gowaves/pkg/crypto"

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
	LastSeen    time.Time      `json:"last_seen"`
	p           peer.Peer
	logger      *slog.Logger
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

// log returns the peer's logger, or a discarding logger if the peer has no active connection.
func (p *Peer) log() *slog.Logger {
	if p.logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return p.logger
}

func (p *Peer) Send(msg proto.Message) {
	if p.p != nil {
		p.log().Debug("Sending message")
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
		p.log().Debug("Requesting signatures for signatures range",
			slog.String("first", sigs[0].ShortString()), slog.String("last", sigs[len(sigs)-1].ShortString()))
		p.p.SendMessage(&proto.GetSignaturesMessage{Signatures: sigs})
	} else {
		p.log().Debug("Requesting blocks IDs for IDs range",
			slog.String("first", ids[0].ShortString()), slog.String("last", ids[len(ids)-1].ShortString()))
		p.p.SendMessage(&proto.GetBlockIDsMessage{Blocks: ids})
	}
}

func (p *Peer) RequestBlock(id proto.BlockID) {
	p.p.SendMessage(&proto.GetBlockMessage{BlockID: id})
}
