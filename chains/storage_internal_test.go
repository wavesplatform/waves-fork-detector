package chains

import (
	rand2 "math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"github.com/wavesplatform/gowaves/pkg/settings"
)

func TestDoubleInitialization(t *testing.T) {
	st, _ := createTestStorage(t)
	genesis := settings.TestNetSettings.Genesis
	err := st.initialize(genesis)
	require.NoError(t, err)
	bl, err := st.block(genesis.BlockID())
	require.NoError(t, err)
	assert.Equal(t, genesis.BlockID().Bytes(), bl.ID)
	assert.Equal(t, 1, int(bl.Height))
	assert.Equal(t, genesis.Timestamp, bl.Timestamp)

	hs, err := st.heads()
	require.NoError(t, err)
	assert.Equal(t, 1, len(hs))
}

func TestSingleChain10(t *testing.T) {
	st, genesisID := createTestStorage(t)

	id := genesisID
	for i := 0; i < 10; i++ {
		bl := createNthBlock(t, id, i+2)
		err := st.putProtoBlock(bl)
		require.NoError(t, err)
		id = bl.BlockID()
	}

	err := st.close()
	require.NoError(t, err)
}

func BenchmarkPut5MBlocks(b *testing.B) {
	st, genesisID := createTestStorage(b)

	id := genesisID
	for i := 0; i < 5_000_000; i++ {
		bl := createNthBlock(b, id, i+2)
		err := st.putProtoBlock(bl)
		require.NoError(b, err)
		id = bl.BlockID()
	}
	b.ResetTimer()
	b.Run("PutAfter5M", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			bl := createNthBlock(b, id, i+5_000_003)
			err := st.putProtoBlock(bl)
			require.NoError(b, err)
			id = bl.BlockID()
		}
		b.ReportAllocs()
	})
	b.StopTimer()
	err := st.close()
	require.NoError(b, err)
}

func TestGetAncestors(t *testing.T) {
	st, genesisID := createTestStorage(t)

	bl2 := createNthBlock(t, genesisID, 2)
	bl31 := createNthBlock(t, bl2.BlockID(), 31)
	bl32 := createNthBlock(t, bl2.BlockID(), 32)
	bl41 := createNthBlock(t, bl31.BlockID(), 41)
	bl42 := createNthBlock(t, bl32.BlockID(), 42)
	bl43 := createNthBlock(t, bl32.BlockID(), 43)

	err := st.putProtoBlock(bl2)
	require.NoError(t, err)

	err = st.putProtoBlock(bl31)
	require.NoError(t, err)

	err = st.putProtoBlock(bl32)
	require.NoError(t, err)

	err = st.putProtoBlock(bl41)
	require.NoError(t, err)

	err = st.putProtoBlock(bl42)
	require.NoError(t, err)

	err = st.putProtoBlock(bl43)
	require.NoError(t, err)

	ids, err := st.getAncestors(bl41.BlockID(), 100)
	require.NoError(t, err)
	assert.Equal(t, 4, len(ids))
	assert.ElementsMatch(t, []proto.BlockID{bl41.BlockID(), bl31.BlockID(), bl2.BlockID(), genesisID}, ids)

	ids, err = st.getAncestors(bl42.BlockID(), 100)
	require.NoError(t, err)
	assert.Equal(t, 4, len(ids))
	assert.ElementsMatch(t, []proto.BlockID{bl42.BlockID(), bl32.BlockID(), bl2.BlockID(), genesisID}, ids)

	ids, err = st.getAncestors(bl43.BlockID(), 100)
	require.NoError(t, err)
	assert.Equal(t, 4, len(ids))
	assert.ElementsMatch(t, []proto.BlockID{bl43.BlockID(), bl32.BlockID(), bl2.BlockID(), genesisID}, ids)

	fakeBlock := createNthBlock(t, bl42.BlockID(), 52)
	ids, err = st.getAncestors(fakeBlock.BlockID(), 100)
	require.ErrorIs(t, err, leveldb.ErrNotFound)
	require.Empty(t, ids)
}

func TestAncestorsOnLongChain(t *testing.T) {
	st, id := createTestStorage(t)

	for i := 0; i < 2000; i++ {
		bl := createNthBlock(t, id, i+2)
		err := st.putProtoBlock(bl)
		require.NoError(t, err)
		id = bl.BlockID()
	}

	ids, err := st.getAncestors(id, 100)
	require.NoError(t, err)
	assert.Equal(t, 100, len(ids))
}

func BenchmarkGet100AncestorsAfter1MBlocks(b *testing.B) {
	st, genesisID := createTestStorage(b)

	id := genesisID
	for i := 0; i < 1_000_000; i++ {
		bl := createNthBlock(b, id, i+2)
		err := st.putProtoBlock(bl)
		require.NoError(b, err)
		id = bl.BlockID()
	}
	b.ResetTimer()
	b.Run("Get100Ancestors", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ids, err := st.getAncestors(id, 100)
			require.NoError(b, err)
			assert.Equal(b, 100, len(ids))
			id = ids[99]
		}
		b.ReportAllocs()
	})
	b.StopTimer()
	err := st.close()
	require.NoError(b, err)
}

func TestLCA(t *testing.T) {
	st, gid := createTestStorage(t)
	bl2 := createNthBlock(t, gid, 2)
	bl3 := createNthBlock(t, gid, 3)
	bl4 := createNthBlock(t, bl3.BlockID(), 4)
	err := st.putProtoBlock(bl2)
	require.NoError(t, err)
	err = st.putProtoBlock(bl3)
	require.NoError(t, err)
	err = st.putProtoBlock(bl4)
	require.NoError(t, err)

	a, err := st.lca(bl2.BlockID(), bl3.BlockID())
	require.NoError(t, err)
	assert.Equal(t, gid, a)

	a, err = st.lca(bl2.BlockID(), bl4.BlockID())
	require.NoError(t, err)
	assert.Equal(t, gid, a)

	a, err = st.lca(bl4.BlockID(), bl2.BlockID())
	require.NoError(t, err)
	assert.Equal(t, gid, a)

	fakeBlock := createNthBlock(t, bl2.BlockID(), 5)
	_, err = st.lca(fakeBlock.BlockID(), bl4.BlockID())
	assert.ErrorIs(t, err, leveldb.ErrNotFound)
}

func TestCommonAncestorShortForks(t *testing.T) {
	st, gid := createTestStorage(t)
	bl2 := createNthBlock(t, gid, 2)
	bl3 := createNthBlock(t, gid, 3)
	bl4 := createNthBlock(t, gid, 4)
	bl5 := createNthBlock(t, bl2.BlockID(), 5)
	bl6 := createNthBlock(t, bl3.BlockID(), 6)
	bl7 := createNthBlock(t, bl3.BlockID(), 7)
	bl8 := createNthBlock(t, bl3.BlockID(), 8)
	bl9 := createNthBlock(t, bl4.BlockID(), 9)
	err := st.putProtoBlock(bl2)
	require.NoError(t, err)
	err = st.putProtoBlock(bl3)
	require.NoError(t, err)
	err = st.putProtoBlock(bl4)
	require.NoError(t, err)
	err = st.putProtoBlock(bl5)
	require.NoError(t, err)
	err = st.putProtoBlock(bl6)
	require.NoError(t, err)
	err = st.putProtoBlock(bl7)
	require.NoError(t, err)
	err = st.putProtoBlock(bl8)
	require.NoError(t, err)
	err = st.putProtoBlock(bl9)
	require.NoError(t, err)

	for _, test := range []struct {
		l1, l2 proto.BlockID
		a      proto.BlockID
	}{
		{bl6.BlockID(), bl9.BlockID(), gid},
		{bl9.BlockID(), bl5.BlockID(), gid},
		{bl6.BlockID(), bl8.BlockID(), bl3.BlockID()},
		{bl6.BlockID(), gid, gid},
		{bl6.BlockID(), bl4.BlockID(), gid},
		{bl7.BlockID(), bl3.BlockID(), bl3.BlockID()},
		{bl3.BlockID(), bl3.BlockID(), bl3.BlockID()},
	} {
		a, lcaErr := st.lca(test.l1, test.l2)
		require.NoError(t, lcaErr)
		assert.Equal(t, test.a, a)
	}
}

func TestCommonAncestorLongerForks(t *testing.T) {
	/*
		1 <- 2 <- 3 <- 4 <- 51 <- 61 <- 71
		               |           | <- 72
		               | <- 52 <- 62 <- 73 <- 83
	*/
	st, gid := createTestStorage(t)
	bl2 := createNthBlock(t, gid, 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)
	bl4 := createNthBlock(t, bl3.BlockID(), 4)

	bl51 := createNthBlock(t, bl4.BlockID(), 51)
	bl52 := createNthBlock(t, bl4.BlockID(), 52)

	bl61 := createNthBlock(t, bl51.BlockID(), 61)
	bl62 := createNthBlock(t, bl52.BlockID(), 62)

	bl71 := createNthBlock(t, bl61.BlockID(), 71)
	bl72 := createNthBlock(t, bl61.BlockID(), 72)

	bl73 := createNthBlock(t, bl62.BlockID(), 73)
	bl83 := createNthBlock(t, bl73.BlockID(), 83)

	err := st.putProtoBlock(bl2)
	require.NoError(t, err)
	err = st.putProtoBlock(bl3)
	require.NoError(t, err)
	err = st.putProtoBlock(bl4)
	require.NoError(t, err)
	err = st.putProtoBlock(bl51)
	require.NoError(t, err)
	err = st.putProtoBlock(bl52)
	require.NoError(t, err)
	err = st.putProtoBlock(bl61)
	require.NoError(t, err)
	err = st.putProtoBlock(bl62)
	require.NoError(t, err)
	err = st.putProtoBlock(bl71)
	require.NoError(t, err)
	err = st.putProtoBlock(bl72)
	require.NoError(t, err)
	err = st.putProtoBlock(bl73)
	require.NoError(t, err)
	err = st.putProtoBlock(bl83)
	require.NoError(t, err)

	for _, test := range []struct {
		l1, l2 proto.BlockID
		a      proto.BlockID
	}{
		{bl4.BlockID(), bl3.BlockID(), bl3.BlockID()},
		{bl61.BlockID(), bl52.BlockID(), bl4.BlockID()},
		{bl71.BlockID(), bl62.BlockID(), bl4.BlockID()},
		{bl71.BlockID(), bl72.BlockID(), bl61.BlockID()},
		{bl71.BlockID(), bl73.BlockID(), bl4.BlockID()},
		{bl83.BlockID(), bl73.BlockID(), bl73.BlockID()},
		{bl83.BlockID(), bl72.BlockID(), bl4.BlockID()},
		{bl83.BlockID(), bl71.BlockID(), bl4.BlockID()},
		{bl83.BlockID(), bl51.BlockID(), bl4.BlockID()},
	} {
		a, lcaErr := st.lca(test.l1, test.l2)
		require.NoError(t, lcaErr)
		assert.Equal(t, test.a, a)
	}
}

func BenchmarkLCAOnTwo1MBlockForks(b *testing.B) {
	st, id := createTestStorage(b)
	for i := 0; i < 1_000_000; i++ {
		bl := createNthBlock(b, id, i+2)
		err := st.putProtoBlock(bl)
		require.NoError(b, err)
		id = bl.BlockID()
	}
	ca := id
	f1 := make([]proto.BlockID, 0, 10_000)
	for i := 0; i < 1_000_000; i++ {
		bl := createNthBlock(b, id, i+1_000_002)
		err := st.putProtoBlock(bl)
		require.NoError(b, err)
		id = bl.BlockID()
		if i%100 == 0 {
			f1 = append(f1, id)
		}
	}
	id = ca
	f2 := make([]proto.BlockID, 0, 10_000)
	for i := 0; i < 1_000_000; i++ {
		bl := createNthBlock(b, id, i+2_000_002)
		err := st.putProtoBlock(bl)
		require.NoError(b, err)
		id = bl.BlockID()
		if i%100 == 0 {
			f2 = append(f2, id)
		}
	}
	b.ResetTimer()
	b.Run("LCA", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			l1 := f1[rand2.IntN(len(f1))]
			l2 := f2[rand2.IntN(len(f2))]

			a, err := st.lca(l1, l2)
			require.NoError(b, err)
			assert.Equal(b, ca, a)
		}
		b.ReportAllocs()
	})
	b.StopTimer()
	err := st.close()
	require.NoError(b, err)
}

func createTestStorage(t testing.TB) (*storage, proto.BlockID) {
	st, err := newStorage(t.TempDir(), proto.TestNetScheme)
	require.NoError(t, err)
	genesis := settings.TestNetSettings.Genesis
	err = st.initialize(genesis)
	require.NoError(t, err)
	return st, genesis.BlockID()
}
