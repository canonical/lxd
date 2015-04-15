package main

import (
	"fmt"
	"os"

	"github.com/gosexy/gettext"
	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
)

type initCmd struct{}

func (c *initCmd) showByDefault() bool {
	return false
}

func (c *initCmd) usage() string {
	return gettext.Gettext(
		"lxc init <image> [<name>] [--ephemeral|-e] [--profile|-p <profile>...]\n" +
			"\n" +
			"Initializes a container using the specified image and name.\n" +
			"\n" +
			"Not specifying -p will result in the default profile.\n" +
			"Specifying \"-p\" with no argument will result in no profile.\n" +
			"\n" +
			"Example:\n" +
			"lxc init ubuntu u1\n")
}

type profileList []string

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

var profArgs profileList
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
	gnuflag.Var(&profArgs, "profile", "Profile to apply to the new container")
	gnuflag.Var(&profArgs, "p", "Profile to apply to the new container")
	gnuflag.BoolVar(&ephem, "ephemeral", false, gettext.Gettext("Ephemeral container"))
	gnuflag.BoolVar(&ephem, "e", false, gettext.Gettext("Ephemeral container"))
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
		name = ""
		remote = ""
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

	if !requested_empty_profiles && len(profiles) == 0 {
		resp, err = d.Init(name, iremote, image, nil, ephem)
	} else {
		resp, err = d.Init(name, iremote, image, &profiles, ephem)
	}

	if err != nil {
		return err
	}

	return d.WaitForSuccess(resp.Operation)
}
