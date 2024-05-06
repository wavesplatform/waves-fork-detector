package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"unicode"

	"go.uber.org/zap"

	"github.com/wavesplatform/gowaves/pkg/logging"
	"github.com/wavesplatform/gowaves/pkg/p2p/peer"

	"github.com/alexeykiselev/waves-fork-detector/internal"
	"github.com/alexeykiselev/waves-fork-detector/internal/chains"
	"github.com/alexeykiselev/waves-fork-detector/internal/peers"
)

var (
	version = "v0.0.0"
)

func main() {
	if err := run(); err != nil {
		zap.S().Error(capitalize(err.Error()))
		if _, errErr := fmt.Fprintf(os.Stderr, "%s\n", capitalize(err.Error())); errErr != nil {
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
		return fmt.Errorf("failed to create peers registry: %w", err)
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

	linkage, err := chains.NewLinkage(p.dbPath, p.scheme, p.genesis)
	if err != nil {
		return err
	}
	defer linkage.Close()

	api, err := internal.NewAPI(reg, linkage, p.apiBind)
	if err != nil {
		return fmt.Errorf("failed to create API server: %w", err)
	}
	api.Run(ctx)

	parent := peer.NewParent(false)
	connManger := internal.NewConnectionManager(p.scheme, p.name, p.nonce, p.declaredAddress, reg, parent)

	listener := internal.NewListener(p.netBind, p.declaredAddress, connManger)
	listener.Run(ctx)

	respawn := internal.NewRespawn(reg, connManger)
	respawn.Run(ctx)

	distributor, err := internal.NewDistributor(p.scheme, linkage, reg, parent)
	if err != nil {
		return fmt.Errorf("failed to instantiate distributor: %w", err)
	}
	distributor.Run(ctx)

	loader := internal.NewLoader(reg, linkage, distributor.IDsCh(), distributor.BlockCh())
	loader.Run(ctx)

	<-ctx.Done()
	zap.S().Info("User termination in progress...")

	api.Shutdown()
	listener.Shutdown()
	respawn.Shutdown()
	loader.Shutdown()
	distributor.Shutdown()

	zap.S().Info("Terminated")

	return nil
}

func capitalize(str string) string {
	runes := []rune(str)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
