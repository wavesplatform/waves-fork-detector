package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/wavesplatform/gowaves/pkg/logging"
	"github.com/wavesplatform/gowaves/pkg/p2p/peer"

	"github.com/alexeykiselev/waves-fork-detector/internal"
	"github.com/alexeykiselev/waves-fork-detector/internal/blocks"
	"github.com/alexeykiselev/waves-fork-detector/internal/peers"
)

var (
	version = "v0.0.0"
)

func main() {
	if err := run(); err != nil {
		if _, errErr := fmt.Fprintf(os.Stderr, "Error: %v\n", err); errErr != nil {
			return
		}
		os.Exit(1)
	}
	os.Exit(0)
}

func run() error {
	p, err := newParameters()
	if err != nil {
		return err
	}

	logger := logging.SetupLogger(p.logLevel, logging.NetworkDataFilter(false))
	defer func() {
		if syncErr := logger.Sync(); syncErr != nil && errors.Is(err, os.ErrInvalid) {
			panic(fmt.Sprintf("Failed to close logging subsystem: %v\n", syncErr))
		}
	}()

	ctx, done := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer done()

	zap.S().Infof("Waves Fork Detector %s", version)
	p.log()

	reg, err := peers.NewRegistry(p.scheme, p.declaredAddress, p.versions, p.dbPath)
	if err != nil {
		zap.S().Errorf("Failed to create peers registry: %v", err)
		return err
	}
	defer func(reg *peers.Registry) {
		if rcErr := reg.Close(); rcErr != nil {
			zap.S().Errorf("Failed to close peers registry: %v", rcErr)
		}
	}(reg)

	n := reg.AppendAddresses(p.seedPeers)
	if n > 0 {
		zap.S().Infof("%d seed peers added to storage", n)
	}

	drawer, err := blocks.NewDrawer(p.dbPath, p.scheme, p.genesis)
	if err != nil {
		zap.S().Errorf("Failed to create blocks drawer: %v", err)
		return err
	}
	defer drawer.Close()

	api, err := internal.NewAPI(reg, drawer, p.apiBind)
	if err != nil {
		zap.S().Errorf("Failed to create API server: %v", err)
		return err
	}
	api.Run(ctx)

	parent := peer.NewParent()
	connManger := internal.NewConnectionManager(p.scheme, p.name, p.nonce, p.declaredAddress, reg, parent)

	listener := internal.NewListener(p.netBind, p.declaredAddress, connManger)
	listener.Run(ctx)

	respawn := internal.NewRespawn(reg, connManger)
	respawn.Run(ctx)

	distributor, err := internal.NewDistributor(p.scheme, drawer, reg, parent)
	if err != nil {
		zap.S().Errorf("Failed to instantiate distributor: %v", err)
		return err
	}
	distributor.Run(ctx)

	<-ctx.Done()
	zap.S().Info("User termination in progress...")

	api.Shutdown()
	listener.Shutdown()
	respawn.Shutdown()
	distributor.Shutdown()

	zap.S().Info("Terminated")

	return nil
}
