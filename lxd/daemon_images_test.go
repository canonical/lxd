package main

import (
	"testing"
	"time"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/lxd/db"
	"github.com/lxc/lxd/shared/api"
	"github.com/stretchr/testify/suite"
)

type daemonImagesTestSuite struct {
	lxdTestSuite
}

// If the preferCached parameter of ImageDownload is set to true, and
// an image with matching remote details is already present in the
// database, and the auto-update settings is on, we won't download any
// newer image even if available, and just use the cached one.
func (suite *daemonImagesTestSuite) TestUseCachedImagesIfAvailable() {
	// Create an image with alias "test" and fingerprint "abcd".
	err := suite.d.cluster.ImageInsert("default", "abcd", "foo.xz", 1, false, true, "amd64", time.Now(), time.Now(), map[string]string{})
	suite.Req.Nil(err)
	id, _, err := suite.d.cluster.ImageGet("default", "abcd", false, true)
	suite.Req.Nil(err)
	err = suite.d.cluster.ImageSourceInsert(id, "img.srv", "simplestreams", "", "test")
	suite.Req.Nil(err)

	// Pretend we have already a non-expired entry for the remote
	// simplestream data, containing a more recent image for the
	// given alias.
	remote := lxd.ImageServer(&lxd.ProtocolSimpleStreams{})
	alias := api.ImageAliasesEntry{Name: "test"}
	alias.Target = "other-more-recent-fingerprint"
	fingerprints := []string{"other-more-recent-fingerprint"}
	entry := &imageStreamCacheEntry{remote: remote, Aliases: []api.ImageAliasesEntry{alias}, Certificate: "", Fingerprints: fingerprints, expiry: time.Now().Add(time.Hour)}
	imageStreamCache["img.srv"] = entry
	defer delete(imageStreamCache, "img.srv")

	// Request an image with alias "test" and check that it's the
	// one we created above.
	op, err := operationCreate(suite.d.cluster, "default", operationClassTask, db.OperationImageDownload, map[string][]string{}, nil, nil, nil, nil)
	suite.Req.Nil(err)
	image, err := suite.d.ImageDownload(op, "img.srv", "simplestreams", "", "", "test", false, false, "", true, "default")
	suite.Req.Nil(err)
	suite.Req.Equal("abcd", image.Fingerprint)
}

func TestDaemonImagesTestSuite(t *testing.T) {
	suite.Run(t, new(daemonImagesTestSuite))
}
