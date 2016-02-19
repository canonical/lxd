package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
	"github.com/lxc/lxd/shared/i18n"
)

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

func (c *initCmd) showByDefault() bool {
	return false
}

func (c *initCmd) usage() string {
	return i18n.G(
		`Initialize a container from a particular image.

lxc init [remote:]<image> [remote:][<name>] [--ephemeral|-e] [--profile|-p <profile>...] [--config|-c <key=value>...]

Initializes a container using the specified image and name.

Not specifying -p will result in the default profile.
Specifying "-p" with no argument will result in no profile.

Example:
lxc init ubuntu u1`)
}

func (c *initCmd) is_ephem(s string) bool {
	switch s {
	case "-e":
		return true
	case "--ephemeral":
		return true
	}
	return false
}

func (c *initCmd) is_profile(s string) bool {
	switch s {
	case "-p":
		return true
	case "--profile":
		return true
	}
	return false
}

func (c *initCmd) massage_args() {
	l := len(os.Args)
	if l < 2 {
		return
	}

	if c.is_profile(os.Args[l-1]) {
		initRequestedEmptyProfiles = true
		os.Args = os.Args[0 : l-1]
		return
	}

	if l < 3 {
		return
	}

	/* catch "lxc init ubuntu -p -e */
	if c.is_ephem(os.Args[l-1]) && c.is_profile(os.Args[l-2]) {
		initRequestedEmptyProfiles = true
		newargs := os.Args[0 : l-2]
		newargs = append(newargs, os.Args[l-1])
		return
	}
}

func (c *initCmd) flags() {
	c.massage_args()
	gnuflag.Var(&c.confArgs, "config", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&c.confArgs, "c", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&c.profArgs, "profile", i18n.G("Profile to apply to the new container"))
	gnuflag.Var(&c.profArgs, "p", i18n.G("Profile to apply to the new container"))
	gnuflag.BoolVar(&c.ephem, "ephemeral", false, i18n.G("Ephemeral container"))
	gnuflag.BoolVar(&c.ephem, "e", false, i18n.G("Ephemeral container"))
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
	} else {
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
			fmt.Printf("\n")
			return
		}

		opMd := md["metadata"].(map[string]interface{})
		_, ok := opMd["download_progress"]
		if ok {
			fmt.Printf(i18n.G("Retrieving image: %s")+"\r", opMd["download_progress"].(string))
		}
	}
	go d.Monitor([]string{"operation"}, handler)
}
