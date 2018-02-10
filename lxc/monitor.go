package main

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v2"

	"github.com/lxc/lxd/lxc/config"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
	"github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logging"
)

type typeList []string

func (f *typeList) String() string {
	return fmt.Sprint(*f)
}

func (f *typeList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("Invalid type: %s", value)
	}

	if f == nil {
		*f = make(typeList, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

type monitorCmd struct {
	typeArgs typeList
	pretty   bool
	logLevel string
}

func (c *monitorCmd) showByDefault() bool {
	return false
}

func (c *monitorCmd) usage() string {
	return i18n.G(
		`Usage: lxc monitor [<remote>:] [--type=TYPE...] [--pretty]

Monitor a local or remote LXD server.

By default the monitor will listen to all message types.

Message types to listen for can be specified with --type.

*Examples*
lxc monitor --type=logging
    Only show log messages.

lxc monitor --pretty --type=logging --loglevel=info
    Show a pretty log of messages with info level or higher.
`)
}

func (c *monitorCmd) flags() {
	gnuflag.BoolVar(&c.pretty, "pretty", false, i18n.G("Pretty rendering"))
	gnuflag.Var(&c.typeArgs, "type", i18n.G("Event type to listen for"))
	gnuflag.StringVar(&c.logLevel, "loglevel", "", i18n.G("Minimum level for log messages"))
}

func (c *monitorCmd) run(conf *config.Config, args []string) error {
	var err error
	var remote string

	if len(args) > 1 {
		return errArgs
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
	if c.logLevel != "" {
		logLvl, err = log15.LvlFromString(c.logLevel)
		if err != nil {
			return err
		}
	}

	handler := func(message interface{}) {
		// Special handling for logging only output
		if c.pretty && len(c.typeArgs) == 1 && shared.StringInSlice("logging", c.typeArgs) {
			render, err := json.Marshal(&message)
			if err != nil {
				fmt.Printf("error: %s\n", err)
				os.Exit(1)
			}

			event := api.Event{}
			err = json.Unmarshal(render, &event)
			if err != nil {
				fmt.Printf("error: %s\n", err)
				os.Exit(1)
			}

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

		render, err := yaml.Marshal(&message)
		if err != nil {
			fmt.Printf("error: %s\n", err)
			os.Exit(1)
		}

		fmt.Printf("%s\n\n", render)
	}

	_, err = listener.AddHandler(c.typeArgs, handler)
	if err != nil {
		return err
	}

	return listener.Wait()
}
