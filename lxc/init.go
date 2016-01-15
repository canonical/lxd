package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/i18n"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/gnuflag"
)

type initCmd struct{}

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

type profileList []string
type configList []string

var configMap map[string]string

func (f *profileList) String() string {
	return fmt.Sprint(*f)
}

func (f *profileList) Set(value string) error {
	if value == "" {
		requested_empty_profiles = true
		return nil
	}
	if f == nil {
		*f = make(profileList, 1)
	} else {
		*f = append(*f, value)
	}
	return nil
}

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

var profArgs profileList
var confArgs configList
var requested_empty_profiles bool = false
var ephem bool = false

func is_ephem(s string) bool {
	switch s {
	case "-e":
		return true
	case "--ephemeral":
		return true
	}
	return false
}

func is_profile(s string) bool {
	switch s {
	case "-p":
		return true
	case "--profile":
		return true
	}
	return false
}

func massage_args() {
	l := len(os.Args)
	if l < 2 {
		return
	}

	if is_profile(os.Args[l-1]) {
		requested_empty_profiles = true
		os.Args = os.Args[0 : l-1]
		return
	}

	if l < 3 {
		return
	}

	/* catch "lxc init ubuntu -p -e */
	if is_ephem(os.Args[l-1]) && is_profile(os.Args[l-2]) {
		requested_empty_profiles = true
		newargs := os.Args[0 : l-2]
		newargs = append(newargs, os.Args[l-1])
		return
	}
}

func (c *initCmd) flags() {
	massage_args()
	gnuflag.Var(&confArgs, "config", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&confArgs, "c", i18n.G("Config key/value to apply to the new container"))
	gnuflag.Var(&profArgs, "profile", i18n.G("Profile to apply to the new container"))
	gnuflag.Var(&profArgs, "p", i18n.G("Profile to apply to the new container"))
	gnuflag.BoolVar(&ephem, "ephemeral", false, i18n.G("Ephemeral container"))
	gnuflag.BoolVar(&ephem, "e", false, i18n.G("Ephemeral container"))
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
	 * requested_empty_profiles means user requested empty
	 * !requested_empty_profiles but len(profArgs) == 0 means use profile default
	 */
	profiles := []string{}
	for _, p := range profArgs {
		profiles = append(profiles, p)
	}

	var resp *lxd.Response
	if name == "" {
		fmt.Printf(i18n.G("Creating the container") + "\n")
	} else {
		fmt.Printf(i18n.G("Creating %s")+"\n", name)
	}

	if !requested_empty_profiles && len(profiles) == 0 {
		resp, err = d.Init(name, iremote, image, nil, configMap, ephem)
	} else {
		resp, err = d.Init(name, iremote, image, &profiles, configMap, ephem)
	}

	if err != nil {
		return err
	}

	initProgressTracker(d, resp.Operation)

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

func initProgressTracker(d *lxd.Client, operation string) {
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
