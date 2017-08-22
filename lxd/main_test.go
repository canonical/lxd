package main

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"os"

	"github.com/lxc/lxd/lxd/db"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/lxc/lxd/shared"
)

func mockStartDaemon() (*Daemon, error) {
	certBytes, keyBytes, err := shared.GenerateMemCert(false)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil {
		return nil, err
	}

	d := NewDaemon()
	d.os.MockMode = true
	d.tlsConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	if err := d.Init(); err != nil {
		return nil, err
	}

	d.os.IdmapSet = &shared.IdmapSet{Idmap: []shared.IdmapEntry{
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

	// Create default storage pool. Make sure that we don't pass a nil to
	// the next function.
	poolConfig := map[string]string{}

	mockStorage, _ := storageTypeToString(storageTypeMock)
	// Create the database entry for the storage pool.
	poolDescription := fmt.Sprintf("%s storage pool", lxdTestSuiteDefaultStoragePool)
	_, err := dbStoragePoolCreateAndUpdateCache(suite.d.db, lxdTestSuiteDefaultStoragePool, poolDescription, mockStorage, poolConfig)
	if err != nil {
		os.Exit(1)
	}

	rootDev := map[string]string{}
	rootDev["type"] = "disk"
	rootDev["path"] = "/"
	rootDev["pool"] = lxdTestSuiteDefaultStoragePool
	devicesMap := map[string]map[string]string{}
	devicesMap["root"] = rootDev

	defaultID, _, err := db.ProfileGet(suite.d.db, "default")
	if err != nil {
		os.Exit(1)
	}

	tx, err := db.Begin(suite.d.db)
	if err != nil {
		os.Exit(1)
	}

	err = db.DevicesAdd(tx, "profile", defaultID, devicesMap)
	if err != nil {
		tx.Rollback()
		os.Exit(1)
	}

	err = tx.Commit()
	if err != nil {
		os.Exit(1)
	}
	suite.Req = require.New(suite.T())
}

func (suite *lxdTestSuite) TearDownTest() {
	suite.d.db.Close()
}
