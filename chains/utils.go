package chains

import (
	"math"
	"math/big"
)

const decimalBase = 10

func calculateScore(baseTarget uint64) *big.Int {
	res := big.NewInt(0)
	if baseTarget == 0 {
		return res
	}
	if baseTarget > math.MaxInt64 {
		panic("base target is too big")
	}
	bt := big.NewInt(int64(baseTarget))
	maxBlockScore, ok := big.NewInt(0).SetString("18446744073709551616", decimalBase)
	if !ok {
		return res
	}
	res.Div(maxBlockScore, bt)
	return res
}

func calculateCumulativeScore(parentScore *big.Int, baseTarget uint64) *big.Int {
	s := calculateScore(baseTarget)
	if parentScore == nil {
		return s
	}
	return s.Add(s, parentScore)
}
