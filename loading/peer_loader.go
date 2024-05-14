package loading

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/qmuntal/stateless"
	"github.com/wavesplatform/gowaves/pkg/proto"
	"go.uber.org/zap"

	"github.com/alexeykiselev/waves-fork-detector/chains"
	"github.com/alexeykiselev/waves-fork-detector/peers"
)

const (
	idsBatchSize    = 100
	timeoutDuration = 30 * time.Second
)

type stage int

const (
	stageIdle stage = iota
	stageWaitingForIDs
	stageWaitingForBlocks
	stageDone
)

type event int

const (
	eventStart event = iota
	eventIDs
	eventBlock
	eventTick
	eventTimeout
	eventQueueReady
)

type peerLoader struct {
	sm *stateless.StateMachine

	peer peers.HistoryRequester
	hp   chains.HistoryProvider
	r    Reporter

	timestamp time.Time // Time then operation was started. Used to calculate timeout.
	queue     queue
}

func newPeerLoader(peer peers.HistoryRequester, hp chains.HistoryProvider, r Reporter) *peerLoader {
	pl := &peerLoader{
		sm:   stateless.NewStateMachine(stageIdle),
		peer: peer,
		hp:   hp,
		r:    r,
	}
	pl.sm.SetTriggerParameters(eventIDs, reflect.TypeOf([]proto.BlockID{}))
	pl.sm.SetTriggerParameters(eventBlock, reflect.TypeOf((*proto.Block)(nil)))
	pl.sm.SetTriggerParameters(eventTick, reflect.TypeOf(time.Time{}))

	pl.configureIdleState()
	pl.configureWaitingForIDs()
	pl.configureWaitingForBlocksState()
	pl.configureDoneState()

	return pl
}

func (pl *peerLoader) configureIdleState() {
	pl.sm.Configure(stageIdle).
		OnEntryFrom(eventQueueReady, pl.reportOK).
		Permit(eventStart, stageWaitingForIDs).
		Ignore(eventIDs).
		Ignore(eventBlock).
		Ignore(eventTick).
		Ignore(eventTimeout).
		Ignore(eventQueueReady)
}

func (pl *peerLoader) configureWaitingForIDs() {
	pl.sm.Configure(stageWaitingForIDs).
		OnEntryFrom(eventStart, pl.requestIDs).
		Permit(eventIDs, stageWaitingForBlocks).
		InternalTransition(eventTick, pl.onTick).
		Permit(eventTimeout, stageDone).
		Ignore(eventStart).
		Ignore(eventBlock).
		Ignore(eventQueueReady)
}

func (pl *peerLoader) configureWaitingForBlocksState() {
	pl.sm.Configure(stageWaitingForBlocks).
		OnEntryFrom(eventIDs, pl.requestBlocks).
		InternalTransition(eventBlock, pl.appendBlock).
		InternalTransition(eventTick, pl.onTick).
		Permit(eventTimeout, stageDone).
		Permit(eventQueueReady, stageIdle).
		Ignore(eventStart).
		Ignore(eventIDs)
}

func (pl *peerLoader) configureDoneState() {
	pl.sm.Configure(stageDone).
		OnEntry(pl.handleFailure).
		Ignore(eventStart).
		Ignore(eventIDs).
		Ignore(eventBlock).
		Ignore(eventTick).
		Ignore(eventTimeout)
}

func (pl *peerLoader) start() error {
	err := pl.sm.Fire(eventStart)
	if err != nil {
		return fmt.Errorf("failed to start peer loader '%s': %w", pl.peer.ID(), err)
	}
	return nil
}

func (pl *peerLoader) processIDs(ids []proto.BlockID) error {
	err := pl.sm.Fire(eventIDs, ids)
	if err != nil {
		return fmt.Errorf("failed to process IDs for peer '%s': %w", pl.peer.ID(), err)
	}
	return nil
}

func (pl *peerLoader) processBlock(block *proto.Block) error {
	err := pl.sm.Fire(eventBlock, block)
	if err != nil {
		return fmt.Errorf("failed to process block for peer '%s': %w", pl.peer.ID(), err)
	}
	return nil
}

func (pl *peerLoader) processTick(tm time.Time) error {
	err := pl.sm.Fire(eventTick, tm)
	if err != nil {
		return fmt.Errorf("failed to process tick for peer '%s': %w", pl.peer.ID(), err)
	}
	return nil
}

func (pl *peerLoader) onTick(_ context.Context, args ...any) error {
	tm, ok := args[0].(time.Time)
	if !ok {
		return fmt.Errorf("failed to process tick for peer '%s': invalid argument type", pl.peer.ID())
	}
	if d := tm.Sub(pl.timestamp); d > timeoutDuration {
		zap.S().Warnf("[PL@%s] Timeout (%s) in state %s", pl.peer.ID(), d, pl.sm.MustState())
		return pl.sm.Fire(eventTimeout)
	}
	return nil
}

func (pl *peerLoader) requestIDs(_ context.Context, _ ...any) error {
	leash, err := pl.hp.Leash(pl.peer.ID())
	if err != nil {
		return fmt.Errorf("failed to request IDs from '%s': %w", pl.peer.ID(), err)
	}
	lastIDs, err := pl.hp.LastIDs(leash, idsBatchSize)
	if err != nil {
		return fmt.Errorf("failed to request IDs from '%s': %w", pl.peer.ID(), err)
	}
	pl.peer.RequestBlockIDs(lastIDs)
	pl.timestamp = time.Now()
	return nil
}

func (pl *peerLoader) requestBlocks(_ context.Context, args ...any) error {
	ids, ok := args[0].([]proto.BlockID)
	if !ok {
		return fmt.Errorf("failed to request blocks from '%s': invalid argument type", pl.peer.ID())
	}
	req := make([]proto.BlockID, 0, len(ids))
	for i, id := range ids {
		hasBlock, err := pl.hp.HasBlock(id) // Check if we have block with this ID.
		if err != nil {
			return fmt.Errorf("failed to request blocks from '%s': %w", pl.peer.ID(), err)
		}
		if hasBlock {
			leash, lshErr := pl.hp.Leash(pl.peer.ID())
			if lshErr != nil {
				return fmt.Errorf("failed to request blocks form '%s': %w", pl.peer.ID(), lshErr)
			}
			if id != leash {
				// Move leash to the peer.
				if mvErr := pl.hp.MoveLeash(id, pl.peer.ID()); mvErr != nil {
					return fmt.Errorf("failed to request blocks form '%s': %w", pl.peer.ID(), mvErr)
				}
			}
			continue
		}
		req = append(req, ids[i:]...) // We reached first block we don't have, add the tail to request.
		break
	}
	if len(req) == 0 { // Nothing to request, all blocks are in storage.
		return pl.sm.Fire(eventQueueReady) // Fire queue ready to move to Idle state.
	}

	pl.queue = newQueue(req)
	zap.S().Infof("[PL@%s] Requesting blocks for %s", pl.peer.ID(), pl.queue.rangeString())
	for _, id := range req {
		pl.peer.RequestBlock(id)
	}
	pl.timestamp = time.Now()
	return nil
}

func (pl *peerLoader) appendBlock(_ context.Context, args ...any) error {
	block, ok := args[0].(*proto.Block)
	if !ok {
		return fmt.Errorf("failed to append block for peer '%s': invalid argument type", pl.peer.ID())
	}
	pl.queue.put(block)
	if pl.queue.ready() {
		for _, it := range pl.queue.q { // Applying blocks without checks, they were done in ready function.
			if err := pl.hp.PutBlock(it.received, pl.peer.ID()); err != nil {
				return fmt.Errorf("failed to append block '%s' for peer '%s': %w",
					it.received.BlockID(), pl.peer.ID(), err)
			}
		}
		zap.S().Debugf("Peer '%s' loaded blocks %s", pl.peer.ID(), pl.queue.rangeString())
		return pl.sm.Fire(eventQueueReady)
	}
	return nil
}

func (pl *peerLoader) handleFailure(_ context.Context, _ ...any) error {
	// Partially apply the queue. Check queue for received blocks and move leash to the last received block.
	for _, it := range pl.queue.q {
		if it.received != nil && it.received.BlockID() == it.requested {
			if err := pl.hp.PutBlock(it.received, pl.peer.ID()); err != nil {
				return err
			}
		} else {
			break // Stop on first not received block.
		}
	}
	pl.r.Fail()
	return nil
}

func (pl *peerLoader) reportOK(_ context.Context, _ ...any) error {
	pl.r.OK()
	return nil
}
