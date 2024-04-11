package peers

import (
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"time"

	"go.uber.org/zap"

	"github.com/wavesplatform/gowaves/pkg/p2p/peer"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

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
	return fmt.Sprintf("%s-%d '%s' v%s (%s; %s; %s)", p.AddressPort.String(), p.Nonce, p.Name, p.Version, p.State,
		p.NextAttempt.Format(time.RFC3339), p.Score.String())
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
