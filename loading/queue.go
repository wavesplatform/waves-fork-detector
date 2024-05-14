package loading

import (
	"fmt"

	"github.com/wavesplatform/gowaves/pkg/proto"
)

type item struct {
	requested proto.BlockID
	received  *proto.Block
}

type queue struct {
	q []item
}

func newQueue(ids []proto.BlockID) queue {
	q := make([]item, len(ids))
	for i, id := range ids {
		q[i] = item{requested: id}
	}
	return queue{q: q}
}

func (q *queue) put(block *proto.Block) {
	if block == nil {
		return
	}
	for i, p := range q.q {
		if p.requested == block.BlockID() {
			q.q[i].received = block
			return
		}
	}
}

func (q *queue) ready() bool {
	for _, it := range q.q {
		if it.received == nil || (it.received != nil && it.received.BlockID() != it.requested) {
			return false
		}
	}
	return true
}

func (q *queue) rangeString() string {
	l := len(q.q)
	if l == 0 {
		return "[](EMPTY)"
	}
	blocksCnt := 0
	for i := range q.q {
		if q.q[i].received != nil && q.q[i].requested == q.q[i].received.BlockID() {
			blocksCnt++
		}
	}
	sizeStr := fmt.Sprintf("%d/%d", blocksCnt, l)
	if blocksCnt == l {
		sizeStr = fmt.Sprintf("READY/%d", l)
	}
	return fmt.Sprintf("[%s..%s](%s)",
		q.q[0].requested.ShortString(), q.q[len(q.q)-1].requested.ShortString(), sizeStr)
}

// Returns the last received block ID and true if there is such a block.
func (q *queue) lastReceived() (*proto.Block, bool) {
	last := -1
	for i := range q.q {
		if q.q[i].received == nil || q.q[i].received.BlockID() != q.q[i].requested {
			break
		}
		last = i
	}
	if last >= 0 {
		return q.q[last].received, true
	}
	return nil, false
}
