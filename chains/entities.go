package chains

import (
	"fmt"
	"math/big"
	"net/netip"
	"time"

	"github.com/wavesplatform/gowaves/pkg/proto"
)

type Block struct {
	ID        proto.BlockID
	Parent    proto.BlockID
	Height    uint32
	Generator proto.WavesAddress
	Score     *big.Int
	Timestamp uint64
}

type Head struct {
	ID      uint64
	BlockID proto.BlockID
}

func (h *Head) String() string {
	return fmt.Sprintf("(%d)'%s'", h.ID, h.BlockID.String())
}

type Leash struct {
	Addr    netip.Addr
	BlockID proto.BlockID
}

type Stats struct {
	Short int
	Long  int
}

type Fork struct {
	Longest         bool               `json:"longest"`           // Indicates that the fork is the longest
	HeadBlock       proto.BlockID      `json:"head_block"`        // The last block of the fork
	HeadTimestamp   time.Time          `json:"head_timestamp"`    // The timestamp of the last block of the fork
	HeadGenerator   proto.WavesAddress `json:"head_generator"`    // The generator of the last block of the fork
	HeadHeight      uint32             `json:"head_height"`       // The height of the last block of the fork
	Score           *big.Int           `json:"score"`             // The score of the fork
	Peers           []netip.Addr       `json:"peers"`             // Peers that seen on the fork
	LastCommonBlock proto.BlockID      `json:"last_common_block"` // The last common block with the longest fork
	Length          int                `json:"length"`            // The number of blocks since the last common block
}

type byScoreAndPeersDesc []Fork

func (a byScoreAndPeersDesc) Len() int {
	return len(a)
}

func (a byScoreAndPeersDesc) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byScoreAndPeersDesc) Less(i, j int) bool {
	r := a[i].Score.Cmp(a[j].Score)
	if r == 0 {
		return len(a[i].Peers) > len(a[j].Peers)
	}
	return r > 0
}
