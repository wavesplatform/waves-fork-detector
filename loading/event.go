package loading

//go:generate stringer -type event -trimprefix event
type event int

const (
	eventStart event = iota
	eventRestart
	eventIDs
	eventBlock
	eventTick
	eventTimeout
	eventQueueReady
)
