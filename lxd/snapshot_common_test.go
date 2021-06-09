package main

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/revert"
)

func (suite *containerTestSuite) TestSnapshotScheduling() {
	args := db.InstanceArgs{
		Type:      instancetype.Container,
		Ephemeral: false,
		Name:      "hal9000",
	}

	c, err := instance.CreateInternal(suite.d.State(), args, revert.New())
	suite.Req.Nil(err)
	suite.Equal(true, snapshotIsScheduledNow("* * * * *",
		int64(c.ID())),
		"snapshot.schedule config '* * * * *' should have matched now")
	suite.Equal(true, snapshotIsScheduledNow("@daily,"+
		"@hourly,"+
		"@midnight,"+
		"@weekly,"+
		"@monthly,"+
		"@annually,"+
		"@yearly,"+
		" * * * * *",
		int64(c.ID())),
		"snapshot.schedule config '* * * * *' should have matched now")
}

func TestSnapshotCommon(t *testing.T) {
	suite.Run(t, new(containerTestSuite))
}
