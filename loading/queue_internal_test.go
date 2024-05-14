package loading

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wavesplatform/gowaves/pkg/crypto"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"github.com/wavesplatform/gowaves/pkg/settings"
)

func TestNewQueue(t *testing.T) {
	bl1 := createNthBlock(t, settings.TestNetSettings.Genesis.BlockID(), 1)
	bl2 := createNthBlock(t, bl1.BlockID(), 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)
	ids := []proto.BlockID{bl1.BlockID(), bl2.BlockID(), bl3.BlockID()}
	q := newQueue(ids)
	assert.Equal(t, 3, len(q.q))
	last, ok := q.lastReceived()
	assert.Nil(t, last)
	assert.False(t, ok)
}

func TestQueuePutAndLastReceived(t *testing.T) {
	bl1 := createNthBlock(t, settings.TestNetSettings.Genesis.BlockID(), 1)
	bl2 := createNthBlock(t, bl1.BlockID(), 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)
	fake := createNthBlock(t, bl3.BlockID(), 4)
	ids := []proto.BlockID{bl1.BlockID(), bl2.BlockID(), bl3.BlockID()}
	q := newQueue(ids)
	assert.Equal(t, 3, len(q.q))
	last, ok := q.lastReceived()
	assert.Nil(t, last)
	assert.False(t, ok)
	q.put(bl1)
	last, ok = q.lastReceived()
	assert.Equal(t, bl1, last)
	assert.True(t, ok)

	q.put(bl3)
	last, ok = q.lastReceived()
	assert.Equal(t, bl1, last)
	assert.True(t, ok)

	q.put(fake)
	last, ok = q.lastReceived()
	assert.Equal(t, bl1, last)
	assert.True(t, ok)

	q.put(bl2)
	last, ok = q.lastReceived()
	assert.Equal(t, bl3, last)
	assert.True(t, ok)

	q.put(fake)
	last, ok = q.lastReceived()
	assert.Equal(t, bl3, last)
	assert.True(t, ok)
}

func TestReady(t *testing.T) {
	bl1 := createNthBlock(t, settings.TestNetSettings.Genesis.BlockID(), 1)
	bl2 := createNthBlock(t, bl1.BlockID(), 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)
	fake := createNthBlock(t, bl3.BlockID(), 4)
	ids := []proto.BlockID{bl1.BlockID(), bl2.BlockID(), bl3.BlockID()}
	q := newQueue(ids)
	assert.False(t, q.ready())
	q.put(bl1)
	assert.False(t, q.ready())
	q.put(bl2)
	assert.False(t, q.ready())
	q.put(fake)
	assert.False(t, q.ready())
	q.put(bl3)
	assert.True(t, q.ready())
}

func TestRangeString(t *testing.T) {
	bl1 := createNthBlock(t, settings.TestNetSettings.Genesis.BlockID(), 1)
	bl2 := createNthBlock(t, bl1.BlockID(), 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)
	ids := []proto.BlockID{bl1.BlockID(), bl2.BlockID(), bl3.BlockID()}
	q1 := newQueue(nil)
	assert.Equal(t, "[](EMPTY)", q1.rangeString())
	q2 := newQueue(ids)
	assert.Equal(t, "[kzrDuW…Drbk2T..C9MzRX…zfjRWp](0/3)", q2.rangeString())
	q2.put(bl1)
	assert.Equal(t, "[kzrDuW…Drbk2T..C9MzRX…zfjRWp](1/3)", q2.rangeString())
	q2.put(bl2)
	assert.Equal(t, "[kzrDuW…Drbk2T..C9MzRX…zfjRWp](2/3)", q2.rangeString())
	q2.put(bl3)
	assert.Equal(t, "[kzrDuW…Drbk2T..C9MzRX…zfjRWp](READY/3)", q2.rangeString())
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
