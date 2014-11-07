package main

import (
	"log"
	"os"

	"github.com/codegangsta/cli"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/lxc/commands"
	"github.com/lxc/lxd/lxc/modules/settings"
)

var (
	APP_VER = "0.0.1"

	globalFlags = []cli.Flag{
		cli.BoolFlag{"verbose, V", "Enable verbose mode", ""},
		cli.BoolFlag{"debug, d", "Enable debug mode", ""},
	}
)

func init() {
	settings.Appver = APP_VER
}

func main() {
	app := cli.NewApp()
	app.Name = "lxd"
	app.Usage = "lxd (pronounced lex-dee) is a REST API, command line tool and OpenStack plugin based on liblxc"
	app.Version = APP_VER
	app.Commands = []cli.Command{
		commands.Ping,
	}
	app.Flags = append(app.Flags, globalFlags...)
	app.Before = func(c *cli.Context) error {
		settings.Verbose = c.GlobalBool("verbose")
		settings.Debug = c.GlobalBool("debug")
		if settings.Verbose || settings.Debug {
			lxd.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
			lxd.SetDebug(settings.Debug)
		}
		return nil
	}
	app.Run(os.Args)
}

/* vim: set noet ts=4 sw=4 sts=4: */
