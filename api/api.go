package api

import (
	"compress/flate"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/netip"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/alexeykiselev/waves-fork-detector/chains"
	"github.com/alexeykiselev/waves-fork-detector/peers"
	"github.com/alexeykiselev/waves-fork-detector/version"
)

const defaultTimeout = 30 * time.Second

var (
	//go:embed swagger
	res embed.FS
)

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

type API struct {
	ctx      context.Context
	wait     func() error
	registry *peers.Registry
	linkage  *chains.Linkage
	srv      *http.Server
}

func NewAPI(registry *peers.Registry, linkage *chains.Linkage, bind string) (*API, error) {
	if bind == "" {
		return nil, errors.New("empty address to bin")
	}
	swaggerFS, err := fs.Sub(res, "swagger")
	if err != nil {
		return nil, fmt.Errorf("failed to get swagger FS: %w", err)
	}

	a := API{registry: registry, linkage: linkage}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(Logger(zap.L()))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(flate.DefaultCompression))
	const apiRoot = "/api"
	r.Mount(apiRoot, a.routes(apiRoot, swaggerFS))
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

func (a *API) routes(rootMountPath string, swaggerFS fs.FS) chi.Router {
	r := chi.NewRouter()
	swaggerUI := http.StripPrefix(rootMountPath, http.FileServer(http.FS(swaggerFS)))
	r.Mount("/", swaggerUI)      // swagger UI weil be served at the rootMountPath + ("/index.html" or "/")
	r.Group(func(r chi.Router) { // API routes
		r.Use(middleware.SetHeader("Content-Type", "application/json"))
		r.Get("/peers/all", a.peers)
		r.Get("/peers/friendly", a.friendly)
		r.Get("/connections", a.connections)
		r.Get("/heads", a.heads)
		r.Get("/leashes", a.leashes)
		r.Get("/status", a.status)
		r.Get("/forks", a.forks)
		r.Get("/active-forks", a.activeForks)
		r.Get("/fork/{address}", a.fork)
		r.Get("/fork/{address}/generators/{blocks}", a.forkGenerators)
	})
	return r
}

// status returns status information.
func (a *API) status(w http.ResponseWriter, _ *http.Request) {
	goroutines := runtime.NumGoroutine()
	stats, err := a.linkage.Stats()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
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
	connections, err := a.registry.Connections()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	s := status{
		Version:             version.ForkDetectorVersion(),
		ShortForksCount:     stats.Short,
		LongForksCount:      stats.Long,
		AllPeersCount:       len(all),
		FriendlyPeersCount:  len(friends),
		ConnectedPeersCount: len(connections),
		GoroutinesCount:     goroutines,
	}
	err = json.NewEncoder(w).Encode(s)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal status to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

// peers returns the list of all known peers.
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

// friendly returns the list of peers that have been successfully connected at least once.
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

// connections returns the list of active connections.
func (a *API) connections(w http.ResponseWriter, _ *http.Request) {
	connections, err := a.registry.Connections()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	err = json.NewEncoder(w).Encode(connections)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal connections to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

// heads returns the combined info about heads for all connected peers.
func (a *API) heads(w http.ResponseWriter, _ *http.Request) {
	heads, err := a.linkage.ActiveHeads()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	infos := make([]headInfo, len(heads))
	for i, h := range heads {
		b, blErr := a.linkage.Block(h.BlockID)
		if blErr != nil {
			http.Error(w, fmt.Sprintf("Failed to complete request: %v", blErr), http.StatusInternalServerError)
			return
		}
		infos[i] = headInfo{
			Number:    h.ID,
			ID:        h.BlockID.String(),
			Height:    b.Height,
			Score:     b.Score,
			Timestamp: time.UnixMilli(b.Timestamp),
		}
	}
	err = json.NewEncoder(w).Encode(infos)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal heads to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

// leashes returns the list of all known leashes grouped by block IDs.
func (a *API) leashes(w http.ResponseWriter, _ *http.Request) {
	leashes, err := a.linkage.Leashes()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	m := make(map[proto.BlockID][]string)
	for _, l := range leashes {
		m[l.BlockID] = append(m[l.BlockID], l.Addr.String())
	}
	r := make([]leashInfo, 0, len(m))
	for k, v := range m {
		b, blErr := a.linkage.Block(k)
		if blErr != nil {
			http.Error(w, fmt.Sprintf("Failed to complete request: %v", blErr), http.StatusInternalServerError)
			return
		}
		li := leashInfo{
			BlockID:    b.ID.String(),
			Height:     b.Height,
			Score:      b.Score,
			Timestamp:  time.UnixMilli(b.Timestamp),
			Generator:  b.Generator,
			PeersCount: len(v),
			Peers:      v,
		}
		r = append(r, li)
	}
	sort.Sort(byScoreAndPeersDesc(r))
	err = json.NewEncoder(w).Encode(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal leashes to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func (a *API) forks(w http.ResponseWriter, r *http.Request) {
	ps, err := getPeers(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusBadRequest)
		return
	}
	forks, err := a.linkage.Forks(ps)
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

// activeForks returns the combined info about forks for all ever connected peers.
func (a *API) activeForks(w http.ResponseWriter, r *http.Request) {
	rps, err := getPeers(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusBadRequest)
		return
	}
	connections, err := a.registry.Connections()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	size := len(connections)
	if s := len(rps); s > 0 {
		size = min(size, s)
	}
	ps := make(map[netip.Addr]struct{}, size)
	for _, conn := range connections {
		if _, ok := rps[conn.ID()]; len(rps) > 0 && !ok {
			continue
		}
		ps[conn.ID()] = struct{}{}
	}
	forks, err := a.linkage.Forks(ps)
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

// fork returns the info about fork of the given peer.
func (a *API) fork(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "address")
	peer, err := netip.ParseAddr(addr)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid peer address '%s'", addr), http.StatusBadRequest)
		return
	}
	ok, err := a.linkage.HasLeash(peer)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, fmt.Sprintf("Peer '%s' is unknown", addr), http.StatusNotFound)
		return
	}
	fork, err := a.linkage.Fork(peer)
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

// forkGenerators returns the list of generators of the fork of the given peer.
func (a *API) forkGenerators(w http.ResponseWriter, r *http.Request) {
	addr := chi.URLParam(r, "address")
	peer, err := netip.ParseAddr(addr)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid peer address '%s'", addr), http.StatusBadRequest)
		return
	}

	count := chi.URLParam(r, "blocks")
	c, err := strconv.Atoi(count)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid number of blocks '%s'", addr), http.StatusBadRequest)
		return
	}

	ok, err := a.linkage.HasLeash(peer)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, fmt.Sprintf("Peer '%s' is unknown", addr), http.StatusNotFound)
		return
	}
	fork, err := a.linkage.Fork(peer)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}

	if c == 0 {
		c = fork.Length
		if fork.Longest {
			c = 1000 // For the longest fork we get only generators of 1000 last blocks.
		}
	}

	generators, err := a.linkage.ForkGenerators(fork.HeadBlock, c)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to complete request: %v", err), http.StatusInternalServerError)
		return
	}
	err = json.NewEncoder(w).Encode(generators)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to marshal to JSON: %v", err), http.StatusInternalServerError)
		return
	}
}

func getPeers(r *http.Request) (map[netip.Addr]struct{}, error) {
	const peersParam = "peers"
	ss := strings.Split(r.URL.Query().Get(peersParam), ",")
	m := make(map[netip.Addr]struct{}, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if len(s) > 0 {
			p, err := netip.ParseAddr(s)
			if err != nil {
				return nil, fmt.Errorf("invalid peer %q: %w", s, err)
			}
			m[p] = struct{}{}
		}
	}
	return m, nil
}
