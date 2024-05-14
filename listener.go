package main

import (
	"context"
	"net"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/wavesplatform/gowaves/pkg/proto"
)

type Listener struct {
	ctx  context.Context
	wait func() error

	bind     proto.TCPAddr
	declared proto.TCPAddr

	cm *ConnectionManager
	nl net.Listener
}

func NewListener(bind, declared proto.TCPAddr, cm *ConnectionManager) Service {
	if declared.Empty() {
		zap.S().Info("Declared address of Fork Detector is empty")
		zap.S().Info("No network server will be started")
		return &EmptyService{}
	}
	if bind.Empty() && bind.Port == 0 {
		zap.S().Warn("Bind address is empty")
		zap.S().Info("No network server will be started")
		return &EmptyService{}
	}
	zap.S().Infof("Starting network server on '%s'", bind.String())
	return &Listener{
		bind:     bind,
		declared: declared,
		cm:       cm,
	}
}

func (l *Listener) Run(ctx context.Context) {
	g, gc := errgroup.WithContext(ctx)
	l.ctx = gc
	l.wait = g.Wait

	g.Go(l.run)
}

func (l *Listener) Shutdown() {
	if err := l.nl.Close(); err != nil {
		zap.S().Errorf("Failed to close listener on %s: %v", l.bind, err)
		return
	}
	if err := l.wait(); err != nil {
		zap.S().Warnf("Failed to shutdown Listener: %v", err)
	}
	zap.S().Info("Listener shutdown successfully")
}

func (l *Listener) run() error {
	zap.S().Infof("Start listening on %s", l.bind.String())
	var cfg net.ListenConfig
	nl, err := cfg.Listen(l.ctx, "tcp", l.bind.String())
	if err != nil {
		return err
	}
	l.nl = nl

	for {
		select {
		case <-l.ctx.Done():
			return nil
		default:
			conn, acErr := l.nl.Accept()
			if acErr != nil {
				zap.S().Errorf("Failed to accept connection: %v", acErr)
				continue
			}
			go func() {
				if aErr := l.cm.Accept(l.ctx, conn); aErr != nil {
					zap.S().Debugf("Failed to accept incoming connection from '%s': %v",
						conn.RemoteAddr().String(), aErr)
					return
				}
			}()
		}
	}
}
