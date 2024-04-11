package peers

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/wavesplatform/gowaves/pkg/proto"
)

func TestPeerNodeString(t *testing.T) {
	ts := time.Time{}
	for _, test := range []struct {
		ap      string
		nonce   uint64
		name    string
		version proto.Version
		next    time.Time
		state   State
		exp     string
	}{
		{"1.2.3.4:1234", 1234567890, "wavesT", proto.NewVersion(0, 16, 0),
			ts, PeerUnknown, "1.2.3.4:1234-1234567890 'wavesT' v0.16.0 (Unknown; 0001-01-01T00:00:00Z)"},
		{"0.0.0.0:5678", 9876543210, "wavesW", proto.NewVersion(0, 15, 0),
			ts, PeerHostile, "0.0.0.0:5678-9876543210 'wavesW' v0.15.0 (Hostile; 0001-01-01T00:00:00Z)"},
		{"127.0.0.1:6666", 0, "", proto.Version{}, ts, PeerHostile,
			"127.0.0.1:6666-0 '' v0.0.0 (Hostile; 0001-01-01T00:00:00Z)"},
		{"0.0.0.0:0", 0, "", proto.Version{}, ts, PeerConnected,
			"0.0.0.0:0-0 '' v0.0.0 (Connected; 0001-01-01T00:00:00Z)"},
	} {
		pn := Peer{
			AddressPort: netip.MustParseAddrPort(test.ap),
			Nonce:       test.nonce,
			Name:        test.name,
			Version:     test.version,
			NextAttempt: test.next,
			State:       test.state,
		}
		assert.Equal(t, test.exp, pn.String())
	}
}

func TestToTCPAddr(t *testing.T) {
	for _, test := range []string{
		"1.2.3.4:1234", "0.0.0.0:5678", "127.0.0.1:6666", "0.0.0.0:0",
	} {
		p := Peer{AddressPort: netip.MustParseAddrPort(test)}
		assert.Equal(t, test, p.TCPAddr().String())
	}
}
