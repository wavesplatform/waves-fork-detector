package main

import (
	"context"
	"log/slog"
	"net/netip"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/wavesplatform/gowaves/pkg/logging"
	"github.com/wavesplatform/gowaves/pkg/proto"

	"github.com/rhansen/go-kairos/kairos"

	"github.com/alexeykiselev/waves-fork-detector/peers"
)

const respawnInterval = 10 * time.Second

type Respawn struct {
	ctx   context.Context
	wait  func() error
	timer *kairos.Timer

	reg    *peers.Registry
	cm     *ConnectionManager
	logger *slog.Logger
}

func NewRespawn(reg *peers.Registry, cm *ConnectionManager, logger *slog.Logger) *Respawn {
	return &Respawn{
		timer:  kairos.NewStoppedTimer(),
		reg:    reg,
		cm:     cm,
		logger: logger,
	}
}

func (r *Respawn) Run(ctx context.Context) {
	g, gc := errgroup.WithContext(ctx)
	r.ctx = gc
	r.wait = g.Wait
	r.timer.Reset(respawnInterval)

	g.Go(r.handleEvents)
}

func (r *Respawn) Shutdown() {
	if err := r.wait(); err != nil {
		r.logger.Warn("Failed to shutdown Respawn", logging.Error(err))
	}
	r.logger.Info("Respawn shutdown successfully")
}

func (r *Respawn) handleEvents() error {
	for {
		select {
		case <-r.ctx.Done():
			return nil
		case <-r.timer.C:
			addresses, err := r.reg.TakeAvailableAddresses()
			if len(addresses) > 0 {
				r.logger.Info("Trying to establish connections to available addresses", slog.Int("count", len(addresses)))
			} else {
				r.logger.Debug("No available addresses to establish connections")
			}

			if err != nil {
				r.logger.Warn("Failed to take available addresses", logging.Error(err))
				continue
			}
			r.establishConnections(addresses)
			r.timer.Reset(respawnInterval)
		}
	}
}

func (r *Respawn) establishConnections(addresses []netip.AddrPort) {
	for _, a := range addresses {
		go func(ap netip.AddrPort) {
			addr := proto.NewTCPAddrFromString(ap.String())
			if cErr := r.cm.Connect(r.ctx, addr); cErr != nil {
				r.logger.Debug("Failed to establish outbound connection", slog.String("address", ap.String()), logging.Error(cErr))
				if urErr := r.reg.UnregisterPeer(ap.Addr()); urErr != nil {
					r.logger.Warn("Failed to unregister peer on connection failure", slog.String("address", ap.String()),
						logging.Error(urErr))
					return
				}
			}
		}(a)
	}
}
