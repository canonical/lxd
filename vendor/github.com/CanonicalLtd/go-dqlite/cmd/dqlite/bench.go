package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/CanonicalLtd/go-dqlite"
	"github.com/CanonicalLtd/raft-test"
	"github.com/hashicorp/raft"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

// Return a new bench command.
func newBench() *cobra.Command {
	bench := &cobra.Command{
		Use:   "bench [address]",
		Short: "Bench all raft logs after the given index (included).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			address := args[0]
			role := args[1]

			if role == "server" {
				return runServer(address)
			}

			return runClient(address)
		},
	}

	return bench
}

func runServer(address string) error {
	registry := dqlite.NewRegistry("0")
	fsm := dqlite.NewFSM(registry)

	t := &testing.T{}
	r, cleanup := rafttest.Server(t, fsm, rafttest.Transport(func(i int) raft.Transport {
		_, transport := raft.NewInmemTransport(raft.ServerAddress(address))
		return transport
	}))
	defer cleanup()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return errors.Wrap(err, "failed to listen")
	}

	server, err := dqlite.NewServer(r, registry, listener)
	if err != nil {
		return errors.Wrap(err, "failed to create server")
	}

	time.Sleep(time.Minute)

	return server.Close()
}

func runClient(address string) error {
	store, err := dqlite.DefaultServerStore(":memory:")
	if err != nil {
		return errors.Wrap(err, "failed to create server store")
	}

	if err := store.Set(context.Background(), []dqlite.ServerInfo{{Address: address}}); err != nil {
		return errors.Wrap(err, "failed to set server address")
	}

	driver, err := dqlite.NewDriver(store)
	if err != nil {
		return errors.Wrap(err, "failed to create dqlite driver")
	}

	sql.Register("dqlite", driver)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	db, err := sql.Open("dqlite", "test.db")
	if err != nil {
		return errors.Wrap(err, "failed to open database")
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return errors.Wrap(err, "failed to begin transaction")
	}

	start := time.Now()

	if _, err := tx.ExecContext(ctx, "CREATE TABLE test (n INT, t TEXT)"); err != nil {
		return errors.Wrapf(err, "failed to create test table")
	}

	for i := 0; i < 100; i++ {
		if _, err := tx.ExecContext(ctx, "INSERT INTO test(n,t) VALUES(?, ?)", int64(i), "hello"); err != nil {
			return errors.Wrapf(err, "failed to insert test value")
		}
	}

	rows, err := tx.QueryContext(ctx, "SELECT n FROM test")
	if err != nil {
		return errors.Wrapf(err, "failed to query test table")
	}

	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return errors.Wrap(err, "failed to scan row")
		}
	}
	if err := rows.Err(); err != nil {
		return errors.Wrap(err, "result set failure")
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "failed to commit transaction")
	}

	fmt.Printf("time %s\n", time.Since(start))

	return nil
}
