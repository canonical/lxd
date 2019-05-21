// Copyright 2017 Canonical Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dqlite

import (
	"context"
	"database/sql/driver"
	"io"
	"net"
	"reflect"
	"time"

	"github.com/Rican7/retry/backoff"
	"github.com/Rican7/retry/strategy"
	"github.com/pkg/errors"

	"github.com/CanonicalLtd/go-dqlite/internal/bindings"
	"github.com/CanonicalLtd/go-dqlite/internal/client"
	"github.com/CanonicalLtd/go-dqlite/internal/connection"
)

// Driver perform queries against a dqlite server.
type Driver struct {
	log               LogFunc         // Log function to use
	store             ServerStore     // Holds addresses of dqlite servers
	context           context.Context // Global cancellation context
	connectionTimeout time.Duration   // Max time to wait for a new connection
	contextTimeout    time.Duration   // Default client context timeout.
	clientConfig      client.Config   // Configuration for dqlite client instances
}

// DriverError is returned in case of database errors.
type DriverError = bindings.Error

// DriverOption can be used to tweak driver parameters.
type DriverOption func(*driverOptions)

// WithLogFunc sets a custom logging function.
func WithLogFunc(log LogFunc) DriverOption {
	return func(options *driverOptions) {
		options.Log = log
	}
}

// DialFunc is a function that can be used to establish a network connection.
type DialFunc client.DialFunc

// WithDialFunc sets a custom dial function.
func WithDialFunc(dial DialFunc) DriverOption {
	return func(options *driverOptions) {
		options.Dial = client.DialFunc(dial)
	}
}

// WithConnectionTimeout sets the connection timeout.
//
// If not used, the default is 5 seconds.
func WithConnectionTimeout(timeout time.Duration) DriverOption {
	return func(options *driverOptions) {
		options.ConnectionTimeout = timeout
	}
}

// WithContextTimeout sets the default client context timeout when no context
// deadline is provided.
//
// If not used, the default is 5 seconds.
func WithContextTimeout(timeout time.Duration) DriverOption {
	return func(options *driverOptions) {
		options.ContextTimeout = timeout
	}
}

// WithConnectionBackoffFactor sets the exponential backoff factor for retrying
// failed connection attempts.
//
// If not used, the default is 50 milliseconds.
func WithConnectionBackoffFactor(factor time.Duration) DriverOption {
	return func(options *driverOptions) {
		options.ConnectionBackoffFactor = factor
	}
}

// WithConnectionBackoffCap sets the maximum connection retry backoff value,
// (regardless of the backoff factor) for retrying failed connection attempts.
//
// If not used, the default is 1 second.
func WithConnectionBackoffCap(cap time.Duration) DriverOption {
	return func(options *driverOptions) {
		options.ConnectionBackoffCap = cap
	}
}

// WithContext sets a global cancellation context.
func WithContext(context context.Context) DriverOption {
	return func(options *driverOptions) {
		options.Context = context
	}
}

// NewDriver creates a new dqlite driver, which also implements the
// driver.Driver interface.
func NewDriver(store ServerStore, options ...DriverOption) (*Driver, error) {
	o := defaultDriverOptions()

	for _, option := range options {
		option(o)
	}

	driver := &Driver{
		log:               o.Log,
		store:             store,
		context:           o.Context,
		connectionTimeout: o.ConnectionTimeout,
		contextTimeout:    o.ContextTimeout,
	}

	driver.clientConfig.Dial = o.Dial
	driver.clientConfig.AttemptTimeout = 5 * time.Second
	driver.clientConfig.RetryStrategies = []strategy.Strategy{
		driverConnectionRetryStrategy(
			o.ConnectionBackoffFactor,
			o.ConnectionBackoffCap,
		),
	}

	return driver, nil
}

// Hold configuration options for a dqlite driver.
type driverOptions struct {
	Log                     LogFunc
	Dial                    client.DialFunc
	ConnectionTimeout       time.Duration
	ContextTimeout          time.Duration
	ConnectionBackoffFactor time.Duration
	ConnectionBackoffCap    time.Duration
	Context                 context.Context
}

// Create a driverOptions object with sane defaults.
func defaultDriverOptions() *driverOptions {
	return &driverOptions{
		Log:                     defaultLogFunc(),
		Dial:                    client.TCPDial,
		ConnectionTimeout:       15 * time.Second,
		ContextTimeout:          5 * time.Second,
		ConnectionBackoffFactor: 50 * time.Millisecond,
		ConnectionBackoffCap:    time.Second,
		Context:                 context.Background(),
	}
}

// Return a retry strategy with jittered exponential backoff, capped at the
// given amount of time.
func driverConnectionRetryStrategy(factor, cap time.Duration) strategy.Strategy {
	backoff := backoff.BinaryExponential(factor)

	return func(attempt uint) bool {
		if attempt > 0 {
			duration := backoff(attempt)
			if duration > cap {
				duration = cap
			}
			time.Sleep(duration)
		}

		return true
	}
}

// Open establishes a new connection to a SQLite database on the dqlite server.
//
// The given name must be a pure file name without any directory segment,
// dqlite will connect to a database with that name in its data directory.
//
// Query parameters are always valid except for "mode=memory".
//
// If this node is not the leader, or the leader is unknown an ErrNotLeader
// error is returned.
func (d *Driver) Open(uri string) (driver.Conn, error) {
	// Validate the given data source string.
	filename, flags, err := connection.ParseURI(uri)
	if err != nil {
		return nil, errors.Wrapf(err, "invalid URI %s", uri)
	}

	ctx, cancel := context.WithTimeout(d.context, d.connectionTimeout)
	defer cancel()

	// TODO: generate a client ID.
	connector := client.NewConnector(0, d.store, d.clientConfig, d.log)

	conn := &Conn{
		log:            d.log,
		contextTimeout: d.contextTimeout,
	}

	conn.client, err = connector.Connect(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create dqlite connection")
	}
	conn.client.SetContextTimeout(d.contextTimeout)

	conn.request.Init(4096)
	conn.response.Init(4096)

	defer conn.request.Reset()
	defer conn.response.Reset()

	client.EncodeOpen(&conn.request, filename, flags, "volatile")

	if err := conn.client.Call(ctx, &conn.request, &conn.response); err != nil {
		conn.client.Close()
		return nil, errors.Wrap(err, "failed to open database")
	}

	conn.id, err = client.DecodeDb(&conn.response)
	if err != nil {
		conn.client.Close()
		return nil, errors.Wrap(err, "failed to open database")
	}

	return conn, nil
}

// SetContextTimeout sets the default client timeout when no context deadline
// is provided.
func (d *Driver) SetContextTimeout(timeout time.Duration) {
	d.contextTimeout = timeout
}

// ErrNoAvailableLeader is returned as root cause of Open() if there's no
// leader available in the cluster.
var ErrNoAvailableLeader = client.ErrNoAvailableLeader

// Conn implements the sql.Conn interface.
type Conn struct {
	log            LogFunc
	client         *client.Client
	request        client.Message
	response       client.Message
	id             uint32 // Database ID.
	contextTimeout time.Duration
}

// PrepareContext returns a prepared statement, bound to this connection.
// context is for the preparation of the statement, it must not store the
// context within the statement itself.
func (c *Conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	defer c.request.Reset()
	defer c.response.Reset()

	stmt := &Stmt{
		client:   c.client,
		request:  &c.request,
		response: &c.response,
	}

	client.EncodePrepare(&c.request, uint64(c.id), query)

	if err := c.client.Call(ctx, &c.request, &c.response); err != nil {
		return nil, driverError(err)
	}

	var err error
	stmt.db, stmt.id, stmt.params, err = client.DecodeStmt(&c.response)
	if err != nil {
		return nil, driverError(err)
	}

	return stmt, nil
}

// Prepare returns a prepared statement, bound to this connection.
func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

// ExecContext is an optional interface that may be implemented by a Conn.
func (c *Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	defer c.request.Reset()
	defer c.response.Reset()

	client.EncodeExecSQL(&c.request, uint64(c.id), query, args)

	if err := c.client.Call(ctx, &c.request, &c.response); err != nil {
		return nil, driverError(err)
	}

	result, err := client.DecodeResult(&c.response)
	if err != nil {
		return nil, driverError(err)
	}

	return &Result{result: result}, nil
}

// Query is an optional interface that may be implemented by a Conn.
func (c *Conn) Query(query string, args []driver.Value) (driver.Rows, error) {
	return c.QueryContext(context.Background(), query, valuesToNamedValues(args))
}

// QueryContext is an optional interface that may be implemented by a Conn.
func (c *Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	defer c.request.Reset()

	client.EncodeQuerySQL(&c.request, uint64(c.id), query, args)

	if err := c.client.Call(ctx, &c.request, &c.response); err != nil {
		return nil, driverError(err)
	}

	rows, err := client.DecodeRows(&c.response)
	if err != nil {
		return nil, driverError(err)
	}

	return &Rows{ctx: ctx, request: &c.request, response: &c.response, client: c.client, rows: rows}, nil
}

// Exec is an optional interface that may be implemented by a Conn.
func (c *Conn) Exec(query string, args []driver.Value) (driver.Result, error) {
	return c.ExecContext(context.Background(), query, valuesToNamedValues(args))
}

// Close invalidates and potentially stops any current prepared statements and
// transactions, marking this connection as no longer in use.
//
// Because the sql package maintains a free pool of connections and only calls
// Close when there's a surplus of idle connections, it shouldn't be necessary
// for drivers to do their own connection caching.
func (c *Conn) Close() error {
	return c.client.Close()
}

// BeginTx starts and returns a new transaction.  If the context is canceled by
// the user the sql package will call Tx.Rollback before discarding and closing
// the connection.
//
// This must check opts.Isolation to determine if there is a set isolation
// level. If the driver does not support a non-default level and one is set or
// if there is a non-default isolation level that is not supported, an error
// must be returned.
//
// This must also check opts.ReadOnly to determine if the read-only value is
// true to either set the read-only transaction property if supported or return
// an error if it is not supported.
func (c *Conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if _, err := c.ExecContext(ctx, "BEGIN", nil); err != nil {
		return nil, driverError(err)
	}

	tx := &Tx{
		conn: c,
	}

	return tx, nil
}

// Begin starts and returns a new transaction.
//
// Deprecated: Drivers should implement ConnBeginTx instead (or additionally).
func (c *Conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

// Tx is a transaction.
type Tx struct {
	conn *Conn
}

// Commit the transaction.
func (tx *Tx) Commit() error {
	ctx, cancel := context.WithTimeout(context.Background(), tx.conn.contextTimeout)
	defer cancel()

	if _, err := tx.conn.ExecContext(ctx, "COMMIT", nil); err != nil {
		return driverError(err)
	}

	return nil
}

// Rollback the transaction.
func (tx *Tx) Rollback() error {
	ctx, cancel := context.WithTimeout(context.Background(), tx.conn.contextTimeout)
	defer cancel()

	if _, err := tx.conn.ExecContext(ctx, "ROLLBACK", nil); err != nil {
		return driverError(err)
	}

	return nil
}

// Stmt is a prepared statement. It is bound to a Conn and not
// used by multiple goroutines concurrently.
type Stmt struct {
	client   *client.Client
	request  *client.Message
	response *client.Message
	db       uint32
	id       uint32
	params   uint64
}

// Close closes the statement.
func (s *Stmt) Close() error {
	defer s.request.Reset()
	defer s.response.Reset()

	client.EncodeFinalize(s.request, s.db, s.id)

	ctx := context.Background()

	if err := s.client.Call(ctx, s.request, s.response); err != nil {
		return driverError(err)
	}

	if err := client.DecodeEmpty(s.response); err != nil {
		return driverError(err)
	}

	return nil
}

// NumInput returns the number of placeholder parameters.
func (s *Stmt) NumInput() int {
	return int(s.params)
}

// ExecContext executes a query that doesn't return rows, such
// as an INSERT or UPDATE.
//
// ExecContext must honor the context timeout and return when it is canceled.
func (s *Stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	defer s.request.Reset()
	defer s.response.Reset()

	client.EncodeExec(s.request, s.db, s.id, args)

	if err := s.client.Call(ctx, s.request, s.response); err != nil {
		return nil, driverError(err)
	}

	result, err := client.DecodeResult(s.response)
	if err != nil {
		return nil, driverError(err)
	}

	return &Result{result: result}, nil
}

// Exec executes a query that doesn't return rows, such
func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamedValues(args))
}

// QueryContext executes a query that may return rows, such as a
// SELECT.
//
// QueryContext must honor the context timeout and return when it is canceled.
func (s *Stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	defer s.request.Reset()

	client.EncodeQuery(s.request, s.db, s.id, args)

	if err := s.client.Call(ctx, s.request, s.response); err != nil {
		return nil, driverError(err)
	}

	rows, err := client.DecodeRows(s.response)
	if err != nil {
		return nil, driverError(err)
	}

	return &Rows{ctx: ctx, request: s.request, response: s.response, client: s.client, rows: rows}, nil
}

// Query executes a query that may return rows, such as a
func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamedValues(args))
}

// Result is the result of a query execution.
type Result struct {
	result client.Result
}

// LastInsertId returns the database's auto-generated ID
// after, for example, an INSERT into a table with primary
// key.
func (r *Result) LastInsertId() (int64, error) {
	return int64(r.result.LastInsertID), nil
}

// RowsAffected returns the number of rows affected by the
// query.
func (r *Result) RowsAffected() (int64, error) {
	return int64(r.result.RowsAffected), nil
}

// Rows is an iterator over an executed query's results.
type Rows struct {
	ctx      context.Context
	client   *client.Client
	request  *client.Message
	response *client.Message
	rows     client.Rows
	consumed bool
}

// Columns returns the names of the columns. The number of
// columns of the result is inferred from the length of the
// slice. If a particular column name isn't known, an empty
// string should be returned for that entry.
func (r *Rows) Columns() []string {
	return r.rows.Columns
}

// Close closes the rows iterator.
func (r *Rows) Close() error {
	r.rows.Close()

	// If we consumed the whole result set, there's nothing to do as
	// there's no pending response from the server.
	if r.consumed {
		return nil
	}

	r.rows.Close()

	// Let's issue an interrupt request and wait until we get an empty
	// response, signalling that the query was interrupted.
	if err := r.client.Interrupt(r.ctx, r.request, r.response); err != nil {
		return driverError(err)
	}

	return nil
}

// Next is called to populate the next row of data into
// the provided slice. The provided slice will be the same
// size as the Columns() are wide.
//
// Next should return io.EOF when there are no more rows.
func (r *Rows) Next(dest []driver.Value) error {
	err := r.rows.Next(dest)

	if err == client.ErrRowsPart {
		r.rows.Close()
		if err := r.client.More(r.ctx, r.response); err != nil {
			return driverError(err)
		}
		rows, err := client.DecodeRows(r.response)
		if err != nil {
			return driverError(err)
		}
		r.rows = rows
		return r.rows.Next(dest)
	}

	if err == io.EOF {
		r.consumed = true
	}

	return err
}

// ColumnTypeScanType implements RowsColumnTypeScanType.
func (r *Rows) ColumnTypeScanType(i int) reflect.Type {
	// column := sql.NewColumn(r.rows, i)

	// typ, err := r.client.ColumnTypeScanType(context.Background(), column)
	// if err != nil {
	// 	return nil
	// }

	// return typ.DriverType()
	return nil
}

// ColumnTypeDatabaseTypeName implements RowsColumnTypeDatabaseTypeName.
func (r *Rows) ColumnTypeDatabaseTypeName(i int) string {
	// column := sql.NewColumn(r.rows, i)

	// typeName, err := r.client.ColumnTypeDatabaseTypeName(context.Background(), column)
	// if err != nil {
	// 	return ""
	// }

	// return typeName.Value
	return ""
}

// Convert a driver.Value slice into a driver.NamedValue slice.
func valuesToNamedValues(args []driver.Value) []driver.NamedValue {
	namedValues := make([]driver.NamedValue, len(args))
	for i, value := range args {
		namedValues[i] = driver.NamedValue{
			Ordinal: i + 1,
			Value:   value,
		}
	}
	return namedValues
}

func driverError(err error) error {
	switch err := errors.Cause(err).(type) {
	case *net.OpError:
		return driver.ErrBadConn
	case client.ErrRequest:
		switch err.Code {
		case bindings.ErrIoErrNotLeader:
			fallthrough
		case bindings.ErrIoErrLeadershipLost:
			return driver.ErrBadConn
		default:
			return DriverError{
				Code:    int(err.Code),
				Message: err.Description,
			}
		}
	}
	return err
}

func init() {
	err := bindings.Init()
	if err != nil {
		panic(errors.Wrap(err, "failed to initialize dqlite"))
	}
}
