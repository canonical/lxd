package main

import (
	"encoding/json"
	"fmt"
	"os"

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
	cmd.Use = i18n.G("monitor [<remote>:]")
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

	// Sanity checks
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

	d, err := conf.GetContainerServer(remote)
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

	handler := func(event api.Event) {
		// Special handling for logging only output
		if c.flagPretty && len(c.flagType) == 1 && shared.StringInSlice("logging", c.flagType) {
			logEntry := api.EventLogging{}
			err = json.Unmarshal(event.Metadata, &logEntry)
			if err != nil {
				fmt.Printf("error: %s\n", err)
				os.Exit(1)
			}

			lvl, err := log15.LvlFromString(logEntry.Level)
			if err != nil {
				fmt.Printf("error: %s\n", err)
				os.Exit(1)
			}

			if lvl > logLvl {
				return
			}

			ctx := []interface{}{}
			for k, v := range logEntry.Context {
				ctx = append(ctx, k)
				ctx = append(ctx, v)
			}

			record := log15.Record{
				Time: event.Timestamp,
				Lvl:  lvl,
				Msg:  logEntry.Message,
				Ctx:  ctx,
			}

			format := logging.TerminalFormat()
			fmt.Printf("%s", format.Format(&record))
			return
		}

		// Render as JSON (to expand RawMessage)
		jsonRender, err := json.Marshal(&event)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(1)
		}

		// Read back to a clean interface
		var rawEvent interface{}
		err = json.Unmarshal(jsonRender, &rawEvent)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(1)
		}

		// And now print as YAML
		render, err := yaml.Marshal(&rawEvent)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(1)
		}

		fmt.Printf("%s\n\n", render)
	}

	_, err = listener.AddHandler(c.flagType, handler)
	if err != nil {
		return err
	}

	return listener.Wait()
}
