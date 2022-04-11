//go:build linux

package logger

import (
	"log/syslog"

	"github.com/sirupsen/logrus"
	lSyslog "github.com/sirupsen/logrus/hooks/syslog"
)

func setupSyslog(logger *logrus.Logger, syslogName string) error {
	syslogHook, err := lSyslog.NewSyslogHook("", "", syslog.LOG_INFO, syslogName)
	if err != nil {
		return err
	}

	logger.AddHook(syslogHook)
	return nil
}
