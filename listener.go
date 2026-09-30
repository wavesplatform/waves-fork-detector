package main

import (
	"context"
	"log/slog"
	"net"

	"golang.org/x/sync/errgroup"

	"github.com/wavesplatform/gowaves/pkg/logging"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

type Listener struct {
	ctx  context.Context
	wait func() error

	bind     proto.TCPAddr
	declared proto.TCPAddr

	cm *ConnectionManager
	nl net.Listener

	logger *slog.Logger
}

func NewListener(bind, declared proto.TCPAddr, cm *ConnectionManager, logger *slog.Logger) Service {
	if declared.Empty() {
		logger.Info("Declared address of Fork Detector is empty")
		logger.Info("No network server will be started")
		return &EmptyService{}
	}
	if bind.Empty() && bind.Port == 0 {
		logger.Warn("Bind address is empty")
		logger.Warn("No network server will be started")
		return &EmptyService{}
	}
	logger.Info("Starting network server", slog.String("bind", bind.String()))
	return &Listener{
		bind:     bind,
		declared: declared,
		cm:       cm,
		logger:   logger,
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
		l.logger.Error("Failed to close listener", slog.String("bind", l.bind.String()), logging.Error(err))
		return
	}
	if err := l.wait(); err != nil {
		l.logger.Warn("Failed to shutdown Listener", logging.Error(err))
	}
	l.logger.Info("Listener shutdown successfully")
}

func (l *Listener) run() error {
	l.logger.Info("Start listening", slog.String("bind", l.bind.String()))
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
				l.logger.Error("Failed to accept connection", logging.Error(acErr))
				continue
			}
			go func() {
				if aErr := l.cm.Accept(l.ctx, conn); aErr != nil {
					l.logger.Debug("Failed to accept incoming connection",
						slog.String("remote", conn.RemoteAddr().String()), logging.Error(aErr))
					return
				}
			}()
		}
	}
}
