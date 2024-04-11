package peers

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/wavesplatform/gowaves/pkg/proto"
)

type VersionProvider interface {
	SuggestVersion(addr netip.Addr) (proto.Version, error)
}

// SuggestVersion returns the best version possible for the peer on given address.
func (r *Registry) SuggestVersion(addr netip.Addr) (proto.Version, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	peer, err := r.storage.peer(addr)
	if err != nil {
		if !errors.Is(err, leveldb.ErrNotFound) {
			return proto.Version{}, err
		}
		ver := r.versions.bestVersion()
		np := Peer{
			AddressPort: netip.AddrPortFrom(addr, 0),
			Version:     ver,
			NextAttempt: time.Now().Add(delay).Round(time.Second),
			State:       PeerUnknown,
		}
		if putErr := r.storage.putPeer(np); putErr != nil {
			return proto.Version{}, fmt.Errorf("failed to save peer: %w", putErr)
		}
		return ver, nil
	}
	// We already saw the peer on this address and port.
	switch peer.State {
	case PeerUnknown:
		peer.Version = r.versions.nextVersion(peer.Version)
		peer.NextAttempt = time.Now().Add(delay).Round(time.Second)
		if err = r.storage.putPeer(peer); err != nil {
			return proto.Version{}, err
		}
		return peer.Version, nil
	case PeerConnected:
		return peer.Version, nil
	case PeerHostile:
		return proto.Version{}, fmt.Errorf("peer is hostile")
	default:
		return proto.Version{}, fmt.Errorf("impossible peer state")
	}
}
