package cluster

import (
	"database/sql"
	"fmt"
	"sync/atomic"

	"github.com/CanonicalLtd/go-grpc-sql"
)

// Open the cluster database object.
//
// The name argument is the name of the cluster database. It defaults to
// 'db.bin', but can be overwritten for testing.
//
// The dialer argument is a function that returns a gRPC dialer that can be
// used to connect to a database node using the gRPC SQL package.
func Open(name string, dialer grpcsql.Dialer) (*sql.DB, error) {
	driver := grpcsql.NewDriver(dialer)
	driverName := grpcSQLDriverName()
	sql.Register(driverName, driver)

	// Create the cluster db. This won't immediately establish any gRPC
	// connection, that will happen only when a db transaction is started
	// (see the database/sql connection pooling code for more details).
	if name == "" {
		name = "db.bin"
	}
	db, err := sql.Open(driverName, name)
	if err != nil {
		return nil, fmt.Errorf("cannot open cluster database: %v", err)
	}

	return db, nil
}

// Generate a new name for the grpcsql driver registration. We need it to be
// unique for testing, see below.
func grpcSQLDriverName() string {
	defer atomic.AddUint64(&grpcSQLDriverSerial, 1)
	return fmt.Sprintf("grpc-%d", grpcSQLDriverSerial)
}

// Monotonic serial number for registering new instances of grpcsql.Driver
// using the database/sql stdlib package. This is needed since there's no way
// to unregister drivers, and in unit tests more than one driver gets
// registered.
var grpcSQLDriverSerial uint64
