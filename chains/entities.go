package chains

import (
	"fmt"
	"math/big"
	"net/netip"

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
