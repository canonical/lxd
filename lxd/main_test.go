package main

import (
	"io/ioutil"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type lxdTestSuite struct {
	suite.Suite
	d      *Daemon
	Req    *require.Assertions
	tmpdir string
}

func (suite *lxdTestSuite) SetupSuite() {
	cwd, err := os.Getwd()
	if err != nil {
		os.Exit(1)
	}

	suite.tmpdir, err = ioutil.TempDir(
		path.Join(path.Dir(cwd), "test"), "lxd_testrun_")
	if err != nil {
		os.Exit(1)
	}

	if err := os.Setenv("LXD_DIR", suite.tmpdir); err != nil {
		os.Exit(1)
	}

	suite.d, err = mockStartDaemon()
	if err != nil {
		os.Exit(1)
	}
}

func (suite *lxdTestSuite) TearDownSuite() {
	suite.d.Stop()

	err := os.RemoveAll(suite.tmpdir)
	if err != nil {
		os.Exit(1)
	}
}

func (suite *lxdTestSuite) SetupTest() {
	suite.Req = require.New(suite.T())
}

func TestLxdTestSuite(t *testing.T) {
	suite.Run(t, new(lxdTestSuite))
}
