package main

import (
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/logging"
)

func init() {
	shared.Log = log.Log
	shared.Debugf = log.Debugf
	shared.Logf = log.Logf
}
