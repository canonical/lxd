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
var listenAddr = gnuflag.String("tcp", "", "TCP address to listen on in addition to the unix socket")

func run() error {
	gnuflag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s\n\nOptions:\n\n--tcp <addr:port>       Bind to addr:port.")
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
