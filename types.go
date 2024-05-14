package main

import (
	"net"
	"net/netip"

	"github.com/wavesplatform/gowaves/pkg/proto"
)

type PeerForkInfo struct {
	Peer    netip.Addr    `json:"peer"`
	Lag     int           `json:"lag"`
	Name    string        `json:"name"`
	Version proto.Version `json:"version"`
}

type Fork struct {
	Longest          bool           `json:"longest"`            // Indicates that the fork is the longest
	Height           int            `json:"height"`             // The height of the last block in the fork
	HeadBlock        proto.BlockID  `json:"head_block"`         // The last block of the fork
	LastCommonHeight int            `json:"last_common_height"` // The height of the last common block
	LastCommonBlock  proto.BlockID  `json:"last_common_block"`  // The last common block with the longest fork
	Length           int            `json:"length"`             // The number of blocks since the last common block
	Peers            []PeerForkInfo `json:"peers"`              // Peers that seen on the fork
}

type ForkByHeightLengthAndPeersCount []Fork

func (a ForkByHeightLengthAndPeersCount) Len() int {
	return len(a)
}

func (a ForkByHeightLengthAndPeersCount) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ForkByHeightLengthAndPeersCount) Less(i, j int) bool {
	if a[i].Longest {
		return true
	}
	if a[i].Height > a[j].Height {
		return true
	}
	if a[i].Length > a[j].Length {
		return true
	}
	return len(a[i].Peers) > len(a[j].Peers)
}

type NodeForkInfo struct {
	Address    net.IP        `json:"address"`
	Nonce      int           `json:"nonce"`
	Name       string        `json:"name"`
	Version    proto.Version `json:"version"`
	OnFork     Fork          `json:"on_fork"`
	OtherForks []Fork        `json:"other_forks"`
}
