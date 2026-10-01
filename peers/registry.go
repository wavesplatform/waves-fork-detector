package peers

import (
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/wavesplatform/gowaves/pkg/logging"
	"github.com/wavesplatform/gowaves/pkg/p2p/peer"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

const (
	delay = 10 * time.Minute
	// activeWindow is the period during which a peer is considered active after it was last seen.
	activeWindow = 96 * time.Hour
	// staleAge is the period after which a peer that was not seen is removed from the storage.
	staleAge = 30 * 24 * time.Hour
)

type Registry struct {
	scheme   proto.Scheme
	versions versions
	declared netip.Addr
	storage  *storage

	mu          sync.Mutex
	connections map[netip.Addr]peer.Peer
	pending     map[netip.Addr]struct{}
	seeds       map[netip.Addr]struct{}

	logger *slog.Logger
}

func NewRegistry(
	scheme proto.Scheme, declared proto.TCPAddr, versions []proto.Version, path string, logger *slog.Logger,
) (*Registry, error) {
	s, err := newStorage(path)
	if err != nil {
		return nil, fmt.Errorf("failed to create peers registry: %w", err)
	}
	ap := netip.AddrPort{}
	if !declared.Empty() {
		ap, err = netip.ParseAddrPort(declared.String())
		if err != nil {
			return nil, fmt.Errorf("failed to parse declared address: %w", err)
		}
	}
	return &Registry{
		scheme:      scheme,
		declared:    ap.Addr(),
		versions:    newVersions(versions),
		storage:     s,
		connections: make(map[netip.Addr]peer.Peer),
		pending:     make(map[netip.Addr]struct{}),
		seeds:       make(map[netip.Addr]struct{}),
		logger:      logger,
	}, nil
}

func (r *Registry) Close() error {
	return r.storage.close()
}

func (r *Registry) RegisterPeer(addr netip.Addr, np peer.Peer, handshake proto.Handshake) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove peer from pending connection list.
	delete(r.pending, addr)

	if !addr.Is4() {
		_ = np.Close()
		return fmt.Errorf("failed to register peer: not an IPv4 address: '%s'", addr.String())
	}
	if addr.Compare(r.declared) == 0 {
		return errors.New("failed to register peer: connection to itself")
	}

	// Look up for peer in the storage.
	p, err := r.storage.peer(addr)
	if err != nil {
		if !errors.Is(err, leveldb.ErrNotFound) {
			_ = np.Close()
			return fmt.Errorf("failed to register peer: %w", err)
		}
		p = Peer{AddressPort: netip.AddrPortFrom(addr, 0)}
	}
	if p.State == PeerHostile {
		_ = np.Close()
		return fmt.Errorf("peer '%s' already registered as hostile", addr.String())
	}

	now := time.Now().Round(time.Second)
	if np.Handshake().Version.CmpMinor(p.Version) >= 2 {
		p.State = PeerHostile
		p.LastSeen = now
		p.Version = np.Handshake().Version
		p.Name = fmt.Sprintf("%s(%s)", np.Handshake().NodeName, np.Handshake().AppName)
		_ = np.Close()
		return r.storage.putPeer(p)
	}
	if np.Handshake().AppName[len(np.Handshake().AppName)-1] != r.scheme {
		p.State = PeerHostile
		p.LastSeen = now
		p.Version = np.Handshake().Version
		p.Name = fmt.Sprintf("%s(%s)", np.Handshake().NodeName, np.Handshake().AppName)
		_ = np.Close()
		return r.storage.putPeer(p)
	}

	port, err := r.checkPort(addr, np, handshake)
	if err != nil {
		return err
	}
	p.AddressPort = netip.AddrPortFrom(addr, port)
	p.Nonce = handshake.NodeNonce
	p.Name = handshake.NodeName
	p.Version = handshake.Version
	p.State = PeerConnected
	p.NextAttempt = now
	p.LastSeen = now
	p.p = np

	r.connections[addr] = np

	return r.storage.putPeer(p)
}

func (r *Registry) UpdatePeerScore(addr netip.Addr, score *big.Int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, err := r.storage.peer(addr)
	if err != nil {
		return fmt.Errorf("failed to update peer score: %w", err)
	}
	p.Score = score
	p.LastSeen = time.Now().Round(time.Second)
	return r.storage.putPeer(p)
}

func (r *Registry) checkPort(addr netip.Addr, np peer.Peer, handshake proto.Handshake) (uint16, error) {
	port := uint16(0)
	if !handshake.DeclaredAddr.Empty() {
		ha, pErr := netip.ParseAddrPort(handshake.DeclaredAddr.String())
		if pErr != nil {
			return 0, fmt.Errorf("failed to register peer: invalid declared address: %w", pErr)
		}
		port = ha.Port()
		if ha.Addr().IsLoopback() {
			_ = np.Close()
			return 0, fmt.Errorf("failed to regiester peer: declared address is loopback: '%s'", ha.String())
		}
		if !ha.Addr().Is4() {
			_ = np.Close()
			return 0, fmt.Errorf("failed to register peer: declared address is not IPv4: '%s'", ha.String())
		}
		if ha.Addr().Compare(addr) != 0 {
			r.logger.Warn("Declared address does not match actual remote address", slog.String("declared", ha.String()),
				slog.String("remote", addr.String()))
			port = 0
		}
	}
	return port, nil
}

func (r *Registry) UnregisterPeer(addr netip.Addr) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, err := r.storage.peer(addr)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			r.logger.Warn("Attempt to unregister unknown peer", slog.String("peer", addr.String()))
		}
		return fmt.Errorf("failed to unregister peer '%s': %w", addr.String(), err)
	}

	delete(r.pending, addr)
	if _, ok := r.connections[addr]; ok {
		// The peer was alive until this moment.
		p.LastSeen = time.Now().Round(time.Second)
		delete(r.connections, addr)
	}

	p.NextAttempt = time.Now().Add(delay).Round(time.Second)
	p.p = nil

	return r.storage.putPeer(p)
}

func (r *Registry) MarkAsHostile(addr net.Addr) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	a, err := netip.ParseAddr(addr.String())
	if err != nil {
		return fmt.Errorf("failed to register peer as hostile: %w", err)
	}

	delete(r.pending, a)

	p, err := r.storage.peer(a)
	if err != nil {
		if !errors.Is(err, leveldb.ErrNotFound) {
			return fmt.Errorf("failed to register peer as hostile: %w", err)
		}
	}

	p.AddressPort = netip.AddrPortFrom(a, 0)
	p.State = PeerHostile
	p.LastSeen = time.Now().Round(time.Second)
	return r.storage.putPeer(p)
}

// Connections returns the list of active connections.
func (r *Registry) Connections() ([]Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	connections := make([]Peer, len(r.connections))
	i := 0
	for a, p := range r.connections {
		sp, err := r.storage.peer(a)
		if err != nil {
			return nil, fmt.Errorf("failed to get active connetcions: %w", err)
		}
		sp.p = p
		sp.logger = r.logger.With(slog.String("peer", a.String()))
		connections[i] = sp
		i++
	}
	sort.Sort(ByName(connections))
	return connections, nil
}

// AppendAddresses adds new addresses to the storage filtering out already known addresses.
// Function returns the number of newly added addresses.
func (r *Registry) AppendAddresses(addresses []*net.TCPAddr) int {
	return r.appendAddresses(addresses, false)
}

// AppendSeedAddresses adds configured seeds and exempts them from stale-peer pruning for this run.
// Existing peer state and retry times are preserved. It returns the number of newly added addresses.
func (r *Registry) AppendSeedAddresses(addresses []*net.TCPAddr) int {
	return r.appendAddresses(addresses, true)
}

func (r *Registry) appendAddresses(addresses []*net.TCPAddr, seeds bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for i := range addresses {
		ap, err := netip.ParseAddrPort(addresses[i].String())
		if err != nil {
			r.logger.Debug("Error adding address", logging.Error(err))
			continue
		}
		if !ap.Addr().Is4() {
			r.logger.Debug("Skipping non-IPv4 address", slog.String("address", ap.String()))
			continue
		}
		if ap.Addr().IsLoopback() {
			r.logger.Debug("Skipping loopback address", slog.String("address", ap.String()))
			continue
		}
		if ap.Addr().Compare(r.declared) == 0 {
			r.logger.Debug("Skipping self address", slog.String("address", ap.String()))
			continue
		}
		if seeds {
			r.seeds[ap.Addr()] = struct{}{}
		}
		yes, err := r.storage.hasPeer(ap.Addr())
		if err != nil {
			r.logger.Debug("Failed to append addresses", logging.Error(err))
			return count
		}
		if !yes {
			p := Peer{
				AddressPort: ap,
				State:       PeerUnknown,
				LastSeen:    time.Now().Round(time.Second),
			}
			if putErr := r.storage.putPeer(p); putErr != nil {
				r.logger.Warn("Failed to append addresses", logging.Error(putErr))
				return count
			}
			count++
		}
	}
	return count
}

func (r *Registry) Peer(addr netip.Addr) (Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.storage.peer(addr)
}

func (r *Registry) Peers() ([]Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	peers, err := r.storage.peers()
	if err != nil {
		return nil, err
	}
	sort.Sort(ByName(peers))
	return peers, nil
}

func (r *Registry) FriendlyPeers() ([]Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	peers, err := r.storage.peers()
	if err != nil {
		return nil, err
	}
	friends := make([]Peer, 0)
	for _, p := range peers {
		if p.State == PeerConnected {
			friends = append(friends, p)
		}
	}
	sort.Sort(ByName(friends))
	return friends, nil
}

// ActivePeers returns the peers suitable for advertising to other nodes: successfully connected at least once,
// with known listening port, and either connected right now or seen during the active window.
func (r *Registry) ActivePeers() ([]Peer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	peers, err := r.storage.peers()
	if err != nil {
		return nil, fmt.Errorf("failed to get active peers: %w", err)
	}
	threshold := time.Now().Add(-activeWindow)
	active := make([]Peer, 0)
	for _, p := range peers {
		if p.State != PeerConnected || p.AddressPort.Port() == 0 {
			continue
		}
		if _, ok := r.connections[p.AddressPort.Addr()]; ok || p.LastSeen.After(threshold) {
			active = append(active, p)
		}
	}
	return active, nil
}

// PruneStalePeers removes the peers that were not seen longer than the stale age.
// Configured seeds are retained regardless of age.
// Peers without the last seen time (stored by previous versions) get the current time to start counting from.
// Function returns the number of removed peers.
func (r *Registry) PruneStalePeers() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	peers, err := r.storage.peers()
	if err != nil {
		return 0, fmt.Errorf("failed to prune stale peers: %w", err)
	}
	now := time.Now().Round(time.Second)
	threshold := now.Add(-staleAge)
	stale := make([]netip.Addr, 0)
	for _, p := range peers {
		addr := p.AddressPort.Addr()
		if _, ok := r.seeds[addr]; ok {
			continue
		}
		if _, ok := r.connections[addr]; ok {
			continue
		}
		if _, ok := r.pending[addr]; ok {
			continue
		}
		if p.LastSeen.IsZero() {
			p.LastSeen = now
			if putErr := r.storage.putPeer(p); putErr != nil {
				return 0, fmt.Errorf("failed to prune stale peers: %w", putErr)
			}
			continue
		}
		if p.LastSeen.Before(threshold) {
			stale = append(stale, addr)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	if dErr := r.storage.deletePeers(stale); dErr != nil {
		return 0, fmt.Errorf("failed to prune stale peers: %w", dErr)
	}
	return len(stale), nil
}

func (r *Registry) Addresses() ([]net.Addr, error) {
	addresses := make([]net.Addr, 0)
	peers, err := r.FriendlyPeers()
	if err != nil {
		return addresses, fmt.Errorf("failed to get public addresses from storage: %w", err)
	}
	for _, p := range peers {
		addresses = append(addresses, p.TCPAddr())
	}
	return addresses, nil
}

// TakeAvailableAddresses returns the list of known non-hostile addresses that are not in the list of active
// connections and not in the list of pending connections.
func (r *Registry) TakeAvailableAddresses() ([]netip.AddrPort, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	addresses := make([]netip.AddrPort, 0)
	peers, err := r.storage.peers()
	if err != nil {
		return addresses, fmt.Errorf("failed to get available addresses from storage: %w", err)
	}
	r.logger.Debug("Getting available addresses", slog.Int("pending", len(r.pending)),
		slog.Int("connected", len(r.connections)))
	for _, p := range peers {
		if p.State == PeerHostile {
			continue
		}
		if p.AddressPort.Port() == 0 {
			continue
		}
		if p.AddressPort.Addr().Compare(r.declared) == 0 {
			continue
		}
		if p.NextAttempt.After(time.Now()) {
			continue
		}
		_, ok := r.connections[p.AddressPort.Addr()]
		if ok {
			continue
		}
		_, ok = r.pending[p.AddressPort.Addr()]
		if ok {
			continue
		}
		addresses = append(addresses, p.AddressPort)
		r.pending[p.AddressPort.Addr()] = struct{}{}
	}
	return addresses, nil
}

func (r *Registry) Broadcast(msg proto.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.connections {
		p.SendMessage(msg)
	}
}

type versions []proto.Version

func newVersions(vs []proto.Version) versions {
	sorted := proto.ByVersion(vs)
	sort.Sort(sort.Reverse(sorted))
	for i, v := range sorted {
		sorted[i] = proto.NewVersion(v.Major(), v.Minor(), 0)
	}
	return versions(sorted)
}

func (vs versions) bestVersion() proto.Version {
	return vs[0]
}

func (vs versions) nextVersion(v proto.Version) proto.Version {
	i := 0
	for ; i < len(vs); i++ {
		x := vs[i]
		if v.Major() == x.Major() && v.Minor() == x.Minor() {
			break
		}
	}
	if i >= len(vs)-1 {
		return vs[0]
	}
	return vs[i+1]
}
