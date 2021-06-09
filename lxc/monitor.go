package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	cli "github.com/lxc/lxd/shared/cmd"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logging"
)

type cmdMonitor struct {
	global *cmdGlobal

	flagType     []string
	flagPretty   bool
	flagLogLevel string
}

func (c *cmdMonitor) Command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = usage("monitor", i18n.G("[<remote>:]"))
	cmd.Short = i18n.G("Monitor a local or remote LXD server")
	cmd.Long = cli.FormatSection(i18n.G("Description"), i18n.G(
		`Monitor a local or remote LXD server

By default the monitor will listen to all message types.`))
	cmd.Example = cli.FormatSection("", i18n.G(
		`lxc monitor --type=logging
    Only show log messages.

lxc monitor --pretty --type=logging --loglevel=info
    Show a pretty log of messages with info level or higher.

lxc monitor --type=lifecycle
    Only show lifecycle events.`))
	cmd.Hidden = true

	cmd.RunE = c.Run
	cmd.Flags().BoolVar(&c.flagPretty, "pretty", false, i18n.G("Pretty rendering"))
	cmd.Flags().StringArrayVar(&c.flagType, "type", nil, i18n.G("Event type to listen for")+"``")
	cmd.Flags().StringVar(&c.flagLogLevel, "loglevel", "", i18n.G("Minimum level for log messages")+"``")

	return cmd
}

func (c *cmdMonitor) Run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	var err error
	var remote string

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	if len(args) == 0 {
		remote, _, err = conf.ParseRemote("")
		if err != nil {
			return err
		}
	} else {
		remote, _, err = conf.ParseRemote(args[0])
		if err != nil {
			return err
		}
	}

	d, err := conf.GetInstanceServer(remote)
	if err != nil {
		return err
	}

	listener, err := d.GetEvents()
	if err != nil {
		return err
	}

	logLvl := log15.LvlDebug
	if c.flagLogLevel != "" {
		logLvl, err = log15.LvlFromString(c.flagLogLevel)
		if err != nil {
			return err
		}
	}

	chError := make(chan error, 1)

	handler := func(event api.Event) {
		// Special handling for logging and lifecycle output
		if c.flagPretty && !shared.StringInSlice("operation", c.flagType) {
			format := logging.TerminalFormat()
			record, err := event.ToLogging()
			if err != nil {
				chError <- err
				return
			}

			lvl, err := log15.LvlFromString(record.Lvl)
			if err != nil {
				chError <- err
				return
			}

			log15Record := log15.Record{
				Time: record.Time,
				Lvl:  lvl,
				Msg:  record.Msg,
				Ctx:  record.Ctx,
			}
			// Check log level for logging type
			// `lifecycle` type have fixed `info` log level
			if event.Type == "logging" && (log15Record.Lvl > logLvl) {
				return
			}
			fmt.Printf("%s", format.Format(&log15Record))
			return
		}

		// Render as JSON (to expand RawMessage)
		jsonRender, err := json.Marshal(&event)
		if err != nil {
			chError <- err
			return
		}

		// Read back to a clean interface
		var rawEvent interface{}
		err = json.Unmarshal(jsonRender, &rawEvent)
		if err != nil {
			chError <- err
			return
		}

		// And now print as YAML
		render, err := yaml.Marshal(&rawEvent)
		if err != nil {
			chError <- err
			return
		}

		fmt.Printf("%s\n\n", render)
	}

	_, err = listener.AddHandler(c.flagType, handler)
	if err != nil {
		return err
	}

	go func() {
		chError <- listener.Wait()
	}()

	return <-chError
}
