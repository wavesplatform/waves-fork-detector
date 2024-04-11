package peers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wavesplatform/gowaves/pkg/proto"
)

func TestNextVersion5(t *testing.T) {
	vs := newVersions([]proto.Version{
		proto.NewVersion(0, 1, 0),
		proto.NewVersion(0, 2, 0),
		proto.NewVersion(0, 3, 0),
		proto.NewVersion(0, 4, 0),
		proto.NewVersion(0, 5, 0),
	})
	v := vs.bestVersion()
	assert.Equal(t, proto.NewVersion(0, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 4, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 3, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 2, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 1, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(0, 4, 0), v)
}

func TestNextVersion2(t *testing.T) {
	vs := newVersions([]proto.Version{
		proto.NewVersion(1, 5, 4),
		proto.NewVersion(1, 4, 18),
	})
	v := vs.bestVersion()
	assert.Equal(t, proto.NewVersion(1, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(1, 4, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(1, 5, 0), v)
	v = vs.nextVersion(v)
	assert.Equal(t, proto.NewVersion(1, 4, 0), v)
}

func TestNextVersionZero(t *testing.T) {
	vs := newVersions([]proto.Version{
		proto.NewVersion(1, 5, 4),
		proto.NewVersion(1, 4, 18),
	})
	v := vs.nextVersion(proto.Version{})
	assert.Equal(t, proto.NewVersion(1, 5, 0), v)
}
