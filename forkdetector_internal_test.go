package main

import (
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/wavesplatform/gowaves/pkg/proto"

	"github.com/alexeykiselev/waves-fork-detector/peers"
)

func TestInitializePeersPreservesExpiredSeeds(t *testing.T) {
	path := t.TempDir()
	seed := netip.MustParseAddrPort("1.1.1.1:6868")
	stale := netip.MustParseAddrPort("2.2.2.2:6868")
	fresh := netip.MustParseAddrPort("3.3.3.3:6868")

	// Persist records from a previous run using the peers storage CBOR schema.
	db, err := leveldb.OpenFile(filepath.Join(path, "peers"), nil)
	require.NoError(t, err)
	for _, ap := range []netip.AddrPort{seed, stale, fresh} {
		lastSeen := time.Now().Add(-31 * 24 * time.Hour)
		if ap == fresh {
			lastSeen = time.Now()
		}
		value, marshalErr := cbor.Marshal(map[uint64]any{
			0: ap.Port(),
			3: "1.5.0",
			4: peers.PeerConnected,
			7: lastSeen,
		})
		require.NoError(t, marshalErr)
		key := ap.Addr().As4()
		require.NoError(t, db.Put(key[:], value, nil))
	}
	require.NoError(t, db.Close())

	logger := slog.New(slog.DiscardHandler)
	reg, err := peers.NewRegistry(proto.TestNetScheme, proto.TCPAddr{},
		[]proto.Version{proto.NewVersion(1, 5, 0)}, path, logger)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reg.Close()) })

	initializePeers(reg, []*net.TCPAddr{net.TCPAddrFromAddrPort(seed)}, logger)
	// A subsequent timer-driven prune must also retain the disconnected seed.
	removed, err := reg.PruneStalePeers()
	require.NoError(t, err)
	require.Zero(t, removed)

	available, err := reg.TakeAvailableAddresses()
	require.NoError(t, err)
	require.ElementsMatch(t, []netip.AddrPort{seed, fresh}, available)
	restored, err := reg.Peer(seed.Addr())
	require.NoError(t, err)
	require.Equal(t, peers.PeerConnected, restored.State)
	require.True(t, restored.LastSeen.Before(time.Now().Add(-30*24*time.Hour)))
}
