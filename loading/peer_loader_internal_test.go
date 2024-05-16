package loading

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"github.com/wavesplatform/gowaves/pkg/settings"

	mockchains "github.com/alexeykiselev/waves-fork-detector/mocks/github.com/alexeykiselev/waves-fork-detector/chains"
	mockloading "github.com/alexeykiselev/waves-fork-detector/mocks/github.com/alexeykiselev/waves-fork-detector/loading"
	mockpeers "github.com/alexeykiselev/waves-fork-detector/mocks/github.com/alexeykiselev/waves-fork-detector/peers"
)

func TestPeerLoaderSimpleOneIDsBatch(t *testing.T) {
	addr := netip.MustParseAddr("8.8.8.8")

	bl1 := createNthBlock(t, settings.TestNetSettings.Genesis.BlockID(), 1)
	bl2 := createNthBlock(t, bl1.BlockID(), 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)

	mhr := mockpeers.NewMockHistoryRequester(t)
	mhr.On("ID").Return(addr)
	mhr.On("RequestBlockIDs", []proto.BlockID{bl1.BlockID()})
	mhr.On("RequestBlock", bl2.BlockID())
	mhr.On("RequestBlock", bl3.BlockID())

	mhp := mockchains.NewMockHistoryProvider(t)
	mhp.On("Leash", addr).Return(bl1.BlockID(), nil)
	mhp.On("LastIDs", bl1.BlockID(), 100).Return([]proto.BlockID{bl1.BlockID()}, nil)
	mhp.On("HasBlock", bl1.BlockID()).Return(true, nil)
	mhp.On("Leash", addr).Return(bl1.BlockID(), nil).NotBefore(mhp.On("HasBlock", bl1.BlockID()))
	mhp.On("HasBlock", bl2.BlockID()).Return(false, nil)
	mhp.On("PutBlock", bl2, addr).Return(nil).Once()
	mhp.On("PutBlock", bl3, addr).Return(nil).Once()

	mr := mockloading.NewMockReporter(t)
	mr.On("OK").Once()

	pl := newPeerLoader(mhr, mhp, mr)
	assert.Equal(t, stateIdle, pl.sm.MustState())
	err := pl.start()
	require.NoError(t, err)
	assert.Equal(t, stateWaitingForIDs, pl.sm.MustState())
	err = pl.processIDs([]proto.BlockID{bl1.BlockID(), bl2.BlockID(), bl3.BlockID()})
	require.NoError(t, err)
	assert.Equal(t, stateWaitingForBlocks, pl.sm.MustState())
	err = pl.processBlock(bl2)
	require.NoError(t, err)
	assert.Equal(t, stateWaitingForBlocks, pl.sm.MustState())
	err = pl.processBlock(bl3)
	require.NoError(t, err)
	assert.Equal(t, stateIdle, pl.sm.MustState())

	mhr.AssertExpectations(t)
	mhp.AssertExpectations(t)
}

func TestTimeoutOnWaitingForIDs(t *testing.T) {
	addr := netip.MustParseAddr("8.8.8.8")
	start := time.Now()

	bl1 := proto.MustBlockIDFromBase58("4Ax3PbKd39x4XsfLRmYzwr8mGBL2u2gMd8RJYUs8Zhfh")

	mhr := mockpeers.NewMockHistoryRequester(t)
	mhr.On("ID").Return(addr)
	mhr.On("RequestBlockIDs", []proto.BlockID{bl1})

	mhp := mockchains.NewMockHistoryProvider(t)
	mhp.On("Leash", addr).Return(bl1, nil)
	mhp.On("LastIDs", bl1, 100).Return([]proto.BlockID{bl1}, nil)

	mr := mockloading.NewMockReporter(t)
	mr.On("Fail").Once()

	pl := newPeerLoader(mhr, mhp, mr)
	assert.Equal(t, stateIdle, pl.sm.MustState())
	err := pl.start()
	require.NoError(t, err)
	assert.Equal(t, stateWaitingForIDs, pl.sm.MustState())
	err = pl.processTick(start.Add(timeoutDuration + time.Second))
	assert.NoError(t, err)
	assert.Equal(t, stateDone, pl.sm.MustState())

	mhr.AssertExpectations(t)
	mhp.AssertExpectations(t)
}

func TestTimeoutOnWaitingForBlocks(t *testing.T) {
	addr := netip.MustParseAddr("8.8.8.8")
	start := time.Now()

	bl1 := createNthBlock(t, settings.TestNetSettings.Genesis.BlockID(), 1)
	bl2 := createNthBlock(t, bl1.BlockID(), 2)
	bl3 := createNthBlock(t, bl2.BlockID(), 3)

	mhr := mockpeers.NewMockHistoryRequester(t)
	mhr.On("ID").Return(addr)
	mhr.On("RequestBlockIDs", []proto.BlockID{bl1.BlockID()})
	mhr.On("RequestBlock", bl2.BlockID())
	mhr.On("RequestBlock", bl3.BlockID())

	mhp := mockchains.NewMockHistoryProvider(t)
	mhp.On("Leash", addr).Return(bl1.BlockID(), nil)
	mhp.On("LastIDs", bl1.BlockID(), 100).Return([]proto.BlockID{bl1.BlockID()}, nil)
	mhp.On("HasBlock", bl1.BlockID()).Return(true, nil)
	mhp.On("Leash", addr).Return(bl1.BlockID(), nil).NotBefore(mhp.On("HasBlock", bl1.BlockID()))
	mhp.On("HasBlock", bl2.BlockID()).Return(false, nil)
	// Because of timeout we are expecting partial application of blocks batch.
	mhp.On("PutBlock", bl2, addr).Return(nil).Once()

	mr := mockloading.NewMockReporter(t)
	mr.On("Fail").Once()

	pl := newPeerLoader(mhr, mhp, mr)
	assert.Equal(t, stateIdle, pl.sm.MustState())
	err := pl.start()
	require.NoError(t, err)
	assert.Equal(t, stateWaitingForIDs, pl.sm.MustState())
	err = pl.processIDs([]proto.BlockID{bl1.BlockID(), bl2.BlockID(), bl3.BlockID()})
	require.NoError(t, err)
	assert.Equal(t, stateWaitingForBlocks, pl.sm.MustState())
	err = pl.processBlock(bl2)
	require.NoError(t, err)
	assert.Equal(t, stateWaitingForBlocks, pl.sm.MustState())
	err = pl.processTick(start.Add(timeoutDuration + time.Second))
	require.NoError(t, err)
	assert.Equal(t, stateDone, pl.sm.MustState())

	mhr.AssertExpectations(t)
	mhp.AssertExpectations(t)
}
