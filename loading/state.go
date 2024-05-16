package loading

//go:generate stringer -type state -trimprefix state
type state int

const (
	stateIdle state = iota
	stateWaitingForIDs
	stateWaitingForBlocks
	stateDone
)
