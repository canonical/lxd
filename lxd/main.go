package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/lxc/lxd"
	"github.com/lxc/lxd/internal/gnuflag"
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
var group = gnuflag.String("group", "", "Group which owns the lxd socket")

func init() {
	myGroup, err := lxd.GroupName(os.Getgid())
	if err != nil {
		lxd.Debugf("Problem finding default group %s", err)
	}
	*group = myGroup
}

func run() error {
	gnuflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: lxd [options]\n\nOptions:\n\n")
		gnuflag.PrintDefaults()
	}

	gnuflag.Parse(true)

	if *verbose || *debug {
		lxd.SetLogger(log.New(os.Stderr, "", log.LstdFlags))
		lxd.SetDebug(*debug)
	}

	d, err := StartDaemon(*listenAddr)
	if err != nil {
		return err
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT)
	signal.Notify(ch, syscall.SIGTERM)
	<-ch
	return d.Stop()
}
