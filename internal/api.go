package internal

import (
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"runtime"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/alexeykiselev/waves-fork-detector/internal/blocks"
	"github.com/alexeykiselev/waves-fork-detector/internal/peers"
)

const defaultTimeout = 30 * time.Second

// Logger is a middleware that logs the start and end of each request, along
// with some useful data about what was requested, what the response status was,
// and how long it took to return.
func Logger(l *zap.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)

			t1 := time.Now()
			defer func() {
				l.Debug("Served",
					zap.String("proto", r.Proto),
					zap.String("path", r.URL.Path),
					zap.String("remote", r.RemoteAddr),
					zap.Duration("lat", time.Since(t1)),
					zap.Int("status", ww.Status()),
					zap.Int("size", ww.BytesWritten()),
					zap.String("reqId", middleware.GetReqID(r.Context())))
			}()

			next.ServeHTTP(ww, r)
		}
		return http.HandlerFunc(fn)
	}
}

type status struct {
	ShortForksCount     int `json:"short_forks_count"`
	LongForksCount      int `json:"long_forks_count"`
	AllPeersCount       int `json:"all_peers_count"`
	FriendlyPeersCount  int `json:"friendly_peers_count"`
	ConnectedPeersCount int `json:"connected_peers_count"`
	TotalBlocksCount    int `json:"total_blocks_count"`
	GoroutinesCount     int `json:"goroutines_count"`
}

type API struct {
	ctx      context.Context
	wait     func() error
	registry *peers.Registry
	drawer   *blocks.Drawer
	srv      *http.Server
}

type PublicAddressInfo struct {
	Address         string    `json:"address"`
	Version         string    `json:"version"`
	Status          string    `json:"status"`
	Attempts        int       `json:"attempts"`
	NextAttemptTime time.Time `json:"next_attempt_time"`
}

func NewAPI(registry *peers.Registry, drawer *blocks.Drawer, bind string) (*API, error) {
	if bind == "" {
		return nil, errors.New("empty address to bin")
	}
	a := API{registry: registry, drawer: drawer}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(Logger(zap.L()))
	r.Use(middleware.Recoverer)
	r.Use(middleware.SetHeader("Content-Type", "application/json"))
	r.Use(middleware.Compress(flate.DefaultCompression))
	r.Mount("/api", a.routes())
	a.srv = &http.Server{Addr: bind, Handler: r, ReadHeaderTimeout: defaultTimeout, ReadTimeout: defaultTimeout}
	return &a, nil
}

func (a *API) Run(ctx context.Context) {
	g, gc := errgroup.WithContext(ctx)
	a.ctx = gc
	a.wait = g.Wait

	g.Go(a.runServer)
}

func (a *API) Shutdown() {
	if err := a.srv.Shutdown(a.ctx); err != nil && !errors.Is(err, context.Canceled) {
		zap.S().Errorf("Failed to shutdown API: %v", err)
	}
	if err := a.wait(); err != nil {
		zap.S().Warnf("Failed to shutdown API: %v", err)
	}
	zap.S().Info("API shutdown successfully")
}

func (a *API) runServer() error {
	err := a.srv.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		zap.S().Fatalf("Failed to start API: %v", err)
		return err
	}
	return nil
}

func (a *API) routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/status", a.status)           // Status information
	r.Get("/peers/all", a.peers)         // Returns the list of all known peers
	r.Get("/peers/friendly", a.friendly) // Returns the list of peers that have been successfully connected at least once
	r.Get("/connections", a.connections) // Returns the list of active connections
	r.Get("/forks", a.forks)             // Returns the combined info about forks for all connected peers
	r.Get("/all-forks", a.allForks)      // Returns the combined info about all registered forks
	r.Get("/fork/{address}", a.fork)     // Returns the info about fork of the given peer
	return r
}

func (a *API) status(w http.ResponseWriter, _ *http.Request) {
	goroutines := runtime.NumGoroutine()
	stats := a.drawer.Stats()
	all, err := a.registry.Peers()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	friends, err := a.registry.FriendlyPeers()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	connections := a.registry.Connections()
	s := status{
		ShortForksCount:     stats.Short,
		LongForksCount:      stats.Long,
		AllPeersCount:       len(all),
		FriendlyPeersCount:  len(friends),
		ConnectedPeersCount: len(connections),
		TotalBlocksCount:    stats.Blocks,
		GoroutinesCount:     goroutines,
	}
	err = json.NewEncoder(w).Encode(s)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal status to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func (a *API) peers(w http.ResponseWriter, _ *http.Request) {
	all, err := a.registry.Peers()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	err = json.NewEncoder(w).Encode(all)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal peers to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func (a *API) friendly(w http.ResponseWriter, _ *http.Request) {
	friendly, err := a.registry.FriendlyPeers()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	err = json.NewEncoder(w).Encode(friendly)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal peers to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func (a *API) connections(w http.ResponseWriter, _ *http.Request) {
	connections := a.registry.Connections()
	err := json.NewEncoder(w).Encode(connections)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal connections to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func (a *API) forks(w http.ResponseWriter, _ *http.Request) {
	nodes := a.registry.Connections()
	addresses := make([]netip.Addr, len(nodes))
	for i, n := range nodes {
		addresses[i] = n.AddressPort.Addr()
	}
	forks, err := a.drawer.Forks(addresses)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	err = json.NewEncoder(w).Encode(forks)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal status to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func (a *API) allForks(w http.ResponseWriter, _ *http.Request) {
	nodes, err := a.registry.Peers()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	addresses := make([]netip.Addr, len(nodes))
	for i, n := range nodes {
		addresses[i] = n.AddressPort.Addr()
	}
	forks, err := a.drawer.Forks(addresses)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	err = json.NewEncoder(w).Encode(forks)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal status to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func (a *API) fork(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "address")
	peer, err := netip.ParseAddr(addr)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid peer address '%s'", addr), http.StatusBadRequest)
		return
	}
	fork, err := a.drawer.Fork(peer)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	err = json.NewEncoder(w).Encode(fork)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal status to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}
