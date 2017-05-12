package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type daemonTestSuite struct {
	lxdTestSuite
}

func (suite *daemonTestSuite) Test_config_value_set_empty_removes_val() {
	var err error
	d := suite.d

	err = daemonConfig["core.trust_password"].Set(d, "foo")
	suite.Req.Nil(err)

	val := daemonConfig["core.trust_password"].Get()
	suite.Req.Equal(len(val), 192)

	valMap := daemonConfigRender()
	value, present := valMap["core.trust_password"]
	suite.Req.True(present)
	suite.Req.Equal(value, true)

	err = daemonConfig["core.trust_password"].Set(d, "")
	suite.Req.Nil(err)

	val = daemonConfig["core.trust_password"].Get()
	suite.Req.Equal(val, "")

	valMap = daemonConfigRender()
	_, present = valMap["core.trust_password"]
	suite.Req.False(present)
}

func TestDaemonTestSuite(t *testing.T) {
	suite.Run(t, new(daemonTestSuite))
}
