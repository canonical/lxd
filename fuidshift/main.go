package main

import (
	"fmt"
	"os"

	"github.com/lxc/lxd/shared"
)

func help(me string, status int) {
	fmt.Printf("Usage: %s directory [-t] [-r] <range1> [<range2> ...]\n", me)
	fmt.Printf("  -t implies test mode.  No file ownerships will be changed.\n")
	fmt.Printf("  -r means reverse, that is shift the uids out of hte container.\n")
	fmt.Printf("\n")
	fmt.Printf("  A range is [u|b|g]:<first_container_id:first_host_id:range>.\n")
	fmt.Printf("  where u means shift uids, g means shift gids, b means shift both.\n")
	fmt.Printf("  For example: %s directory b:0:100000:65536 u:10000:1000:1\n", me)
	os.Exit(status)
}

func main() {
	if err := run(); err != nil {
		fmt.Printf("Error: %q\n", err)
		help(os.Args[0], 1)
	}
}

func run() error {
	if len(os.Args) < 3 {
		if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
			help(os.Args[0], 0)
		} else {
			help(os.Args[0], 1)
		}
	}

	directory := os.Args[1]
	idmap := shared.IdmapSet{}
	testmode := false
	reverse := false

	for pos := 2; pos < len(os.Args); pos++ {

		switch os.Args[pos] {
		case "-r", "--reverse":
			reverse = true
		case "t", "-t", "--test", "test":
			testmode = true
		default:
			var err error
			idmap, err = idmap.Append(os.Args[pos])
			if err != nil {
				return err
			}
		}
	}

	if idmap.Len() == 0 {
		fmt.Printf("No idmaps given\n")
		help(os.Args[0], 1)
	}

	if !testmode && os.Geteuid() != 0 {
		fmt.Printf("This must be run as root\n")
		os.Exit(1)
	}

	if reverse {
		return idmap.UidshiftFromContainer(directory, testmode)
	}
	return idmap.UidshiftIntoContainer(directory, testmode)
}
