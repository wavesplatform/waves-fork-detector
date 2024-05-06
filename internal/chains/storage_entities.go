package chains

import (
	"encoding/binary"
	"fmt"
	"math/big"
	"net/netip"

	"github.com/wavesplatform/gowaves/pkg/proto"
)

func idKeyBytes(prefix byte, id proto.BlockID) []byte {
	idBytes := id.Bytes()
	buf := make([]byte, prefixSize+len(idBytes))
	buf[0] = prefix
	copy(buf[prefixSize:], idBytes)
	return buf
}

func numKeyBytes(prefix byte, num uint64) []byte {
	buf := make([]byte, prefixSize+uint64Size)
	buf[0] = prefix
	binary.BigEndian.PutUint64(buf[prefixSize:], num)
	return buf
}

func numKeyFromBytes(data []byte) (byte, uint64) {
	if l := len(data); l != prefixSize+uint64Size {
		panic(fmt.Sprintf("invalid size %d of block key", l))
	}
	return data[0], binary.BigEndian.Uint64(data[prefixSize:])
}

func addrKeyBytes(prefix byte, addr netip.Addr) []byte {
	const bitsInByte = 8
	s := addr.BitLen() / bitsInByte
	buf := make([]byte, prefixSize+s)
	buf[0] = prefix
	copy(buf[prefixSize:], addr.AsSlice())
	return buf
}

type block struct {
	Height    uint32   `cbor:"0,keyasint"`
	Timestamp uint64   `cbor:"1,keyasint"`
	Score     *big.Int `cbor:"2,keyasint"`
	ID        []byte   `cbor:"3,keyasint"`
	Parent    []byte   `cbor:"4,keyasint"`
	Generator []byte   `cbor:"5,keyasint"`
}

func newBlock(scheme proto.Scheme, b *proto.Block, height uint32, parentScore *big.Int) (block, error) {
	ga, err := proto.NewAddressFromPublicKey(scheme, b.GeneratorPublicKey)
	if err != nil {
		return block{}, fmt.Errorf("failed to create new block: %w", err)
	}
	return block{
		Height:    height,
		Timestamp: b.Timestamp,
		Score:     calculateCumulativeScore(parentScore, b.BaseTarget),
		ID:        b.BlockID().Bytes(),
		Parent:    b.Parent.Bytes(),
		Generator: ga.Bytes(),
	}, nil
}

type link struct {
	Height  uint32   `cbor:"0,keyasint"`
	BlockID []byte   `cbor:"1,keyasint"`
	Parents []uint64 `cbor:"2,keyasint"`
}
