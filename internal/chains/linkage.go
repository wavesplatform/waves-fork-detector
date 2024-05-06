package chains

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"go.uber.org/zap"
)

var (
	ErrBlockNotFound  = fmt.Errorf("block not found")
	ErrUnleashedPeer  = fmt.Errorf("peer not leashed")
	ErrParentNotFound = fmt.Errorf("parent not found")
)

type Linkage struct {
	scheme  proto.Scheme
	genesis proto.BlockID

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

func (l *Linkage) Leash(addr netip.Addr) (proto.BlockID, error) {
	lsh, err := l.st.leash(addr)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return proto.BlockID{}, ErrUnleashedPeer
		}
		return proto.BlockID{}, err
	}
	return lsh, nil
}

// LeashScore returns the score of the peer's leash. For an unleashed peer the score of genesis block is returned.
// Error indicates a general storage failure.
func (l *Linkage) LeashScore(addr netip.Addr) (*proto.Score, error) {
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

func (l *Linkage) BlockScore(id proto.BlockID) (*proto.Score, error) {
	bl, err := l.st.block(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get block by ID '%s': %w", id.String(), err)
	}
	return bl.Score, nil
}

// Heads returns list of all chains heads sorted by score descendant order.
func (l *Linkage) Heads() ([]Head, error) {
	return l.st.heads()
}

func (l *Linkage) LastIDs(id proto.BlockID, count int) ([]proto.BlockID, error) {
	return l.st.getAncestors(id, count)
}

func (l *Linkage) Stats() Stats {
	return Stats{}
}

func (l *Linkage) Forks(addresses []netip.Addr) ([]Fork, error) {
	_ = addresses
	return nil, nil
}

func (l *Linkage) Fork(peer netip.Addr) ([]Fork, error) {
	_ = peer
	return nil, nil
}

func (l *Linkage) hasBlock(id proto.BlockID) (bool, error) {
	return l.st.hasBlock(id)
}
