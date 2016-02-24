package main

import (
	"fmt"
	"strings"

	"github.com/codegangsta/cli"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/i18n"
)

// TODO: Make this a "hidden" command.
var commandInit = cli.Command{
	Name:      "init",
	Usage:     i18n.G("Initializes a container using the specified image and name."),
	ArgsUsage: i18n.G("[remote:]<image> [remote:][<name>] [--ephemeral|-e] [--profile|-p <profile>...]"),

	Description: `Initialize a container from a particular image.

   lxc init [remote:]<image> [remote:][<name>] [--ephemeral|-e] [--profile|-p <profile>...] [--config|-c <key=value>...]

   Initializes a container using the specified image and name.

   Not specifying -p will result in the default profile.
   Specifying "-p" with an empty argument will result in no profile.

   Example:
   lxc init ubuntu u1`,

	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: i18n.G("Print debug information."),
		},

		cli.BoolFlag{
			Name:  "verbose",
			Usage: i18n.G("Print verbose information."),
		},

		cli.BoolFlag{
			Name:  "ephemeral, e",
			Usage: i18n.G("Ephemeral container."),
		},

		cli.StringSliceFlag{
			Name:  "config, c",
			Value: nil,
			Usage: i18n.G("Config key/value to apply to the new container."),
		},

		cli.StringSliceFlag{
			Name:  "profile, p",
			Value: nil,
			Usage: i18n.G("Profile to apply to the new container."),
		},
	},
	Action: commandWrapper(commandActionInit),
}

func commandActionInit(config *lxd.Config, context *cli.Context) error {
	var cmd = &initCmd{}
	cmd.confArgs = context.StringSlice("config")
	cmd.profArgs = context.StringSlice("profile")
	cmd.ephem = context.Bool("ephemeral")

	return cmd.run(config, context.Args())
}

type profileList []string

var configMap map[string]string

func (f *profileList) String() string {
	return fmt.Sprint(*f)
}

type configList []string

func (f *configList) String() string {
	return fmt.Sprint(configMap)
}

func (f *configList) Set(value string) error {
	if value == "" {
		return fmt.Errorf(i18n.G("Invalid configuration key"))
	}

	items := strings.SplitN(value, "=", 2)
	if len(items) < 2 {
		return fmt.Errorf(i18n.G("Invalid configuration key"))
	}

	if configMap == nil {
		configMap = map[string]string{}
	}

	configMap[items[0]] = items[1]

	return nil
}

func (f *profileList) Set(value string) error {
	if value == "" {
		initRequestedEmptyProfiles = true
		return nil
	}
	if f == nil {
		*f = make(profileList, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

var initRequestedEmptyProfiles bool

type initCmd struct {
	profArgs profileList
	confArgs configList
	ephem    bool
}

func (c *initCmd) run(config *lxd.Config, args []string) error {
	if len(args) > 2 || len(args) < 1 {
		return errArgs
	}

	iremote, image := config.ParseRemoteAndContainer(args[0])

	var name string
	var remote string
	if len(args) == 2 {
		remote, name = config.ParseRemoteAndContainer(args[1])
	} else {
		remote, name = config.ParseRemoteAndContainer("")
	}

	d, err := lxd.NewClient(config, remote)
	if err != nil {
		return err
	}

	// TODO: implement the syntax for supporting other image types/remotes

	/*
	 * initRequestedEmptyProfiles means user requested empty
	 * !initRequestedEmptyProfiles but len(profArgs) == 0 means use profile default
	 */
	profiles := []string{}
	for _, p := range c.profArgs {
		profiles = append(profiles, p)
	}

	var resp *lxd.Response
	if name == "" {
		fmt.Printf(i18n.G("Creating the container") + "\n")
	} else {
		fmt.Printf(i18n.G("Creating %s")+"\n", name)
	}

	if !initRequestedEmptyProfiles && len(profiles) == 0 {
		resp, err = d.Init(name, iremote, image, nil, configMap, c.ephem)
	} else {
		resp, err = d.Init(name, iremote, image, &profiles, configMap, c.ephem)
	}

	if err != nil {
		return err
	}

	c.initProgressTracker(d, resp.Operation)

	err = d.WaitForSuccess(resp.Operation)

	if err != nil {
		return err
	}

	op, err := resp.MetadataAsOperation()
	if err != nil {
		return fmt.Errorf(i18n.G("didn't get any affected image, container or snapshot from server"))
	}

	containers, ok := op.Resources["containers"]
	if !ok || len(containers) == 0 {
		return fmt.Errorf(i18n.G("didn't get any affected image, container or snapshot from server"))
	}

	if len(containers) == 1 && name == "" {
		fmt.Printf(i18n.G("Container name is: %s"), name)
	}

	return nil
}

func (c *initCmd) initProgressTracker(d *lxd.Client, operation string) {
	handler := func(msg interface{}) {
		if msg == nil {
			return
		}

		event := msg.(map[string]interface{})
		if event["type"].(string) != "operation" {
			return
		}

		if event["metadata"] == nil {
			return
		}

		md := event["metadata"].(map[string]interface{})
		if !strings.HasSuffix(operation, md["id"].(string)) {
			return
		}

		if md["metadata"] == nil {
			return
		}

		if shared.StatusCode(md["status_code"].(float64)).IsFinal() {
			return
		}

		opMd := md["metadata"].(map[string]interface{})
		_, ok := opMd["download_progress"]
		if ok {
			fmt.Printf(i18n.G("Retrieving image: %s")+"\r", opMd["download_progress"].(string))
		}

		if opMd["download_progress"].(string) == "100%" {
			fmt.Printf("\n")
		}
	}
	go d.Monitor([]string{"operation"}, handler)
}
