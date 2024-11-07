package chains

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/netip"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"go.uber.org/zap"
)

const (
	longForkThreshold = 5
)

var (
	ErrParentNotFound = fmt.Errorf("parent not found")
	ErrRefNotFound    = fmt.Errorf("reference block not found")
)

type Leasher interface {
	Leash(addr netip.Addr) (proto.BlockID, error)
	MoveLeash(id proto.BlockID, addr netip.Addr) error
}

type HistoryProvider interface {
	Leasher
	LCB(addr netip.Addr) (proto.BlockID, error)
	HasBlock(id proto.BlockID) (bool, error)
	LastIDs(id proto.BlockID, count int) ([]proto.BlockID, error)
	PutBlock(block *proto.Block, addr netip.Addr) error
}

type Linkage struct {
	scheme  proto.Scheme
	genesis proto.BlockID

	mu *sync.RWMutex
	st *storage
}

func NewLinkage(path string, scheme proto.Scheme, genesis proto.Block) (*Linkage, error) {
	st, err := newStorage(path, scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Linkage: %w", err)
	}
	if iniErr := st.initialize(genesis); iniErr != nil {
		return nil, fmt.Errorf("failed to initialize Linkage: %w", iniErr)
	}
	return &Linkage{
		scheme:  scheme,
		genesis: genesis.BlockID(),
		mu:      &sync.RWMutex{},
		st:      st,
	}, nil
}

func (l *Linkage) Close() {
	err := l.st.close()
	if err != nil {
		zap.S().Errorf("Failed to close Linkage: %v", err)
	}
}

func (l *Linkage) PutBlock(block *proto.Block, addr netip.Addr) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	ok, err := l.st.hasBlock(block.BlockID())
	if err != nil {
		return err
	}
	if ok {
		if ulErr := l.st.updateLeash(addr, block.BlockID()); ulErr != nil {
			return ulErr
		}
		return nil
	}
	hasParent, err := l.st.hasBlock(block.Parent)
	if err != nil {
		return err
	}
	if !hasParent {
		return ErrParentNotFound
	}
	if insErr := l.st.putProtoBlock(block); insErr != nil {
		return insErr
	}
	if ulErr := l.st.updateLeash(addr, block.BlockID()); ulErr != nil {
		return ulErr
	}
	return nil
}

func (l *Linkage) PutMicroBlock(inv *proto.MicroBlockInv, addr netip.Addr) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	ok, err := l.st.hasBlock(inv.TotalBlockID)
	if err != nil {
		return err
	}
	if ok {
		if ulErr := l.st.updateLeash(addr, inv.TotalBlockID); ulErr != nil {
			return ulErr
		}
		return nil
	}
	hasRef, err := l.st.hasBlock(inv.Reference)
	if err != nil {
		return err
	}
	if !hasRef {
		return ErrRefNotFound
	}
	if insErr := l.st.putMicroBlock(inv); insErr != nil {
		return insErr
	}
	if ulErr := l.st.updateLeash(addr, inv.TotalBlockID); ulErr != nil {
		return ulErr
	}
	return nil
}

func (l *Linkage) HasLeash(addr netip.Addr) (bool, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	_, err := l.st.leash(addr)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Leash return the block ID the peer is leashed to. For an unleashed peer the genesis block ID is returned.
func (l *Linkage) Leash(addr netip.Addr) (proto.BlockID, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	lsh, err := l.st.leash(addr)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return l.genesis, nil // Return genesis block ID in case of no leash for the peer.
		}
		return proto.BlockID{}, err
	}
	return lsh, nil
}

// LeashScore returns the score of the peer's leash. For an unleashed peer the score of genesis block is returned.
// Error indicates a general storage failure.
func (l *Linkage) LeashScore(addr netip.Addr) (*proto.Score, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	id, err := l.st.leash(addr)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			id = l.genesis
		}
	}
	bl, err := l.st.block(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get block by ID '%s': %w", id.String(), err)
	}
	return bl.Score, nil
}

// Heads returns list of all chains heads.
func (l *Linkage) Heads() ([]Head, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.st.heads()
}

// ActiveHeads returns list of heads pointed by peers.
func (l *Linkage) ActiveHeads() ([]Head, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.activeHeads()
}

func (l *Linkage) LastIDs(id proto.BlockID, count int) ([]proto.BlockID, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.st.getAncestors(id, count)
}

func (l *Linkage) HasBlock(id proto.BlockID) (bool, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.hasBlock(id)
}

func (l *Linkage) Block(id proto.BlockID) (Block, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.block(id)
}

func (l *Linkage) MoveLeash(id proto.BlockID, addr netip.Addr) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.st.updateLeash(addr, id)
}

func (l *Linkage) Leashes() ([]Leash, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.st.leashes()
}

func (l *Linkage) LogInitialStats() {
	l.mu.RLock()
	defer l.mu.RUnlock()

	heads, err := l.activeHeads()
	if err != nil {
		zap.S().Errorf("Failed to log statistics: %v", err)
		return
	}
	zap.S().Infof("Heads count in storage: %d", len(heads))
	for _, head := range heads {
		b, blErr := l.Block(head.BlockID)
		if blErr != nil {
			zap.S().Errorf("Failed to get block: %v", blErr)
			return
		}
		zap.S().Infof("\tHead '%s' at height %d", head.BlockID.String(), b.Height)
	}
	leashes, err := l.st.leashes()
	if err != nil {
		zap.S().Errorf("Failed to log statistics: %v", err)
		return
	}
	zap.S().Infof("Leashes count in storage: %d", len(leashes))
	for _, lsh := range leashes {
		zap.S().Infof("\tPeer '%s' on block '%s'", lsh.Addr.String(), lsh.BlockID.String())
	}
}

func (l *Linkage) Stats() (Stats, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	forks, err := l.forks(nil)
	if err != nil {
		return Stats{}, fmt.Errorf("failed to get stats: %w", err)
	}
	r := Stats{Short: 0, Long: 0}
	for _, f := range forks {
		if f.Length > longForkThreshold {
			r.Long++
		} else {
			r.Short++
		}
	}
	return r, nil
}

// LCB returns the last common block with the longest chain. For the unleashed peer the genesis block ID is returned.
func (l *Linkage) LCB(peer netip.Addr) (proto.BlockID, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	head, err := l.st.leash(peer)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return l.genesis, nil // Return genesis block ID in case of no leash for the peer.
		}
		return proto.BlockID{}, fmt.Errorf("failed to get last common block of '%s': %w", peer.String(), err)
	}
	leashes, err := l.st.leashes()
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to get last common block of '%s': %w", peer.String(), err)
	}
	m := make(map[proto.BlockID][]netip.Addr)
	for _, lsh := range leashes {
		m[lsh.BlockID] = append(m[lsh.BlockID], lsh.Addr)
	}
	score := big.NewInt(0)
	peers := 0
	var longest proto.BlockID
	for k, v := range m {
		b, blErr := l.block(k)
		if blErr != nil {
			return proto.BlockID{}, fmt.Errorf("failed to get last common block of '%s': %w", peer.String(), blErr)
		}
		switch score.Cmp(b.Score) {
		case 0:
			if len(v) > peers {
				score = b.Score
				peers = len(v)
				longest = k
			}
		case -1:
			score = b.Score
			longest = k
			peers = len(v)
		}
	}
	if head == longest { // Peer is on the longest fork.
		return head, nil
	}
	lca, lcaErr := l.st.lca(longest, head)
	if lcaErr != nil {
		return proto.BlockID{}, fmt.Errorf("failed to get last common block of '%s': %w", peer.String(), lcaErr)
	}
	return lca, nil
}

type LookupPeers map[netip.Addr]struct{}

func (l *Linkage) Forks(peers LookupPeers) ([]Fork, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.forks(peers)
}

func (l *Linkage) Fork(peer netip.Addr) (Fork, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	leashes, err := l.st.leashes()
	if err != nil {
		return Fork{}, fmt.Errorf("failed to get fork of '%s': %w", peer.String(), err)
	}
	m := make(map[proto.BlockID][]netip.Addr)
	for _, lsh := range leashes {
		m[lsh.BlockID] = append(m[lsh.BlockID], lsh.Addr)
	}
	score := big.NewInt(0)
	peers := 0
	var longest proto.BlockID
	var fork Fork
	for k, v := range m {
		b, blErr := l.block(k)
		if blErr != nil {
			return Fork{}, fmt.Errorf("failed to get fork of '%s': %w", peer.String(), blErr)
		}
		switch score.Cmp(b.Score) {
		case 0:
			if len(v) > peers {
				score = b.Score
				peers = len(v)
				longest = k
			}
		case -1:
			score = b.Score
			longest = k
			peers = len(v)
		}
		if slices.Contains(v, peer) {
			fork = Fork{
				Longest:         false,
				HeadBlock:       b.ID,
				HeadTimestamp:   time.UnixMilli(b.Timestamp),
				HeadGenerator:   b.Generator,
				HeadHeight:      b.Height,
				Score:           b.Score,
				Peers:           v,
				PeersCount:      len(v),
				LastCommonBlock: b.ID,
				Length:          int(b.Height), // Initially length of the fork is the height of the head block.
			}
		}
	}
	if fork.HeadBlock == longest { // Peer is on the longest fork.
		fork.Longest = true
		fork.Length = 0
		return fork, nil
	}
	lcaID, lcaErr := l.st.lca(longest, fork.HeadBlock)
	if lcaErr != nil {
		return Fork{}, fmt.Errorf("failed to get fork of '%s': %w", peer.String(), lcaErr)
	}
	fork.LastCommonBlock = lcaID // Set last common block ID.
	lcaBlock, blErr := l.st.block(lcaID)
	if blErr != nil {
		return Fork{}, fmt.Errorf("failed to get fork of '%s': %w", peer.String(), blErr)
	}
	fork.Length -= int(lcaBlock.Height) // Update length of the fork.

	return fork, nil
}

// ForkGenerators returns the list of generators with the number of generated blocks for the `count` of ancestor \
// of `blockID` block.
func (l *Linkage) ForkGenerators(blockID proto.BlockID, count int) ([]GeneratorStats, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	m := make(map[proto.WavesAddress]int)
	bs, err := l.st.getBlocks(blockID, count)
	if err != nil {
		return nil, fmt.Errorf("failed to get generators: %w", err)
	}
	for _, b := range bs {
		m[b.Generator]++
	}

	stats := make([]GeneratorStats, 0, len(m))
	for k, v := range m {
		stats = append(stats, GeneratorStats{Generator: k, Blocks: v})
	}
	r := byBlocksCountDesc(stats)
	sort.Sort(r)
	return r, nil
}

func (l *Linkage) activeHeads() ([]Head, error) {
	heads, err := l.st.heads()
	if err != nil {
		return nil, fmt.Errorf("failed to get active leashes: %w", err)
	}
	leashes, err := l.st.leashes()
	if err != nil {
		return nil, fmt.Errorf("failed to get active leashes: %w", err)
	}
	leashed := make(map[proto.BlockID]struct{})
	for _, lsh := range leashes {
		leashed[lsh.BlockID] = struct{}{}
	}
	r := make([]Head, 0, len(heads))
	for _, h := range heads {
		if _, ok := leashed[h.BlockID]; ok {
			r = append(r, h)
		}
	}
	return r, nil
}

func (l *Linkage) hasBlock(id proto.BlockID) (bool, error) {
	return l.st.hasBlock(id)
}

func (l *Linkage) block(id proto.BlockID) (Block, error) {
	b, err := l.st.block(id)
	if err != nil {
		return Block{}, err
	}
	bID, err := proto.NewBlockIDFromBytes(b.ID)
	if err != nil {
		return Block{}, fmt.Errorf("failed to get block: %w", err)
	}
	var pID proto.BlockID
	if len(b.Parent) > 0 {
		pID, err = proto.NewBlockIDFromBytes(b.Parent)
		if err != nil {
			return Block{}, fmt.Errorf("failed to get block: %w", err)
		}
	}
	ga, err := proto.NewAddressFromBytes(b.Generator)
	if err != nil {
		return Block{}, fmt.Errorf("failed to get block: %w", err)
	}
	return Block{
		ID:        bID,
		Parent:    pID,
		Height:    b.Height,
		Generator: ga,
		Score:     b.Score,
		Timestamp: safeTimestamp(b.Timestamp),
	}, nil
}

func safeTimestamp(ts uint64) int64 {
	if ts < math.MaxInt64 {
		return int64(ts)
	}
	panic("timestamp is too large")
}

func (l *Linkage) forks(peers LookupPeers) ([]Fork, error) {
	leashes, err := l.st.leashes()
	if err != nil {
		return nil, fmt.Errorf("failed to get forks: %w", err)
	}
	m := make(map[proto.BlockID][]netip.Addr)
	for _, lsh := range leashes {
		if _, ok := peers[lsh.Addr]; len(peers) > 0 && !ok {
			continue // Skip leash if the peer is not in the map, but only for non-empty peers.
		}
		m[lsh.BlockID] = append(m[lsh.BlockID], lsh.Addr)
	}
	r := make([]Fork, 0, len(m))
	for k, v := range m {
		b, blErr := l.block(k)
		if blErr != nil {
			return nil, fmt.Errorf("failed to get forks: %w", blErr)
		}
		frk := Fork{
			Longest:         false,
			HeadBlock:       b.ID,
			HeadTimestamp:   time.UnixMilli(b.Timestamp),
			HeadGenerator:   b.Generator,
			HeadHeight:      b.Height,
			Score:           b.Score,
			Peers:           v,
			PeersCount:      len(v),
			LastCommonBlock: b.ID,
			Length:          int(b.Height), // Initially length of the fork is the height of the head block.
		}
		r = append(r, frk)
	}
	sort.Sort(byScoreAndPeersDesc(r))

	longest := r[0]
	r[0].Longest = true

	for i := range r {
		lcaID, lcaErr := l.st.lca(longest.HeadBlock, r[i].HeadBlock)
		if lcaErr != nil {
			return nil, fmt.Errorf("failed to get forks: %w", lcaErr)
		}
		r[i].LastCommonBlock = lcaID // Set last common block ID.
		lcaBlock, blErr := l.st.block(lcaID)
		if blErr != nil {
			return nil, fmt.Errorf("failed to get forks: %w", blErr)
		}
		r[i].Length -= int(lcaBlock.Height) // Update length of the fork.
	}

	return r, nil
}
