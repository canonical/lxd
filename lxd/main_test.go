package main

import (
	"crypto/tls"
	"io/ioutil"
	"os"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/shared/idmap"
)

func mockStartDaemon() (*Daemon, error) {
	d := NewDaemon()
	d.os.MockMode = true
	d.tlsConfig = &tls.Config{}

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
	initializeDbObject(suite.d, ":memory:")
	daemonConfigInit(suite.d.db)
	suite.Req = require.New(suite.T())

	suite.d.pruneChan = make(chan bool)
	suite.d.resetAutoUpdateChan = make(chan bool)
	go func() {
		for {
			select {
			case <-suite.d.pruneChan:
			case <-suite.d.resetAutoUpdateChan:
				continue
			}
		}
	}()
}

func (suite *lxdTestSuite) TearDownTest() {
	suite.d.db.Close()
}
