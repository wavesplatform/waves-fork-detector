package blocks

import (
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"path/filepath"

	"github.com/cozodb/cozo-lib-go"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"go.uber.org/zap"
)

var (
	ErrBlockNotFound  = fmt.Errorf("block not found")
	ErrUnleashedPeer  = fmt.Errorf("peer not leashed")
	ErrParentNotFound = fmt.Errorf("parent not found")
)

const (
	engine = "rocksdb"
	dbPath = "blocks"

	decimalBase = 10
)

type Stats struct {
	Blocks int
	Short  int
	Long   int
}

type PeerForkInfo struct {
	Peer    netip.Addr    `json:"peer"`
	Lag     int           `json:"lag"`
	Name    string        `json:"name"`
	Version proto.Version `json:"version"`
}

type Fork struct {
	Longest         bool           `json:"longest"`           // Indicates that the fork is the longest
	HeadBlock       proto.BlockID  `json:"head_block"`        // The last block of the fork
	LastCommonBlock proto.BlockID  `json:"last_common_block"` // The last common block with the longest fork
	Length          int            `json:"length"`            // The number of blocks since the last common block
	Peers           []PeerForkInfo `json:"peers"`             // Peers that seen on the fork
}

type Drawer struct {
	db     cozo.CozoDB
	scheme proto.Scheme
}

func NewDrawer(path string, scheme proto.Scheme, genesis proto.Block) (*Drawer, error) {
	db, err := createDB(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open Drawer DB: %w", err)
	}
	if !hasGenesis(db, genesis.BlockID()) {
		if initErr := initialize(db, genesis, scheme); initErr != nil {
			return nil, fmt.Errorf("failed to initialize Drawer DB: %w", initErr)
		}
	}
	return &Drawer{db: db, scheme: scheme}, nil
}

func (d *Drawer) Close() {
	d.db.Close()
}

func (d *Drawer) PutBlock(block *proto.Block, addr netip.Addr) error {
	ok, err := d.hasBlock(block.BlockID())
	if err != nil {
		return err
	}
	if ok {
		if ulErr := d.updateLeash(addr, block.BlockID()); ulErr != nil {
			return ulErr
		}
		return nil
	}
	hasParent, err := d.hasBlock(block.Parent)
	if err != nil {
		return err
	}
	if !hasParent {
		return ErrParentNotFound
	}
	if insErr := d.insertBlock(block); insErr != nil {
		return insErr
	}
	if ulErr := d.updateLeash(addr, block.BlockID()); ulErr != nil {
		return ulErr
	}
	return nil
}

func (d *Drawer) Stats() Stats {
	return Stats{}
}

func (d *Drawer) Forks(addresses []netip.Addr) ([]Fork, error) {
	return nil, nil
}

func (d *Drawer) Fork(peer netip.Addr) ([]Fork, error) {
	return nil, nil
}

func (d *Drawer) Head(addr netip.Addr) (proto.BlockID, error) {
	args := cozo.Map{
		"node": addr.String(),
	}
	res, err := d.db.Run("?[on] := *leash[$node, on]", args, true)
	if err != nil {
		return proto.BlockID{}, fmt.Errorf("failed to check block existence: %w", err)
	}
	zap.S().Debugf("Head of '%s' result: %v", addr.String(), res)
	if !res.Ok || len(res.Rows) != 1 {
		return proto.BlockID{}, ErrUnleashedPeer
	}
	if v, ok := res.Rows[0][0].(string); ok {
		id, idErr := proto.NewBlockIDFromBase58(v)
		if idErr != nil {
			return proto.BlockID{}, fmt.Errorf("failed to parse block id: %w", idErr)
		}
		return id, nil
	}
	return proto.BlockID{}, errors.New("invalid value type of block ID")
}

func (d *Drawer) IsUnleashed(addr netip.Addr) (bool, error) {
	args := cozo.Map{
		"node": addr.String(),
	}
	res, err := d.db.Run("?[on] := *leash[$node, on]", args, true)
	if err != nil {
		return false, fmt.Errorf("failed to check node '%s' leash: %v", addr.String(), err)
	}
	if res.Ok && len(res.Rows) == 0 {
		return true, nil
	}
	return false, nil
}

func (d *Drawer) insertBlock(block *proto.Block) error {
	ga, err := proto.NewAddressFromPublicKey(d.scheme, block.GeneratorPublicKey)
	if err != nil {
		return fmt.Errorf("failed to insert block '%s': %w", block.BlockID().String(), err)
	}
	s, err := d.blockCumulativeScore(block)
	if err != nil {
		return fmt.Errorf("failed to insert block '%s': %w", block.BlockID().String(), err)
	}
	args := cozo.Map{
		"id":        block.BlockID().String(),
		"generator": ga.String(),
		"score":     s.String(),
		"ts":        block.Timestamp,
		"parent":    block.Parent.String(),
	}
	res, err := d.db.Run(`
		{?[id, generator, score, ts] <- [[$id, $generator, $score, $ts]]; :put blocks {id => generator, score, ts}}
		{?[from, to] <- [[$id, $parent]]; :put chain {from, to}}
	`, args, false)
	if err != nil {
		return fmt.Errorf("failed to insert block: %w", err)
	}
	zap.S().Debugf("Block '%s' insertion result: %v", block.BlockID().String(), res)
	return nil
}

func (d *Drawer) blockCumulativeScore(block *proto.Block) (*big.Int, error) {
	parent, err := d.parentScore(block.Parent)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate score: %w", err)
	}
	s := calculateScore(block.BaseTarget)
	return parent.Add(parent, s), nil
}

func (d *Drawer) parentScore(id proto.BlockID) (*big.Int, error) {
	args := cozo.Map{
		"id": id.String(),
	}
	res, err := d.db.Run("?[score] := *blocks[$id, _, score, _]", args, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent block score: %w", err)
	}
	if !res.Ok || len(res.Rows) != 1 {
		return nil, ErrBlockNotFound
	}
	if v, ok := res.Rows[0][0].(string); ok {
		s, ok := new(big.Int).SetString(v, decimalBase)
		if !ok {
			return nil, errors.New("failed to parse parent block score")
		}
		return s, nil
	}
	return nil, errors.New("invalid value type of parent block score")
}

func (d *Drawer) hasBlock(id proto.BlockID) (bool, error) {
	args := cozo.Map{
		"id": id.String(),
	}
	res, err := d.db.Run("?[generator, score, ts] := *blocks[$id, generator, score, ts]", args, true)
	if err != nil {
		return false, fmt.Errorf("failed to check block existence: %w", err)
	}
	zap.S().Debugf("Has block '%s' result: %v", id.String(), res)
	if res.Ok && len(res.Rows) == 1 {
		return true, nil
	}
	return false, nil
}

func (d *Drawer) updateLeash(addr netip.Addr, id proto.BlockID) error {
	args := cozo.Map{
		"node": addr.String(),
		"on":   id.String(),
	}
	res, err := d.db.Run(`
		?[node, on] <- [[$node, $on]]
		:put leash {node, on}
`, args, false)
	if err != nil {
		return fmt.Errorf("failed to update leash: %w", err)
	}
	zap.S().Debugf("Update leash '%s'->'%s' result: %v", addr.String(), id.String(), res)
	return nil
}

func createDB(path string) (cozo.CozoDB, error) {
	if path == "" { // Special case for testing purpose, empty path creates in-memory database.
		return cozo.New("mem", "", nil)
	}
	return cozo.New(engine, filepath.Clean(filepath.Join(path, dbPath)), nil)
}

func hasGenesis(db cozo.CozoDB, id proto.BlockID) bool {
	args := cozo.Map{
		"id": id.String(),
	}
	res, err := db.Run("?[generator, ts] := *blocks[$id, generator, ts]", args, true)
	if err != nil {
		return false
	}
	return res.Ok && len(res.Rows) == 1
}

func initialize(db cozo.CozoDB, genesis proto.Block, scheme proto.Scheme) error {
	res, err := db.Run("::relations", nil, true)
	if err != nil {
		return fmt.Errorf("failed to check relations existence: %w", err)
	}
	if res.Ok && len(res.Rows) == 3 &&
		res.Rows[0][0] == "blocks" && res.Rows[1][0] == "chain" && res.Rows[2][0] == "leash" {
		return nil
	}
	_, err = db.Run(`
		{:create blocks{id: String => generator: String, score: String, ts: Int}}
		{:create chain{from: String, to: String}}
		{:create leash{node: String => on: String}}
	`, nil, false)
	if err != nil {
		return fmt.Errorf("failed to create relations: %w", err)
	}
	s := calculateScore(genesis.BaseTarget)
	g, err := proto.NewAddressFromPublicKey(scheme, genesis.GeneratorPublicKey)
	if err != nil {
		return fmt.Errorf("failed to insert genesis block: %w", err)
	}
	args := cozo.Map{
		"id":        genesis.BlockID().String(),
		"generator": g.String(),
		"score":     s.String(),
		"ts":        genesis.Timestamp,
	}
	_, err = db.Run(`
		?[id, generator, score, ts] <- [[$id, $generator, $score, $ts]]
		:put blocks {id => generator, score, ts}
	`, args, false)
	if err != nil {
		return fmt.Errorf("failed to insert genesis block: %w", err)
	}
	return nil
}

func calculateScore(baseTarget uint64) *big.Int {
	res := big.NewInt(0)
	if baseTarget == 0 {
		return res
	}
	bt := big.NewInt(int64(baseTarget))
	maxBlockScore, ok := big.NewInt(0).SetString("18446744073709551616", decimalBase)
	if !ok {
		return res
	}
	res.Div(maxBlockScore, bt)
	return res
}
