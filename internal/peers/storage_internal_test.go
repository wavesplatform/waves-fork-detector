package peers

import (
	"math/big"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

func TestPutGetPeer(t *testing.T) {
	st, err := newStorage(t.TempDir())
	require.NoError(t, err)
	ap := netip.MustParseAddrPort("8.8.8.8:12345")
	p := Peer{
		AddressPort: ap,
		Nonce:       1234567890,
		Name:        "name",
		Version:     proto.NewVersion(1, 2, 3),
		State:       1,
		NextAttempt: time.Now().Add(time.Hour).Round(time.Second),
		Score:       big.NewInt(123),
		p:           nil,
	}
	err = st.putPeer(p)
	require.NoError(t, err)
	p2, err := st.peer(ap.Addr())
	require.NoError(t, err)
	assert.Equal(t, p, p2)
}
