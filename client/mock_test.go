package lxd_test

import (
    "github.com/lxc/lxd/client"
    "github.com/lxc/lxd/client/mocks"
)

var _ lxd.Server = (*mocks.MockServer)(nil)
var _ lxd.ImageServer = (*mocks.MockImageServer)(nil)
var _ lxd.ContainerServer = (*mocks.MockContainerServer)(nil)
