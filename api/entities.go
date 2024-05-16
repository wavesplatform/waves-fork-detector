package api

import (
	"math/big"
	"time"
)

type status struct {
	ShortForksCount     int `json:"short_forks_count"`
	LongForksCount      int `json:"long_forks_count"`
	AllPeersCount       int `json:"all_peers_count"`
	FriendlyPeersCount  int `json:"friendly_peers_count"`
	ConnectedPeersCount int `json:"connected_peers_count"`
	TotalBlocksCount    int `json:"total_blocks_count"`
	GoroutinesCount     int `json:"goroutines_count"`
}

type headInfo struct {
	Number    uint64    `json:"number"`
	ID        string    `json:"id"`
	Height    uint32    `json:"height"`
	Score     *big.Int  `json:"score"`
	Timestamp time.Time `json:"timestamp"`
}

type leashInfo struct {
	BlockID    string    `json:"block_id"`
	Height     uint32    `json:"height"`
	Score      *big.Int  `json:"score"`
	Timestamp  time.Time `json:"timestamp"`
	PeersCount int       `json:"peers_count"`
	Peers      []string  `json:"peers"`
}

type byScoreAndPeersDesc []leashInfo

func (a byScoreAndPeersDesc) Len() int {
	return len(a)
}

func (a byScoreAndPeersDesc) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a byScoreAndPeersDesc) Less(i, j int) bool {
	r := a[i].Score.Cmp(a[j].Score)
	if r == 0 {
		return a[i].PeersCount > a[j].PeersCount
	}
	return r > 0
}
