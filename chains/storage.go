package chains

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"path/filepath"

	"github.com/fxamacker/cbor/v2"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

const (
	storagePath = "blocks"

	prefixSize    = 1
	uint64Size    = 8
	digestSize    = 32
	signatureSize = 64
)

const (
	counterPrefix byte = iota
	blockPrefix
	chainPrefix
	leashPrefix
	headToBlockPrefix
	blockToHeadPrefix
)
const (
	blocksCounter byte = iota
	headsCounter
)

type storage struct {
	db *leveldb.DB

	scheme proto.Scheme
}

func newStorage(path string, scheme proto.Scheme) (*storage, error) {
	db, err := leveldb.OpenFile(filepath.Clean(filepath.Join(path, storagePath)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open blocks storage: %w", err)
	}
	return &storage{db: db, scheme: scheme}, nil
}

func (s *storage) initialize(genesis proto.Block) error {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return fmt.Errorf("failed to initialize blocks storage: %w", err)
	}
	defer sn.Release()
	blocks, err := s.getCounter(sn, blocksCounter)
	if err != nil {
		return fmt.Errorf("failed to initialize blocks storage: %w", err)
	}
	if blocks == 0 {
		ga, gaErr := proto.NewAddressFromPublicKey(s.scheme, genesis.GeneratorPublicKey)
		if gaErr != nil {
			return fmt.Errorf("failed to initialize blocks storage: %w", gaErr)
		}
		batch := new(leveldb.Batch)
		b := block{
			Height:    1,
			Timestamp: genesis.Timestamp,
			Score:     calculateScore(genesis.BaseTarget),
			ID:        genesis.BlockID().Bytes(),
			Generator: ga.Bytes(),
		}
		if pbErr := s.putBlock(batch, 1, b); pbErr != nil {
			return fmt.Errorf("failed to initialize blocks storage: %w", pbErr)
		}
		s.putCounter(batch, blocksCounter, 1)
		s.putID(batch, blockPrefix, genesis.BlockID(), 1)
		l := link{
			BlockID: genesis.BlockID().Bytes(),
			Height:  1,
			Parents: nil,
		}
		if plErr := s.putLink(batch, 1, l); plErr != nil {
			return fmt.Errorf("failed to initialize blocks storage: %w", plErr)
		}
		// Initialize heads storage with one head pointing genesis block.
		s.putCounter(batch, headsCounter, 1)
		s.putNum(batch, headToBlockPrefix, 1, 1)
		s.putNum(batch, blockToHeadPrefix, 1, 1)
		if wrErr := s.db.Write(batch, nil); wrErr != nil {
			return fmt.Errorf("failed to initialize blocks storage: %w", wrErr)
		}
	}
	return nil
}

func (s *storage) close() error {
	return s.db.Close()
}

func (s *storage) block(id proto.BlockID) (block, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return block{}, fmt.Errorf("failed to get block: %w", err)
	}
	defer sn.Release()
	return s.getBlockByID(sn, id)
}

func (s *storage) hasBlock(id proto.BlockID) (bool, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return false, fmt.Errorf("failed to check block existence: %w", err)
	}
	defer sn.Release()
	_, err = s.getNumOfBlockID(sn, id)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check block existence: %w", err)
	}
	return true, nil
}

func (s *storage) putProtoBlock(block *proto.Block) error {
	// Get next block number.
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return fmt.Errorf("failed to put block: %w", err)
	}
	defer sn.Release()
	n, err := s.getCounter(sn, blocksCounter)
	if err != nil {
		return fmt.Errorf("failed to put block: %w", err)
	}
	// Get parent block and its number.
	parentNum, err := s.getNumOfBlockID(sn, block.Parent)
	if err != nil {
		return fmt.Errorf("failed to put block: %w", err)
	}

	parent, err := s.getBlock(sn, parentNum)
	if err != nil {
		return fmt.Errorf("failed to put block: %w", err)
	}

	batch := new(leveldb.Batch)

	// Put next block number.
	n++
	s.putCounter(batch, blocksCounter, n)

	b, err := newBlock(s.scheme, block, parent.Height+1, parent.Score)
	if err != nil {
		return fmt.Errorf("failed to put block: %w", err)
	}

	s.putID(batch, blockPrefix, block.BlockID(), n) // Save reference ID -> num.
	// Put information about block by its number.
	if putErr := s.putBlock(batch, n, b); putErr != nil {
		return fmt.Errorf("failed to put block: %w", putErr)
	}

	// Put block into the chain.
	if lnkErr := s.putNewLinkForBlock(sn, batch, n, block.BlockID(), b.Height, block.Parent); lnkErr != nil {
		return fmt.Errorf("failed to put block: %w", lnkErr)
	}
	if phErr := s.putHead(sn, batch, n, parentNum); phErr != nil {
		return fmt.Errorf("failed to put block: %w", phErr)
	}
	return s.db.Write(batch, nil)
}

func (s *storage) putMicroBlock(inv *proto.MicroBlockInv) error {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return fmt.Errorf("failed to put micro-block: %w", err)
	}
	defer sn.Release()
	n, err := s.getCounter(sn, blocksCounter)
	if err != nil {
		return fmt.Errorf("failed to put micro-block: %w", err)
	}
	// Get reference block and its number.
	refNum, err := s.getNumOfBlockID(sn, inv.Reference)
	if err != nil {
		return fmt.Errorf("failed to put micro-block: %w", err)
	}

	ref, err := s.getBlock(sn, refNum)
	if err != nil {
		return fmt.Errorf("failed to put micro-block: %w", err)
	}

	parentID, err := proto.NewBlockIDFromBytes(ref.Parent)
	if err != nil {
		return fmt.Errorf("failed to put micro-block: %w", err)
	}

	batch := new(leveldb.Batch)

	// Put next block number.
	n++
	s.putCounter(batch, blocksCounter, n)

	b := block{
		Height:    ref.Height,
		Timestamp: ref.Timestamp,
		Score:     ref.Score,
		ID:        inv.TotalBlockID.Bytes(),
		Parent:    ref.Parent,
		Generator: ref.Generator,
	}

	s.putID(batch, blockPrefix, inv.TotalBlockID, n) // Save reference ID -> num.
	// Put information about block by its number.
	if putErr := s.putBlock(batch, n, b); putErr != nil {
		return fmt.Errorf("failed to put micro-block: %w", putErr)
	}

	// Put block into the chain.
	if lnkErr := s.putNewLinkForBlock(sn, batch, n, inv.TotalBlockID, b.Height, parentID); lnkErr != nil {
		return fmt.Errorf("failed to put micro-block: %w", lnkErr)
	}
	if phErr := s.putHead(sn, batch, n, refNum); phErr != nil {
		return fmt.Errorf("failed to put micro-block: %w", phErr)
	}
	return s.db.Write(batch, nil)
}

func (s *storage) getAncestors(id proto.BlockID, count int) ([]proto.BlockID, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return nil, fmt.Errorf("failed to get ancestors: %w", err)
	}
	defer sn.Release()

	n, err := s.getNumOfBlockID(sn, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get ancestors: %w", err)
	}
	r := make([]proto.BlockID, 0, count)
	i := 0
	for i < count {
		l, glErr := s.getLink(sn, n)
		if glErr != nil {
			return nil, fmt.Errorf("failed to get ancestors: %w", glErr)
		}
		pid, pidErr := proto.NewBlockIDFromBytes(l.BlockID)
		if pidErr != nil {
			return nil, fmt.Errorf("failed to get ancestors: %w", pidErr)
		}
		r = append(r, pid)
		if len(l.Parents) == 0 {
			break
		}
		n = l.Parents[0]
		i++
	}
	return r, nil
}

func (s *storage) lca(id1, id2 proto.BlockID) (proto.BlockID, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to find common ancestor: %w", err)
	}
	defer sn.Release()

	l1, err := s.getLinkByID(sn, id1)
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to find common ancestor: %w", err)
	}
	l2, err := s.getLinkByID(sn, id2)
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to find common ancestor: %w", err)
	}

	// The link which is present farthest from the genesis block is taken as farthest.
	farthest, other := l1, l2
	if farthest.Height < other.Height {
		farthest, other = other, farthest
	}
	// Finding the ancestor of farthest which is at same level as other link.
	farthest, err = s.fastBackwardFarthest(sn, farthest, other)
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to find common ancestor: %w", err)
	}
	// If the links are the same, then the common ancestor is found.
	if bytes.Equal(farthest.BlockID, other.BlockID) {
		return proto.NewBlockIDFromBytes(farthest.BlockID)
	}
	// Finding the link closest to the genesis block which is not the common ancestor of farthest and other i.e.
	// a link x such that x is not the common ancestor of farthest and other but x.Parents[0] is.
	for i := len(farthest.Parents) - 1; i >= 0; i-- {
		if farthest.Parents[i] != other.Parents[i] {
			farthest, err = s.getLink(sn, farthest.Parents[i])
			if err != nil {
				return proto.BlockID{}, fmt.Errorf("failed to find common ancestor: %w", err)
			}
			other, err = s.getLink(sn, other.Parents[i])
			if err != nil {
				return proto.BlockID{}, fmt.Errorf("failed to find common ancestor: %w", err)
			}
		}
	}
	l, err := s.getLink(sn, farthest.Parents[0])
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to find common ancestor: %w", err)
	}
	return proto.NewBlockIDFromBytes(l.BlockID)
}

func (s *storage) fastBackwardFarthest(sn *leveldb.Snapshot, farthest link, other link) (link, error) {
	i := len(farthest.Parents) - 1
	for i >= 0 {
		if farthest.Height-(1<<i) >= other.Height {
			l, err := s.getLink(sn, farthest.Parents[i])
			if err != nil {
				return link{}, err
			}
			farthest = l
			if i >= len(farthest.Parents) {
				i = len(farthest.Parents) - 1
				continue
			}
		}
		i--
	}
	return farthest, nil
}

func (s *storage) leash(addr netip.Addr) (proto.BlockID, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to get leash: %w", err)
	}
	defer sn.Release()
	return s.getLeash(sn, addr)
}

func (s *storage) updateLeash(addr netip.Addr, id proto.BlockID) error {
	batch := new(leveldb.Batch)
	s.putLeash(batch, addr, id)
	if err := s.db.Write(batch, nil); err != nil {
		return fmt.Errorf("failed to update leash: %w", err)
	}
	return nil
}

func (s *storage) leashes() ([]Leash, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return nil, fmt.Errorf("failed to get leashes: %w", err)
	}
	defer sn.Release()
	r := make([]Leash, 0)
	rng := &util.Range{
		Start: addrKeyBytes(netip.AddrFrom4([...]byte{0x00, 0x00, 0x00, 0x00})),
		Limit: addrKeyBytes(netip.AddrFrom16([...]byte{
			0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		})),
	}
	it := sn.NewIterator(rng, nil)
	defer it.Release()
	for it.First(); it.Valid(); it.Next() {
		addr := addrKeyFromBytes(it.Key())
		id, idErr := proto.NewBlockIDFromBytes(it.Value())
		if idErr != nil {
			return nil, fmt.Errorf("failed to get leashes: %w", idErr)
		}
		r = append(r, Leash{Addr: addr, BlockID: id})
	}
	return r, nil
}

func (s *storage) heads() ([]Head, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return nil, fmt.Errorf("failed to get heads: %w", err)
	}
	defer sn.Release()
	heads, blocks, err := s.getHeads(sn)
	if err != nil {
		return nil, fmt.Errorf("failed to get heads: %w", err)
	}
	r := make([]Head, len(heads))
	for i, bn := range blocks {
		bl, gbErr := s.getBlock(sn, bn)
		if gbErr != nil {
			return nil, fmt.Errorf("failed to get heads: %w", gbErr)
		}
		id, idErr := proto.NewBlockIDFromBytes(bl.ID)
		if idErr != nil {
			return nil, fmt.Errorf("failed to get heads: %w", idErr)
		}
		r[i] = Head{
			ID:      heads[i],
			BlockID: id,
		}
	}
	return r, nil
}

func (s *storage) getCounter(sn *leveldb.Snapshot, counterID byte) (uint64, error) {
	if sn == nil {
		panic("nil snapshot")
	}
	key := [2]byte{counterPrefix, counterID}
	v, err := sn.Get(key[:], nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to get counter %d: %w", counterID, err)
	}
	if l := len(v); l != uint64Size {
		panic(fmt.Sprintf("invalid size %d of blocks counter", l))
	}
	return binary.BigEndian.Uint64(v), nil
}

func (s *storage) putCounter(batch *leveldb.Batch, counterID byte, value uint64) {
	key := [2]byte{counterPrefix, counterID}
	buf := make([]byte, uint64Size)
	binary.BigEndian.PutUint64(buf, value)
	batch.Put(key[:], buf)
}

func (s *storage) getNumOfBlockID(sn *leveldb.Snapshot, id proto.BlockID) (uint64, error) {
	v, err := sn.Get(idKeyBytes(blockPrefix, id), nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get number for ID '%s': %w", id.String(), err)
	}
	if l := len(v); l != uint64Size {
		return 0, fmt.Errorf("invalid size %d of number for ID '%s'", l, id.String())
	}
	return binary.BigEndian.Uint64(v), nil
}

func (s *storage) putID(batch *leveldb.Batch, prefix byte, id proto.BlockID, num uint64) {
	buf := make([]byte, uint64Size)
	binary.BigEndian.PutUint64(buf, num)
	batch.Put(idKeyBytes(prefix, id), buf)
}

func (s *storage) putNewLinkForBlock(
	sn *leveldb.Snapshot, batch *leveldb.Batch, num uint64, id proto.BlockID, h uint32, parentID proto.BlockID,
) error {
	pn, err := s.getNumOfBlockID(sn, parentID)
	if err != nil {
		return err
	}

	// Collect parents for the link starting from the parent block at first position.
	maxParents := int(math.Ceil(math.Log2(float64(h))))
	parents := make([]uint64, 0, maxParents)
	for l := 0; l < maxParents; l++ {
		parents = append(parents, pn)
		pl, glErr := s.getLink(sn, pn)
		if glErr != nil {
			return glErr
		}
		if len(pl.Parents) > l {
			pn = pl.Parents[l]
		}
	}
	l := link{
		BlockID: id.Bytes(),
		Height:  h,
		Parents: parents,
	}
	return s.putLink(batch, num, l)
}

func (s *storage) putBlock(batch *leveldb.Batch, num uint64, b block) error {
	buf, err := cbor.Marshal(b)
	if err != nil {
		return fmt.Errorf("failed to store a block at %d: %w", num, err)
	}
	batch.Put(numKeyBytes(blockPrefix, num), buf)
	return nil
}

func (s *storage) putLink(batch *leveldb.Batch, num uint64, l link) error {
	buf, err := cbor.Marshal(&l)
	if err != nil {
		return fmt.Errorf("failed to store a link at %d: %w", num, err)
	}
	batch.Put(numKeyBytes(chainPrefix, num), buf)
	return nil
}

func (s *storage) getBlockByID(sn *leveldb.Snapshot, id proto.BlockID) (block, error) {
	n, err := s.getNumOfBlockID(sn, id)
	if err != nil {
		return block{}, fmt.Errorf("failed to get block by ID '%s': %w", id.String(), err)
	}
	b, err := s.getBlock(sn, n)
	if err != nil {
		return block{}, fmt.Errorf("failed to get block by ID '%s': %w", id.String(), err)
	}
	return b, nil
}

func (s *storage) getBlock(sn *leveldb.Snapshot, num uint64) (block, error) {
	v, err := sn.Get(numKeyBytes(blockPrefix, num), nil)
	if err != nil {
		return block{}, fmt.Errorf("failed to get block # %d: %w", num, err)
	}
	var b block
	if umErr := cbor.Unmarshal(v, &b); umErr != nil {
		return block{}, fmt.Errorf("failed to get block #  %d: %w", num, umErr)
	}
	return b, nil
}

func (s *storage) getLink(sn *leveldb.Snapshot, num uint64) (link, error) {
	v, err := sn.Get(numKeyBytes(chainPrefix, num), nil)
	if err != nil {
		return link{}, fmt.Errorf("failed to get link by number %d: %w", num, err)
	}
	var l link
	if umErr := cbor.Unmarshal(v, &l); umErr != nil {
		return link{}, fmt.Errorf("failed to unmarshal link by number %d: %w", num, umErr)
	}
	return l, nil
}

func (s *storage) getLinkByID(sn *leveldb.Snapshot, id proto.BlockID) (link, error) {
	n, err := s.getNumOfBlockID(sn, id)
	if err != nil {
		return link{}, fmt.Errorf("failed to get link: %w", err)
	}
	return s.getLink(sn, n)
}

func (s *storage) getLeash(sn *leveldb.Snapshot, addr netip.Addr) (proto.BlockID, error) {
	v, err := sn.Get(addrKeyBytes(addr), nil)
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to get leash for '%s': %w", addr.String(), err)
	}
	if l := len(v); l != digestSize && l != signatureSize {
		return proto.BlockID{}, fmt.Errorf("invalid size %d of leash for '%s'", l, addr.String())
	}
	id, err := proto.NewBlockIDFromBytes(v)
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to get leash for '%s': %w", addr.String(), err)
	}
	return id, nil
}

func (s *storage) putLeash(batch *leveldb.Batch, addr netip.Addr, id proto.BlockID) {
	batch.Put(addrKeyBytes(addr), id.Bytes())
}

func (s *storage) putHead(sn *leveldb.Snapshot, batch *leveldb.Batch, blockNum, parentNum uint64) error {
	// Lookup for parent head.
	headNum, err := s.getHeadByBlockNum(sn, parentNum)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return s.putNewHead(sn, batch, blockNum)
		}
		return fmt.Errorf("failed to put head: %w", err)
	}
	batch.Delete(numKeyBytes(blockToHeadPrefix, parentNum)) // Remove parent's pointer to the head.
	s.putNum(batch, headToBlockPrefix, headNum, blockNum)   // Point head to new block.
	s.putNum(batch, blockToHeadPrefix, blockNum, headNum)   // Point new block to the head.
	return nil
}

func (s *storage) putNewHead(sn *leveldb.Snapshot, batch *leveldb.Batch, blockNum uint64) error {
	// Assign a next number to head.
	headsCnt, err := s.getCounter(sn, headsCounter)
	if err != nil {
		return fmt.Errorf("failed to put new head: %w", err)
	}
	headNum := headsCnt + 1
	s.putCounter(batch, headsCounter, headNum)
	s.putNum(batch, headToBlockPrefix, headNum, blockNum)
	s.putNum(batch, blockToHeadPrefix, blockNum, headNum)
	return nil
}

func (s *storage) putNum(batch *leveldb.Batch, prefix byte, key, value uint64) {
	buf := make([]byte, uint64Size)
	binary.BigEndian.PutUint64(buf, value)
	batch.Put(numKeyBytes(prefix, key), buf)
}

func (s *storage) getHeadByBlockNum(sn *leveldb.Snapshot, blockNum uint64) (uint64, error) {
	v, err := sn.Get(numKeyBytes(blockToHeadPrefix, blockNum), nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get head by block number %d: %w", blockNum, err)
	}
	if l := len(v); l != uint64Size {
		return 0, fmt.Errorf("invalid size %d of head for block number %d", l, blockNum)
	}
	return binary.BigEndian.Uint64(v), nil
}

// getHeads return all heads numbers with corresponding block numbers pointed by heads.
func (s *storage) getHeads(sn *leveldb.Snapshot) ([]uint64, []uint64, error) {
	headNums := make([]uint64, 0)
	blockNums := make([]uint64, 0)
	r := &util.Range{
		Start: numKeyBytes(headToBlockPrefix, 0),
		Limit: numKeyBytes(headToBlockPrefix, math.MaxUint64),
	}
	it := sn.NewIterator(r, nil)
	defer it.Release()
	for it.First(); it.Valid(); it.Next() {
		prefix, num := numKeyFromBytes(it.Key())
		if prefix != headToBlockPrefix {
			return nil, nil, fmt.Errorf("unexpected prefix: %d", prefix)
		}
		headNums = append(headNums, num)
		blockNums = append(blockNums, binary.BigEndian.Uint64(it.Value()))
	}
	return headNums, blockNums, nil
}
