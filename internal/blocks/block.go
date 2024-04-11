package blocks

import "github.com/wavesplatform/gowaves/pkg/proto"

type Block struct {
	ID        proto.BlockID
	Height    uint32
	Generator proto.WavesAddress
	Timestamp uint64
}
