package peers

import "strings"

type ByName []Peer

func (a ByName) Len() int {
	return len(a)
}

func (a ByName) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a ByName) Less(i, j int) bool {
	x := a[i].Name
	y := a[j].Name
	return strings.ToUpper(x) < strings.ToUpper(y)
}
