package blocks

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wavesplatform/gowaves/pkg/crypto"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"github.com/wavesplatform/gowaves/pkg/settings"
)

func TestDoubleInitialize(t *testing.T) {
	dr, _ := createTestDrawerAndGenesisID(t)
	err := initialize(dr.db, proto.Block{}, proto.TestNetScheme)
	require.NoError(t, err)
}

func TestPutBlock(t *testing.T) {
	dr, id1 := createTestDrawerAndGenesisID(t)
	bl2 := createNthBlock(t, id1, 2)

	a := netip.MustParseAddr("8.8.8.8")

	err := dr.PutBlock(bl2, a)
	require.NoError(t, err)

	ok, err := dr.hasBlock(bl2.BlockID())
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestLeashUpdate(t *testing.T) {
	dr, id1 := createTestDrawerAndGenesisID(t)
	bl2 := createNthBlock(t, id1, 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)

	a := netip.MustParseAddr("8.8.8.8")
	_, err := dr.Head(a)
	assert.ErrorIs(t, err, ErrUnleashedPeer)

	err = dr.PutBlock(bl2, a)
	require.NoError(t, err)

	ok, err := dr.hasBlock(bl2.BlockID())
	require.NoError(t, err)
	assert.True(t, ok)
	id, err := dr.Head(a)
	require.NoError(t, err)
	assert.Equal(t, bl2.BlockID(), id)

	err = dr.PutBlock(bl3, a)
	require.NoError(t, err)

	ok, err = dr.hasBlock(bl3.BlockID())
	require.NoError(t, err)
	assert.True(t, ok)
	id, err = dr.Head(a)
	require.NoError(t, err)
	assert.Equal(t, bl3.BlockID(), id)
}

func TestMultipleLeashes(t *testing.T) {
	dr, id1 := createTestDrawerAndGenesisID(t)

	bl2 := createNthBlock(t, id1, 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)

	a := netip.MustParseAddr("8.8.8.8")
	b := netip.MustParseAddr("9.9.9.9")

	_, err := dr.Head(a)
	assert.ErrorIs(t, err, ErrUnleashedPeer)
	_, err = dr.Head(b)
	assert.ErrorIs(t, err, ErrUnleashedPeer)

	err = dr.PutBlock(bl2, a)
	require.NoError(t, err)

	id, err := dr.Head(a)
	require.NoError(t, err)
	assert.Equal(t, bl2.BlockID(), id)

	err = dr.PutBlock(bl3, a)
	require.NoError(t, err)

	id, err = dr.Head(a)
	require.NoError(t, err)
	assert.Equal(t, bl3.BlockID(), id)

	err = dr.PutBlock(bl2, b)
	require.NoError(t, err)

	id, err = dr.Head(b)
	require.NoError(t, err)
	assert.Equal(t, bl2.BlockID(), id)

	err = dr.PutBlock(bl3, b)
	require.NoError(t, err)

	id, err = dr.Head(b)
	require.NoError(t, err)
	assert.Equal(t, bl3.BlockID(), id)
}

func TestForkSimple(t *testing.T) {
	dr, id1 := createTestDrawerAndGenesisID(t)

	bl2 := createNthBlock(t, id1, 2)
	bl31 := createNthBlock(t, bl2.BlockID(), 31)
	bl32 := createNthBlock(t, bl2.BlockID(), 32)

	a := netip.MustParseAddr("8.8.8.8")
	b := netip.MustParseAddr("9.9.9.9")

	err := dr.PutBlock(bl2, a)
	require.NoError(t, err)
	err = dr.PutBlock(bl2, b)
	require.NoError(t, err)
	err = dr.PutBlock(bl31, a)
	require.NoError(t, err)
	err = dr.PutBlock(bl32, b)
	require.NoError(t, err)

	id, err := dr.Head(a)
	require.NoError(t, err)
	assert.Equal(t, bl31.BlockID(), id)

	id, err = dr.Head(b)
	require.NoError(t, err)
	assert.Equal(t, bl32.BlockID(), id)

	// forkA, err := dr.Fork(a)
	// require.NoError(t, err)
	// forkB, err := dr.Fork(b)
	// require.NoError(t, err)
}

func createTestDrawerAndGenesisID(t *testing.T) (*Drawer, proto.BlockID) {
	genesis := settings.TestNetSettings.Genesis
	dr, err := NewDrawer("", proto.TestNetScheme, genesis)
	require.NoError(t, err)
	return dr, genesis.BlockID()
}

func testID(t *testing.T, s string) proto.BlockID {
	d, err := crypto.FastHash([]byte(s))
	require.NoError(t, err)
	return proto.NewBlockIDFromDigest(d)
}

func createNthBlock(t *testing.T, prev proto.BlockID, n int) *proto.Block {
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
