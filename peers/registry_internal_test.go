package peers

import (
	"log/slog"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

func TestNextVersion5(t *testing.T) {
	vs := newVersions([]proto.Version{
		proto.NewVersion(0, 1, 0),
		proto.NewVersion(0, 2, 0),
		proto.NewVersion(0, 3, 0),
		proto.NewVersion(0, 4, 0),
		proto.NewVersion(0, 5, 0),
	})
	v := vs.bestVersion()
	assert.Equal(t, proto.NewVersion(0, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 4, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 3, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 2, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 1, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 4, 0), v)
}

func TestNextVersion2(t *testing.T) {
	vs := newVersions([]proto.Version{
		proto.NewVersion(1, 5, 4),
		proto.NewVersion(1, 4, 18),
	})
	v := vs.bestVersion()
	assert.Equal(t, proto.NewVersion(1, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(1, 4, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(1, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(1, 4, 0), v)
}

func TestNextVersionZero(t *testing.T) {
	vs := newVersions([]proto.Version{
		proto.NewVersion(1, 5, 4),
		proto.NewVersion(1, 4, 18),
	})
	v := vs.nextVersion(proto.Version{})
	assert.Equal(t, proto.NewVersion(1, 5, 0), v)
}

func newTestRegistry(t *testing.T) *Registry {
	r, err := NewRegistry(proto.TestNetScheme, proto.TCPAddr{}, []proto.Version{proto.NewVersion(1, 5, 0)},
		t.TempDir(), slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func putTestPeer(t *testing.T, r *Registry, ap string, state State, lastSeen time.Time) {
	p := Peer{
		AddressPort: netip.MustParseAddrPort(ap),
		Version:     proto.NewVersion(1, 5, 0),
		State:       state,
		LastSeen:    lastSeen,
	}
	require.NoError(t, r.storage.putPeer(p))
}

func addresses(peers []Peer) []string {
	r := make([]string, len(peers))
	for i, p := range peers {
		r[i] = p.AddressPort.String()
	}
	return r
}

func TestActivePeers(t *testing.T) {
	r := newTestRegistry(t)
	now := time.Now().Round(time.Second)
	putTestPeer(t, r, "1.1.1.1:6868", PeerConnected, now.Add(-time.Hour))      // Recently seen.
	putTestPeer(t, r, "2.2.2.2:6868", PeerConnected, now.Add(-100*time.Hour))  // Seen too long ago.
	putTestPeer(t, r, "3.3.3.3:0", PeerConnected, now.Add(-time.Hour))         // Unknown port.
	putTestPeer(t, r, "4.4.4.4:6868", PeerUnknown, now.Add(-time.Hour))        // Never connected.
	putTestPeer(t, r, "5.5.5.5:6868", PeerHostile, now.Add(-time.Hour))        // Hostile.
	putTestPeer(t, r, "6.6.6.6:6868", PeerConnected, time.Time{})              // Legacy record.
	putTestPeer(t, r, "7.7.7.7:6868", PeerConnected, now.Add(-1000*time.Hour)) // Connected right now.
	r.connections[netip.MustParseAddr("7.7.7.7")] = nil

	active, err := r.ActivePeers()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1.1.1.1:6868", "7.7.7.7:6868"}, addresses(active))
}

func TestPruneStalePeers(t *testing.T) {
	r := newTestRegistry(t)
	now := time.Now().Round(time.Second)
	old := now.Add(-31 * 24 * time.Hour)
	putTestPeer(t, r, "1.1.1.1:6868", PeerConnected, now.Add(-time.Hour)) // Fresh.
	putTestPeer(t, r, "2.2.2.2:6868", PeerConnected, old)                 // Stale.
	putTestPeer(t, r, "3.3.3.3:6868", PeerUnknown, old)                   // Stale, never connected.
	putTestPeer(t, r, "4.4.4.4:6868", PeerHostile, old)                   // Stale hostile.
	putTestPeer(t, r, "5.5.5.5:6868", PeerConnected, time.Time{})         // Legacy record, gets backfilled.
	putTestPeer(t, r, "6.6.6.6:6868", PeerConnected, old)                 // Connected right now.
	putTestPeer(t, r, "7.7.7.7:6868", PeerUnknown, old)                   // Pending connection.
	r.connections[netip.MustParseAddr("6.6.6.6")] = nil
	r.pending[netip.MustParseAddr("7.7.7.7")] = struct{}{}

	n, err := r.PruneStalePeers()
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	all, err := r.storage.peers()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1.1.1.1:6868", "5.5.5.5:6868", "6.6.6.6:6868", "7.7.7.7:6868"}, addresses(all))

	legacy, err := r.storage.peer(netip.MustParseAddr("5.5.5.5"))
	require.NoError(t, err)
	assert.False(t, legacy.LastSeen.IsZero())
}

func TestPruneStalePeersRetainsSeeds(t *testing.T) {
	r := newTestRegistry(t)
	old := time.Now().Add(-31 * 24 * time.Hour).Round(time.Second)
	seed := netip.MustParseAddrPort("1.1.1.1:6868")
	backoff := netip.MustParseAddrPort("2.2.2.2:6868")
	hostile := netip.MustParseAddrPort("3.3.3.3:6868")
	putTestPeer(t, r, seed.String(), PeerConnected, old)
	putTestPeer(t, r, backoff.String(), PeerUnknown, old)
	putTestPeer(t, r, hostile.String(), PeerHostile, old)
	putTestPeer(t, r, "4.4.4.4:6868", PeerConnected, old)
	p, err := r.Peer(backoff.Addr())
	require.NoError(t, err)
	p.NextAttempt = time.Now().Add(time.Hour).Round(time.Second)
	require.NoError(t, r.storage.putPeer(p))

	require.Zero(t, r.AppendSeedAddresses([]*net.TCPAddr{
		net.TCPAddrFromAddrPort(seed), net.TCPAddrFromAddrPort(backoff), net.TCPAddrFromAddrPort(hostile),
	}))
	for _, wantRemoved := range []int{1, 0} {
		removed, pruneErr := r.PruneStalePeers()
		require.NoError(t, pruneErr)
		require.Equal(t, wantRemoved, removed)
	}
	all, err := r.Peers()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{seed.String(), backoff.String(), hostile.String()}, addresses(all))
	for _, retained := range all {
		require.Equal(t, old, retained.LastSeen)
	}
	retained, err := r.Peer(backoff.Addr())
	require.NoError(t, err)
	require.Equal(t, p.NextAttempt, retained.NextAttempt)
	_, err = r.SuggestVersion(hostile.Addr())
	require.Error(t, err)
	active, err := r.ActivePeers()
	require.NoError(t, err)
	require.Empty(t, active)
	available, err := r.TakeAvailableAddresses()
	require.NoError(t, err)
	require.Equal(t, []netip.AddrPort{seed}, available)
}

func TestPruneStalePeersBackfillsLegacyRecords(t *testing.T) {
	for _, kind := range []string{"ordinary", "seed", "connected", "pending"} {
		t.Run(kind, func(t *testing.T) {
			r := newTestRegistry(t)
			ap := netip.MustParseAddrPort("1.1.1.1:6868")
			nextAttempt := time.Now().Add(time.Hour).Round(time.Second)
			// Previous versions persisted no LastSeen field (CBOR key 7).
			data, err := cbor.Marshal(map[uint64]any{
				0: ap.Port(), 3: "1.5.0", 4: PeerConnected, 5: nextAttempt,
			})
			require.NoError(t, err)
			k := key{addr: ap.Addr()}
			require.NoError(t, r.storage.db.Put(k.bytes(), data, nil))
			switch kind {
			case "seed":
				require.Zero(t, r.AppendSeedAddresses([]*net.TCPAddr{net.TCPAddrFromAddrPort(ap)}))
			case "connected":
				r.connections[ap.Addr()] = nil
			case "pending":
				r.pending[ap.Addr()] = struct{}{}
			}

			before := time.Now().Round(time.Second)
			removed, err := r.PruneStalePeers()
			require.NoError(t, err)
			require.Zero(t, removed)
			p, err := r.Peer(ap.Addr())
			require.NoError(t, err)
			require.False(t, p.LastSeen.Before(before))
			require.False(t, p.LastSeen.After(time.Now().Round(time.Second)))
			require.Equal(t, PeerConnected, p.State)
			require.Equal(t, nextAttempt, p.NextAttempt)

			active, err := r.ActivePeers()
			require.NoError(t, err)
			require.Equal(t, []string{ap.String()}, addresses(active))

			// Subsequent pruning must preserve an existing timestamp.
			p.LastSeen = before.Add(-time.Hour)
			require.NoError(t, r.storage.putPeer(p))
			removed, err = r.PruneStalePeers()
			require.NoError(t, err)
			require.Zero(t, removed)
			retained, err := r.Peer(ap.Addr())
			require.NoError(t, err)
			require.Equal(t, p.LastSeen, retained.LastSeen)
		})
	}
}
