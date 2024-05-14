package peers

//go:generate stringer -type State -trimprefix Peer
type State byte

const (
	PeerUnknown State = iota
	PeerConnected
	PeerHostile
)
