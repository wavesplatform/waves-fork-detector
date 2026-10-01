package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/wavesplatform/gowaves/pkg/proto"

	"github.com/alexeykiselev/waves-fork-detector/peers"
)

func TestAPIBindFailureTerminatesProcess(t *testing.T) {
	var cfg net.ListenConfig
	occupied, err := cfg.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, occupied.Close()) })
	executable, err := os.Executable()
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		bind     string
		declared string
	}{
		{name: "no peer listener", bind: "127.0.0.1:0"},
		{name: "starting peer listener", bind: "127.0.0.1:0", declared: "127.0.0.1:6868"},
		{name: "failed peer listener", bind: occupied.Addr().String(), declared: "127.0.0.1:6868"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestAPIProcessHelper$", "--",
				"-db", t.TempDir(), "-api", occupied.Addr().String(),
				"-net", tc.bind, "-declared-address", tc.declared)
			cmd.Env = append(os.Environ(), "FORK_DETECTOR_API_PROCESS_TEST=1")
			output, runErr := cmd.CombinedOutput()
			require.NoError(t, ctx.Err(), "process did not terminate: %s", output)
			var exitErr *exec.ExitError
			require.ErrorAs(t, runErr, &exitErr, "%s", output)
			require.Equal(t, 1, exitErr.ExitCode(), "%s", output)
			require.Contains(t, string(output), "API server failed")
			require.Contains(t, string(output), "Terminated")
		})
	}
}

func TestAPIProcessHelper(_ *testing.T) {
	if os.Getenv("FORK_DETECTOR_API_PROCESS_TEST") != "1" {
		return
	}
	// Isolate application flags and os.Exit in the child process.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = append(os.Args[:1], os.Args[3:]...)
	os.Exit(realMain())
}

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
