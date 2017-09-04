package main

import (
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
)

func mockStartDaemon() (*Daemon, error) {
	d := DefaultDaemon()
	d.os.MockMode = true

	// Setup test certificates. We re-use the ones already on disk under
	// the test/ directory, to avoid generating new ones, which is
	// expensive.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	deps := filepath.Join(cwd, "..", "test", "deps")
	for _, f := range []string{"server.crt", "server.key"} {
		err := os.Symlink(
			filepath.Join(deps, f),
			filepath.Join(shared.VarPath(), f),
		)
		if err != nil {
			return nil, err
		}
	}

	if err := d.Init(); err != nil {
		return nil, err
	}

	d.os.IdmapSet = &idmap.IdmapSet{Idmap: []idmap.IdmapEntry{
		{Isuid: true, Hostid: 100000, Nsid: 0, Maprange: 500000},
		{Isgid: true, Hostid: 100000, Nsid: 0, Maprange: 500000},
	}}

	// Call this after Init so we have a log object.
	storageConfig := make(map[string]interface{})
	d.Storage = &storageLogWrapper{w: &storageMock{d: d}}
	if _, err := d.Storage.Init(storageConfig); err != nil {
		return nil, err
	}

	return d, nil
}

type lxdTestSuite struct {
	suite.Suite
	d      *Daemon
	Req    *require.Assertions
	tmpdir string
}

func (suite *lxdTestSuite) SetupSuite() {
	tmpdir, err := ioutil.TempDir("", "lxd_testrun_")
	if err != nil {
		os.Exit(1)
	}
	suite.tmpdir = tmpdir

	if err := os.Setenv("LXD_DIR", suite.tmpdir); err != nil {
		os.Exit(1)
	}

	suite.d, err = mockStartDaemon()
	if err != nil {
		os.Exit(1)
	}
	suite.Req = require.New(suite.T())
}

func (suite *lxdTestSuite) TearDownSuite() {
	suite.d.Stop()

	err := os.RemoveAll(suite.tmpdir)
	if err != nil {
		os.Exit(1)
	}
}

func (suite *lxdTestSuite) SetupTest() {
	initializeDbObject(suite.d)
	daemonConfigInit(suite.d.db)
	suite.Req = require.New(suite.T())
}

func (suite *lxdTestSuite) TearDownTest() {
	suite.d.db.Close()
	os.Remove(filepath.Join(suite.d.os.VarDir, "lxd.db"))
}
