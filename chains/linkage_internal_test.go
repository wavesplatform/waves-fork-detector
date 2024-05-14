package chains

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/wavesplatform/gowaves/pkg/crypto"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"github.com/wavesplatform/gowaves/pkg/settings"
)

func TestPutBlock(t *testing.T) {
	lk, id1 := createTestLinkageAndGenesisID(t)
	bl2 := createNthBlock(t, id1, 2)

	a := netip.MustParseAddr("8.8.8.8")

	err := lk.PutBlock(bl2, a)
	require.NoError(t, err)

	ok, err := lk.hasBlock(bl2.BlockID())
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestLeashUpdate(t *testing.T) {
	lk, id1 := createTestLinkageAndGenesisID(t)
	bl2 := createNthBlock(t, id1, 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)

	a := netip.MustParseAddr("8.8.8.8")
	id, err := lk.Leash(a)
	assert.NoError(t, err)
	assert.Equal(t, id1, id)

	err = lk.PutBlock(bl2, a)
	require.NoError(t, err)

	ok, err := lk.hasBlock(bl2.BlockID())
	require.NoError(t, err)
	assert.True(t, ok)
	id, err = lk.Leash(a)
	require.NoError(t, err)
	assert.Equal(t, bl2.BlockID(), id)

	err = lk.PutBlock(bl3, a)
	require.NoError(t, err)

	ok, err = lk.hasBlock(bl3.BlockID())
	require.NoError(t, err)
	assert.True(t, ok)
	id, err = lk.Leash(a)
	require.NoError(t, err)
	assert.Equal(t, bl3.BlockID(), id)
}

func TestMultipleLeashes(t *testing.T) {
	lk, id1 := createTestLinkageAndGenesisID(t)

	bl2 := createNthBlock(t, id1, 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)

	a := netip.MustParseAddr("8.8.8.8")
	b := netip.MustParseAddr("9.9.9.9")

	id, err := lk.Leash(a)
	assert.NoError(t, err)
	assert.Equal(t, id1, id)
	id, err = lk.Leash(b)
	assert.NoError(t, err)
	assert.Equal(t, id1, id)

	err = lk.PutBlock(bl2, a)
	require.NoError(t, err)

	id, err = lk.Leash(a)
	require.NoError(t, err)
	assert.Equal(t, bl2.BlockID(), id)

	err = lk.PutBlock(bl3, a)
	require.NoError(t, err)

	id, err = lk.Leash(a)
	require.NoError(t, err)
	assert.Equal(t, bl3.BlockID(), id)

	err = lk.PutBlock(bl2, b)
	require.NoError(t, err)

	id, err = lk.Leash(b)
	require.NoError(t, err)
	assert.Equal(t, bl2.BlockID(), id)

	err = lk.PutBlock(bl3, b)
	require.NoError(t, err)

	id, err = lk.Leash(b)
	require.NoError(t, err)
	assert.Equal(t, bl3.BlockID(), id)
}

func TestHeads(t *testing.T) {
	lk, id1 := createTestLinkageAndGenesisID(t)

	bl2 := createNthBlock(t, id1, 2)
	bl31 := createNthBlock(t, bl2.BlockID(), 31)
	bl32 := createNthBlock(t, bl2.BlockID(), 32)
	bl41 := createNthBlock(t, bl31.BlockID(), 41)
	bl42 := createNthBlock(t, bl32.BlockID(), 42)
	bl43 := createNthBlock(t, bl32.BlockID(), 43)

	a := netip.MustParseAddr("8.8.8.8")
	b := netip.MustParseAddr("9.9.9.9")

	h1 := Head{ID: 1, BlockID: bl2.BlockID()}
	err := lk.PutBlock(bl2, a)
	require.NoError(t, err)
	heads, err := lk.Heads()
	require.NoError(t, err)
	assert.Equal(t, 1, len(heads))
	assert.ElementsMatch(t, []Head{h1}, heads)

	h1.BlockID = bl31.BlockID()
	err = lk.PutBlock(bl31, a)
	require.NoError(t, err)
	heads, err = lk.Heads()
	require.NoError(t, err)
	assert.Equal(t, 1, len(heads))
	assert.ElementsMatch(t, []Head{h1}, heads)

	h2 := Head{ID: 2, BlockID: bl32.BlockID()}
	err = lk.PutBlock(bl32, b)
	require.NoError(t, err)
	heads, err = lk.Heads()
	require.NoError(t, err)
	assert.Equal(t, 2, len(heads))
	assert.ElementsMatch(t, []Head{h1, h2}, heads)

	h1.BlockID = bl41.BlockID()
	err = lk.PutBlock(bl41, a)
	require.NoError(t, err)
	heads, err = lk.Heads()
	require.NoError(t, err)
	assert.Equal(t, 2, len(heads))
	assert.ElementsMatch(t, []Head{h1, h2}, heads)

	h2.BlockID = bl42.BlockID()
	err = lk.PutBlock(bl42, b)
	require.NoError(t, err)
	heads, err = lk.Heads()
	require.NoError(t, err)
	assert.Equal(t, 2, len(heads))
	assert.ElementsMatch(t, []Head{h1, h2}, heads)

	h3 := Head{ID: 3, BlockID: bl43.BlockID()}
	err = lk.PutBlock(bl43, b)
	require.NoError(t, err)
	heads, err = lk.Heads()
	require.NoError(t, err)
	assert.Equal(t, 3, len(heads))
	assert.ElementsMatch(t, []Head{h1, h2, h3}, heads)
}

func TestForkSimple(t *testing.T) {
	lk, id1 := createTestLinkageAndGenesisID(t)

	bl2 := createNthBlock(t, id1, 2)
	bl31 := createNthBlock(t, bl2.BlockID(), 31)
	bl32 := createNthBlock(t, bl2.BlockID(), 32)

	a := netip.MustParseAddr("8.8.8.8")
	b := netip.MustParseAddr("9.9.9.9")

	err := lk.PutBlock(bl2, a)
	require.NoError(t, err)
	err = lk.PutBlock(bl2, b)
	require.NoError(t, err)
	err = lk.PutBlock(bl31, a)
	require.NoError(t, err)
	err = lk.PutBlock(bl32, b)
	require.NoError(t, err)

	id, err := lk.Leash(a)
	require.NoError(t, err)
	assert.Equal(t, bl31.BlockID(), id)

	id, err = lk.Leash(b)
	require.NoError(t, err)
	assert.Equal(t, bl32.BlockID(), id)

	// forkA, err := lk.Fork(a)
	// require.NoError(t, err)
	// forkB, err := lk.Fork(b)
	// require.NoError(t, err)
}

func TestLastBlockIDs(t *testing.T) {
	lk, id1 := createTestLinkageAndGenesisID(t)

	bl2 := createNthBlock(t, id1, 2)
	bl31 := createNthBlock(t, bl2.BlockID(), 31)
	bl32 := createNthBlock(t, bl2.BlockID(), 32)
	bl41 := createNthBlock(t, bl31.BlockID(), 41)
	bl42 := createNthBlock(t, bl32.BlockID(), 42)
	bl43 := createNthBlock(t, bl32.BlockID(), 43)

	a := netip.MustParseAddr("8.8.8.8")
	b := netip.MustParseAddr("9.9.9.9")

	err := lk.PutBlock(bl2, a)
	require.NoError(t, err)

	err = lk.PutBlock(bl31, a)
	require.NoError(t, err)

	err = lk.PutBlock(bl32, b)
	require.NoError(t, err)

	err = lk.PutBlock(bl41, a)
	require.NoError(t, err)

	err = lk.PutBlock(bl42, b)
	require.NoError(t, err)

	err = lk.PutBlock(bl43, b)
	require.NoError(t, err)

	h1 := Head{ID: 1, BlockID: bl41.BlockID()}
	h2 := Head{ID: 2, BlockID: bl42.BlockID()}
	h3 := Head{ID: 3, BlockID: bl43.BlockID()}
	heads, err := lk.Heads()
	require.NoError(t, err)
	assert.Equal(t, 3, len(heads))
	assert.ElementsMatch(t, []Head{h1, h2, h3}, heads)

	ids, err := lk.LastIDs(bl41.BlockID(), 100)
	require.NoError(t, err)
	assert.Equal(t, 4, len(ids))
	assert.ElementsMatch(t, []proto.BlockID{bl41.BlockID(), bl31.BlockID(), bl2.BlockID(), id1}, ids)

	ids, err = lk.LastIDs(bl42.BlockID(), 100)
	require.NoError(t, err)
	assert.Equal(t, 4, len(ids))
	assert.ElementsMatch(t, []proto.BlockID{bl42.BlockID(), bl32.BlockID(), bl2.BlockID(), id1}, ids)

	ids, err = lk.LastIDs(bl43.BlockID(), 100)
	require.NoError(t, err)
	assert.Equal(t, 4, len(ids))
	assert.ElementsMatch(t, []proto.BlockID{bl43.BlockID(), bl32.BlockID(), bl2.BlockID(), id1}, ids)

	fakeBlock := createNthBlock(t, bl42.BlockID(), 52)
	ids, err = lk.LastIDs(fakeBlock.BlockID(), 100)
	require.ErrorIs(t, err, leveldb.ErrNotFound)
	assert.Equal(t, 0, len(ids))
}

func createTestLinkageAndGenesisID(t testing.TB) (*Linkage, proto.BlockID) {
	genesis := settings.TestNetSettings.Genesis
	dr, err := NewLinkage(t.TempDir(), proto.TestNetScheme, genesis)
	require.NoError(t, err)
	return dr, genesis.BlockID()
}

func testID(t testing.TB, s string) proto.BlockID {
	d, err := crypto.FastHash([]byte(s))
	require.NoError(t, err)
	return proto.NewBlockIDFromDigest(d)
}

func createNthBlock(t testing.TB, prev proto.BlockID, n int) *proto.Block {
	const tsStep uint64 = 10000
	id := testID(t, fmt.Sprintf("BLOCK%d", n))
	return &proto.Block{
		BlockHeader: proto.BlockHeader{
			Version:            proto.ProtobufBlockVersion,
			Timestamp:          tsStep * uint64(n),
			Parent:             prev,
			NxtConsensus:       proto.NxtConsensus{BaseTarget: 12345},
			GeneratorPublicKey: crypto.PublicKey{},
			ID:                 id,
		},
	}
}
