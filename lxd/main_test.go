package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
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
	err := sys.SetupTestCerts(shared.VarPath())
	if err != nil {
		return nil, err
	}

	if err := d.Init(); err != nil {
		return nil, err
	}

	d.os.IdmapSet = &idmap.IdmapSet{Idmap: []idmap.IdmapEntry{
		{Isuid: true, Hostid: 100000, Nsid: 0, Maprange: 500000},
		{Isgid: true, Hostid: 100000, Nsid: 0, Maprange: 500000},
	}}

	return d, nil
}

type lxdTestSuite struct {
	suite.Suite
	d      *Daemon
	Req    *require.Assertions
	tmpdir string
}

const lxdTestSuiteDefaultStoragePool string = "lxdTestrunPool"

func (suite *lxdTestSuite) SetupTest() {
	tmpdir, err := ioutil.TempDir("", "lxd_testrun_")
	if err != nil {
		suite.T().Fatalf("failed to create temp dir: %v", err)
	}
	suite.tmpdir = tmpdir

	if err := os.Setenv("LXD_DIR", suite.tmpdir); err != nil {
		suite.T().Fatalf("failed to set LXD_DIR: %v", err)
	}

	suite.d, err = mockStartDaemon()
	if err != nil {
		suite.T().Fatalf("failed to start daemon: %v", err)
	}

	// Create default storage pool. Make sure that we don't pass a nil to
	// the next function.
	poolConfig := map[string]string{}

	mockStorage, _ := storageTypeToString(storageTypeMock)
	// Create the database entry for the storage pool.
	poolDescription := fmt.Sprintf("%s storage pool", lxdTestSuiteDefaultStoragePool)
	_, err = dbStoragePoolCreateAndUpdateCache(suite.d.cluster, lxdTestSuiteDefaultStoragePool, poolDescription, mockStorage, poolConfig)
	if err != nil {
		suite.T().Fatalf("failed to create default storage pool: %v", err)
	}

	rootDev := map[string]string{}
	rootDev["type"] = "disk"
	rootDev["path"] = "/"
	rootDev["pool"] = lxdTestSuiteDefaultStoragePool
	devicesMap := map[string]map[string]string{}
	devicesMap["root"] = rootDev

	defaultID, _, err := suite.d.cluster.ProfileGet("default", "default")
	if err != nil {
		suite.T().Fatalf("failed to get default profile: %v", err)
	}

	tx, err := suite.d.cluster.Begin()
	if err != nil {
		suite.T().Fatalf("failed to begin transaction: %v", err)
	}

	err = db.DevicesAdd(tx, "profile", defaultID, devicesMap)
	if err != nil {
		tx.Rollback()
		suite.T().Fatalf("failed to rollback transaction: %v", err)
	}

	err = tx.Commit()
	if err != nil {
		suite.T().Fatalf("failed to commit transaction: %v", err)
	}
	suite.Req = require.New(suite.T())
}

func (suite *lxdTestSuite) TearDownTest() {
	err := suite.d.Stop()
	if err != nil {
		suite.T().Fatalf("failed to stop daemon: %v", err)
	}
	err = os.RemoveAll(suite.tmpdir)
	if err != nil {
		suite.T().Fatalf("failed to remove temp dir: %v", err)
	}
}
