package main

import (
	"context"
	"net/netip"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/wavesplatform/gowaves/pkg/proto"

	"github.com/rhansen/go-kairos/kairos"

	"github.com/alexeykiselev/waves-fork-detector/peers"
)

const respawnInterval = 10 * time.Second

type Respawn struct {
	ctx   context.Context
	wait  func() error
	timer *kairos.Timer

	reg *peers.Registry
	cm  *ConnectionManager
}

func NewRespawn(reg *peers.Registry, cm *ConnectionManager) *Respawn {
	return &Respawn{
		timer: kairos.NewStoppedTimer(),
		reg:   reg,
		cm:    cm,
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
		zap.S().Warnf("Failed to shutdown Respawn: %v", err)
	}
	zap.S().Info("Respawn shutdown successfully")
}

func (r *Respawn) handleEvents() error {
	for {
		select {
		case <-r.ctx.Done():
			return nil
		case <-r.timer.C:
			addresses, err := r.reg.TakeAvailableAddresses()
			if len(addresses) > 0 {
				zap.S().Infof("Try to establish connections to %d available addresses", len(addresses))
			} else {
				zap.S().Debugf("No available addresses to establish connections")
			}

			if err != nil {
				zap.S().Warnf("Failed to take available addresses: %v", err)
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
				zap.S().Debugf("Failed to establish outbound connection: %v", cErr)
				if urErr := r.reg.UnregisterPeer(ap.Addr()); urErr != nil {
					zap.S().Warnf("Failed to unregister peer on connection failure: %v", urErr)
					return
				}
			}
		}(a)
	}
}
