package peers

import (
	"fmt"
	"math/big"
	"net/netip"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/fxamacker/cbor/v2"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

const (
	storagePath = "peers"
	addrSize    = 4
)

type key struct {
	addr netip.Addr
}

func (k *key) bytes() []byte {
	buf := make([]byte, addrSize)
	ab, _ := k.addr.MarshalBinary() // err is always nil here.
	copy(buf, ab[:addrSize])        // We accept only IPv4 addresses that is checked on storage level.
	return buf
}

func (k *key) fromBytes(data []byte) {
	if l := len(data); l != addrSize {
		panic(fmt.Sprintf("invalid key size %d of peers storage", l))
	}
	var ab [4]byte
	copy(ab[:], data[:addrSize])
	k.addr = netip.AddrFrom4(ab)
}

type value struct {
	Port        uint16    `cbor:"0,keyasint"`
	Nonce       uint64    `cbor:"1,keyasint"`
	Name        string    `cbor:"2,keyasint"`
	Version     string    `cbor:"3,keyasint"`
	State       State     `cbor:"4,keyasint"`
	NextAttempt time.Time `cbor:"5,keyasint"`
	Score       *big.Int  `cbor:"6,keyasint"`
}

type storage struct {
	db *leveldb.DB
}

func newStorage(path string) (*storage, error) {
	db, err := leveldb.OpenFile(filepath.Clean(filepath.Join(path, storagePath)), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open peers storage: %w", err)
	}
	return &storage{db: db}, nil
}

func (s *storage) close() error {
	return s.db.Close()
}

func (s *storage) peer(addr netip.Addr) (Peer, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return Peer{}, err
	}
	defer sn.Release()
	k := key{addr: addr}
	v, err := sn.Get(k.bytes(), nil)
	if err != nil {
		return Peer{}, err
	}
	val := new(value)
	if umErr := cbor.Unmarshal(v, val); umErr != nil {
		return Peer{}, err
	}
	ver, err := proto.NewVersionFromString(val.Version)
	if err != nil {
		return Peer{}, err
	}
	return Peer{
		AddressPort: netip.AddrPortFrom(addr, val.Port),
		Nonce:       val.Nonce,
		Name:        val.Name,
		Version:     ver,
		State:       val.State,
		NextAttempt: val.NextAttempt,
		Score:       val.Score,
	}, nil
}

func (s *storage) putPeer(peer Peer) error {
	if !peer.AddressPort.Addr().Is4() {
		return fmt.Errorf("invalid IP address length")
	}
	if !utf8.ValidString(peer.Name) {
		return fmt.Errorf("invalid peer name")
	}
	batch := new(leveldb.Batch)
	k := key{addr: peer.AddressPort.Addr()}
	v := value{
		Port:        peer.AddressPort.Port(),
		Nonce:       peer.Nonce,
		Name:        peer.Name,
		Version:     peer.Version.String(),
		State:       peer.State,
		NextAttempt: peer.NextAttempt,
		Score:       peer.Score,
	}
	b, err := cbor.Marshal(v)
	if err != nil {
		return err
	}
	batch.Put(k.bytes(), b)
	return s.db.Write(batch, nil)
}

func (s *storage) peers() ([]Peer, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return nil, fmt.Errorf("failed to collect peers: %w", err)
	}
	defer sn.Release()
	it := sn.NewIterator(&util.Range{Start: nil, Limit: nil}, nil) // Empty range means everything.
	defer it.Release()
	r := make([]Peer, 0)
	for it.Next() {
		k := new(key)
		k.fromBytes(it.Key())
		v := new(value)
		if umErr := cbor.Unmarshal(it.Value(), v); umErr != nil {
			return nil, fmt.Errorf("failed to collect peers: %w", umErr)
		}
		ver, vErr := proto.NewVersionFromString(v.Version)
		if vErr != nil {
			return nil, fmt.Errorf("failed to collect peers: %w", vErr)
		}
		p := Peer{
			AddressPort: netip.AddrPortFrom(k.addr, v.Port),
			Nonce:       v.Nonce,
			Name:        v.Name,
			Version:     ver,
			NextAttempt: v.NextAttempt,
			State:       v.State,
			Score:       v.Score,
		}
		r = append(r, p)
	}
	return r, nil
}

func (s *storage) hasPeer(addr netip.Addr) (bool, error) {
	sn, err := s.db.GetSnapshot()
	if err != nil {
		return false, err
	}
	defer sn.Release()

	k := key{addr: addr}
	return sn.Has(k.bytes(), nil)
}
