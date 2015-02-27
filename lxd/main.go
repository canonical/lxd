package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

var verbose = gnuflag.Bool("v", false, "Enables verbose mode.")
var debug = gnuflag.Bool("debug", false, "Enables debug mode.")
var listenAddr = gnuflag.String("tcp", "", "TCP address <addr:port> to listen on in addition to the unix socket (e.g., 127.0.0.1:8443)")
var group = gnuflag.String("group", "", "Group which owns the shared socket")
var help = gnuflag.Bool("help", false, "Print this help message.")
var version = gnuflag.Bool("version", false, "Print LXD's version number and exit.")

func init() {
	myGroup, err := shared.GroupName(os.Getgid())
	if err != nil {
		shared.Debugf("Problem finding default group %s", err)
	}
	*group = myGroup

	rand.Seed(time.Now().UTC().UnixNano())
}

func run() error {
	gnuflag.Usage = func() {
		fmt.Printf("Usage: lxd [options]\n\nOptions:\n")
		gnuflag.PrintDefaults()
	}

	gnuflag.Parse(true)
	if *help {
		// The user asked for help via --help, so we shouldn't print to
		// stderr.
		gnuflag.SetOut(os.Stdout)
		gnuflag.Usage()
		return nil
	}

	if *version {
		fmt.Println(shared.Version)
		return nil
	}

	if *verbose || *debug {
		shared.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
		shared.SetDebug(*debug)
	}

	d, err := StartDaemon(*listenAddr)

	if err != nil {
		if d != nil && d.db != nil {
			d.db.Close()
		}
		return err
	}

	defer d.db.Close()

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT)
	signal.Notify(ch, syscall.SIGTERM)
	<-ch
	return d.Stop()
}
