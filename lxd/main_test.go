package main

import (
	"fmt"
	"io/ioutil"
	"os"

	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
)

func mockStartDaemon() (*Daemon, error) {
	d := defaultDaemon()
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
		suite.T().Errorf("failed to create temp dir: %v", err)
	}
	suite.tmpdir = tmpdir

	if err := os.Setenv("LXD_DIR", suite.tmpdir); err != nil {
		suite.T().Errorf("failed to set LXD_DIR: %v", err)
	}

	suite.d, err = mockStartDaemon()
	if err != nil {
		suite.T().Errorf("failed to start daemon: %v", err)
	}

	// Create default storage pool. Make sure that we don't pass a nil to
	// the next function.
	poolConfig := map[string]string{}

	// Create the database entry for the storage pool.
	poolDescription := fmt.Sprintf("%s storage pool", lxdTestSuiteDefaultStoragePool)
	_, err = dbStoragePoolCreateAndUpdateCache(suite.d.State(), lxdTestSuiteDefaultStoragePool, poolDescription, "mock", poolConfig)
	if err != nil {
		suite.T().Errorf("failed to create default storage pool: %v", err)
	}

	rootDev := map[string]string{}
	rootDev["type"] = "disk"
	rootDev["path"] = "/"
	rootDev["pool"] = lxdTestSuiteDefaultStoragePool
	devicesMap := deviceConfig.Devices{}
	devicesMap["root"] = rootDev

	err = suite.d.cluster.Transaction(func(tx *db.ClusterTx) error {
		profile, err := tx.GetProfile("default", "default")
		if err != nil {
			return err
		}
		profile.Devices = devicesMap.CloneNative()
		return tx.UpdateProfile("default", "default", *profile)
	})
	if err != nil {
		suite.T().Errorf("failed to update default profile: %v", err)
	}

	suite.Req = require.New(suite.T())
}

func (suite *lxdTestSuite) TearDownTest() {
	err := suite.d.Stop()
	if err != nil {
		suite.T().Errorf("failed to stop daemon: %v", err)
	}
	err = os.RemoveAll(suite.tmpdir)
	if err != nil {
		suite.T().Errorf("failed to remove temp dir: %v", err)
	}
}
