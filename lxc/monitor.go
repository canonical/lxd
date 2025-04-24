package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v2"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	cli "github.com/canonical/lxd/shared/cmd"
	"github.com/canonical/lxd/shared/i18n"
)

type cmdMonitor struct {
	global *cmdGlobal

	flagType        []string
	flagPretty      bool
	flagLogLevel    string
	flagAllProjects bool
	flagFormat      string
}

func (c *cmdMonitor) command() *cobra.Command {
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

	cmd.RunE = c.run
	cmd.Flags().BoolVar(&c.flagPretty, "pretty", false, i18n.G("Pretty rendering (short for --format=pretty)"))
	cmd.Flags().BoolVar(&c.flagAllProjects, "all-projects", false, i18n.G("Show events from all projects"))
	cmd.Flags().StringArrayVar(&c.flagType, "type", nil, i18n.G("Event type to listen for")+"``")
	cmd.Flags().StringVar(&c.flagLogLevel, "loglevel", "", i18n.G("Minimum level for log messages (only available when using pretty format)")+"``")
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", "yaml", i18n.G("Format (json|pretty|yaml)")+"``")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return c.global.cmpRemotes(toComplete, ":", false, instanceServerRemoteCompletionFilters(*c.global.conf)...)
	}

	return cmd
}

func (c *cmdMonitor) run(cmd *cobra.Command, args []string) error {
	conf := c.global.conf

	var err error
	var remote string

	// Quick checks.
	exit, err := c.global.CheckArgs(cmd, args, 0, 1)
	if exit {
		return err
	}

	if !shared.ValueInSlice(c.flagFormat, []string{"json", "pretty", "yaml"}) {
		return fmt.Errorf(i18n.G("Invalid format: %s"), c.flagFormat)
	}

	// Setup format.
	if c.flagPretty {
		c.flagFormat = "pretty"
	}

	if c.flagFormat != "pretty" && c.flagLogLevel != "" {
		return errors.New(i18n.G("Log level filtering can only be used with pretty formatting"))
	}

	// Connect to the event source.
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

	var listener *lxd.EventListener
	if c.flagAllProjects {
		listener, err = d.GetEventsAllProjects()
	} else {
		listener, err = d.GetEvents()
	}

	if err != nil {
		return err
	}

	logLevel := logrus.DebugLevel
	if c.flagLogLevel != "" {
		logLevel, err = logrus.ParseLevel(c.flagLogLevel)
		if err != nil {
			return err
		}
	}

	chError := make(chan error, 1)

	handler := func(event api.Event) {
		if c.flagFormat == "pretty" {
			// Parse the event.
			record, err := event.ToLogging()
			if err != nil {
				chError <- err
				return
			}

			if record.Lvl == "dbug" {
				record.Lvl = "debug"
			}

			// Get the log level.
			msgLevel, err := logrus.ParseLevel(record.Lvl)
			if err != nil {
				chError <- err
				return
			}

			// Check log level.
			if msgLevel > logLevel {
				return
			}

			// Setup logrus.
			logger := &logrus.Logger{
				Out: os.Stdout,
			}

			entry := &logrus.Entry{Logger: logger}
			entry.Data = c.unpackCtx(record.Ctx)
			entry.Message = record.Msg
			entry.Time = record.Time
			entry.Level = msgLevel
			format := logrus.TextFormatter{FullTimestamp: true, PadLevelText: true}

			line, err := format.Format(entry)
			if err != nil {
				chError <- err
				return
			}

			fmt.Print(string(line))
			return
		}

		// Render as JSON (to expand RawMessage)
		jsonRender, err := json.Marshal(&event)
		if err != nil {
			chError <- err
			return
		}

		// Read back to a clean interface
		var rawEvent any
		err = json.Unmarshal(jsonRender, &rawEvent)
		if err != nil {
			chError <- err
			return
		}

		// And now print the result.
		var render []byte
		switch c.flagFormat {
		case "yaml":
			render, err = yaml.Marshal(&rawEvent)
			if err != nil {
				chError <- err
				return
			}

		case "json":
			render, err = json.Marshal(&rawEvent)
			if err != nil {
				chError <- err
				return
			}
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

func (c *cmdMonitor) unpackCtx(ctx []any) logrus.Fields {
	out := logrus.Fields{}

	var key string
	for _, entry := range ctx {
		if key == "" {
			key = fmt.Sprint(entry)
		} else {
			out[key] = fmt.Sprint(entry)
			key = ""
		}
	}

	return out
}
