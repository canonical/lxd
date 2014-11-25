package yaml_test

import (
	. "github.com/lxc/lxd/3rdParty/gopkg.in/check.v1"
	"testing"
)

func Test(t *testing.T) { TestingT(t) }

type S struct{}

var _ = Suite(&S{})
