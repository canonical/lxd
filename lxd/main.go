package main

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lxc/lxd/internal/gnuflag"
	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
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

func createDb(p string) error {
	db, err := sql.Open("sqlite3", p)
	if err != nil {
		return fmt.Errorf("Error creating database: %s\n", err)
	}
	defer db.Close()
	stmt := `
CREATE TABLE certificates (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    type INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    certificate TEXT NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE containers (
    id INTEGER primary key AUTOINCREMENT NOT NULL,
    name VARCHAR(255) NOT NULL,
    architecture INTEGER NOT NULL,
    type INTEGER NOT NULL,
    UNIQUE (name)
);
CREATE TABLE containers_config (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    container_id INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (container_id) REFERENCES containers (id),
    UNIQUE (container_id, key)
);
CREATE TABLE images (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    fingerprint VARCHAR(255) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    size INTEGER NOT NULL,
    public INTEGER NOT NULL DEFAULT 0,
    architecture INTEGER NOT NULL,
    creation_date DATETIME,
    expiry_date DATETIME,
    upload_date DATETIME NOT NULL,
    UNIQUE (fingerprint)
);
CREATE TABLE images_properties (
    id INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    image_id INTEGER NOT NULL,
    type INTEGER NOT NULL,
    key VARCHAR(255) NOT NULL,
    value TEXT,
    FOREIGN KEY (image_id) REFERENCES images (id)
);`

	_, err = db.Exec(stmt)
	return err
}

func initDb() error {
	dbpath := shared.VarPath("lxd.db")
	if !shared.PathExists(dbpath) {
		err := createDb(dbpath)
		if err != nil {
			return err
		}
		return nil
	}

	/* TODO - scheck schema and update if necessary */

	return nil
}

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

	err := initDb()
	if err != nil {
		return err
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
