package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/wavesplatform/gowaves/pkg/logging"
	"github.com/wavesplatform/gowaves/pkg/p2p/peer"

	"github.com/alexeykiselev/waves-fork-detector/api"
	"github.com/alexeykiselev/waves-fork-detector/chains"
	"github.com/alexeykiselev/waves-fork-detector/loading"
	"github.com/alexeykiselev/waves-fork-detector/peers"
	"github.com/alexeykiselev/waves-fork-detector/version"
)

const (
	apiNamespace         = "API"
	connectionsNamespace = "CON"
	distributorNamespace = "DTR"
	linkageNamespace     = "LNK"
	listenerNamespace    = "LSN"
	loaderNamespace      = "LDR"
	netNamespace         = "NET"
	netDataNamespace     = "NET.DATA"
	registryNamespace    = "REG"
	respawnNamespace     = "RSP"
)

func main() {
	os.Exit(realMain()) // for more info see https://github.com/golang/go/issues/42078
}

func realMain() int {
	if err := run(); err != nil {
		slog.Error("Failed to run Fork Detector", logging.Error(err))
		return 1
	}
	return 0
}

func run() error {
	p, err := newParameters()
	if err != nil {
		return err
	}

	h := logging.DefaultHandler(p.lp)
	slog.SetDefault(slog.New(h))

	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer done()

	slog.Info("Waves Fork Detector", slog.String("version", version.ForkDetectorVersion()))
	p.log()

	reg, err := peers.NewRegistry(p.scheme, p.declaredAddress, p.versions, p.dbPath,
		newLogger(h, registryNamespace))
	if err != nil {
		return fmt.Errorf("failed to create peers registry: %w", err)
	}
	defer func(reg *peers.Registry) {
		if rcErr := reg.Close(); rcErr != nil {
			slog.Error("Failed to close peers registry", logging.Error(rcErr))
		}
	}(reg)

	n := reg.AppendAddresses(p.seedPeers)
	if n > 0 {
		slog.Info("Seed peers added to storage", slog.Int("count", n))
	}

	linkage, err := chains.NewLinkage(p.dbPath, p.scheme, p.genesis, newLogger(h, linkageNamespace))
	if err != nil {
		return err
	}
	defer linkage.Close()

	linkage.LogInitialStats()

	a, err := api.NewAPI(reg, linkage, p.apiBind, newLogger(h, apiNamespace))
	if err != nil {
		return fmt.Errorf("failed to create API server: %w", err)
	}
	a.Run(ctx)

	parent := peer.NewParent(true)
	nl := buildLogger(h, netNamespace, p.logNetwork)
	ndl := buildLogger(h, netDataNamespace, p.logNetworkData)
	connManger := NewConnectionManager(p.scheme, p.name, p.nonce, p.declaredAddress, reg, parent,
		newLogger(h, connectionsNamespace), nl, ndl)

	listener := NewListener(p.netBind, p.declaredAddress, connManger, newLogger(h, listenerNamespace))
	listener.Run(ctx)

	respawn := NewRespawn(reg, connManger, newLogger(h, respawnNamespace))
	respawn.Run(ctx)

	distributor := NewDistributor(p.scheme, linkage, reg, parent, newLogger(h, distributorNamespace))
	distributor.Run(ctx)

	loader := loading.NewLoader(reg, linkage, distributor.IDsCh(), distributor.BlockCh(),
		newLogger(h, loaderNamespace))
	loader.Run(ctx)

	<-ctx.Done()
	slog.Info("User termination in progress...")

	a.Shutdown()
	listener.Shutdown()
	respawn.Shutdown()
	loader.Shutdown()
	distributor.Shutdown()

	slog.Info("Terminated")

	return nil
}

func buildLogger(h slog.Handler, namespace string, enabled bool) *slog.Logger {
	if !enabled {
		return slog.New(slog.DiscardHandler)
	}
	return newLogger(h, namespace)
}

func newLogger(h slog.Handler, namespace string) *slog.Logger {
	return slog.New(h).With(slog.String(logging.NamespaceKey, namespace))
}
