package main

import (
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/logging"
)

func init() {
	shared.Logfunc = log.LxdLog
	shared.Debugf = log.Debugf
	shared.Logf = log.Logf
}
