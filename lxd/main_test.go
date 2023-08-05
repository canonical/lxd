package main

import (
	"context"
	"fmt"
	"os"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"

	"github.com/canonical/lxd/lxd/db"
	"github.com/canonical/lxd/lxd/db/cluster"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/idmap"
)

// mockStartDaemon creates and initializes a new mock Daemon instance for testing purposes.
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

	err = d.Init()
	if err != nil {
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

// SetupTest sets up the test environment with a temporary directory, mock LXD daemon,
// and default storage pool and profile.
func (suite *lxdTestSuite) SetupTest() {
	tmpdir, err := os.MkdirTemp("", "lxd_testrun_")
	if err != nil {
		suite.T().Errorf("failed to create temp dir: %v", err)
	}

	suite.tmpdir = tmpdir

	err = os.Setenv("LXD_DIR", suite.tmpdir)
	if err != nil {
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
	rootDev["path"] = "/"
	rootDev["pool"] = lxdTestSuiteDefaultStoragePool
	device := cluster.Device{
		Name:   "root",
		Type:   cluster.TypeDisk,
		Config: rootDev,
	}

	err = suite.d.db.Cluster.Transaction(context.TODO(), func(ctx context.Context, tx *db.ClusterTx) error {
		profile, err := cluster.GetProfile(ctx, tx.Tx(), "default", "default")
		if err != nil {
			return err
		}

		return cluster.UpdateProfileDevices(ctx, tx.Tx(), int64(profile.ID), map[string]cluster.Device{"root": device})
	})
	if err != nil {
		suite.T().Errorf("failed to update default profile: %v", err)
	}

	suite.Req = require.New(suite.T())
}

// TearDownTest stops the LXD daemon and removes the temporary directory used for testing.
func (suite *lxdTestSuite) TearDownTest() {
	err := suite.d.Stop(context.Background(), unix.SIGQUIT)
	if err != nil {
		suite.T().Errorf("failed to stop daemon: %v", err)
	}

	err = os.RemoveAll(suite.tmpdir)
	if err != nil {
		suite.T().Errorf("failed to remove temp dir: %v", err)
	}
}
