package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	cli "github.com/canonical/lxd/shared/cmd"
)

type cmdSQL struct {
	global     *cmdGlobal
	flagFormat string
}

func (c *cmdSQL) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = "sql <local|global> <query>"
	cmd.Short = "Execute a SQL query against the LXD local or global database"
	cmd.Long = `Description:
  Execute a SQL query against the LXD local or global database

  The local database is specific to the LXD cluster member you target the
  command to, and contains member-specific data (such as the member network
  address).

  The global database is common to all LXD members in the cluster, and contains
  cluster-specific data (such as profiles, containers, etc).

  If you are running a non-clustered LXD instance, the same applies, as that
  instance is effectively a single-member cluster.

  If <query> is the special value "-", then the query is read from
  standard input.

  If <query> is the special value ".dump", the command returns a SQL text
  dump of the given database.

  If <query> is the special value ".schema", the command returns the SQL
  text schema of the given database.

  This internal command is mostly useful for debugging and disaster
  recovery. The LXD team will occasionally provide hotfixes to users as a
  set of database queries to fix some data inconsistency.

  This command targets the global LXD database and works in both local
  and cluster mode.
`
	cmd.RunE = c.run
	cmd.Hidden = true
	cmd.Flags().StringVarP(&c.flagFormat, "format", "f", cli.TableFormatSQLResult, `Format (sql|csv|json|table|yaml|compact) (default "sql")`)

	return cmd
}

func (c *cmdSQL) run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		_ = cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return errors.New("Missing required arguments")
	}

	database := args[0]
	query := args[1]

	if !slices.Contains([]string{"local", "global"}, database) {
		_ = cmd.Help()

		return errors.New("Invalid database type")
	}

	if query == "-" {
		// Read from stdin
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("Failed to read from stdin: %w", err)
		}

		query = string(bytes)
	}

	// Connect to LXD
	lxdArgs := lxd.ConnectionArgs{
		SkipGetServer: true,
	}

	d, err := lxd.ConnectLXDUnix("", &lxdArgs)
	if err != nil {
		return err
	}

	if query == ".dump" || query == ".schema" {
		url := "/internal/sql?database=" + database
		if query == ".schema" {
			url += "&schema=1"
		}

		response, _, err := d.RawQuery(http.MethodGet, url, nil, "")
		if err != nil {
			return fmt.Errorf("failed to request dump: %w", err)
		}

		dump := internalSQLDump{}
		err = json.Unmarshal(response.Metadata, &dump)
		if err != nil {
			return fmt.Errorf("failed to parse dump response: %w", err)
		}

		fmt.Print(dump.Text)
		return nil
	}

	data := internalSQLQuery{
		Database: database,
		Query:    query,
	}

	response, _, err := d.RawQuery(http.MethodPost, "/internal/sql", data, "")
	if err != nil {
		return err
	}

	batch := internalSQLBatch{}
	err = json.Unmarshal(response.Metadata, &batch)
	if err != nil {
		return err
	}

	for i, result := range batch.Results {
		if len(batch.Results) > 1 {
			fmt.Printf("=> Query %d:\n\n", i)
		}

		if result.Type == "select" {
			err = sqlPrintSelectResult(c.flagFormat, result)
			if err != nil {
				return err
			}
		} else {
			fmt.Printf("Rows affected: %d\n", result.RowsAffected)
		}

		if len(batch.Results) > 1 {
			fmt.Println("")
		}
	}
	return nil
}

func sqlPrintSelectResult(format string, result internalSQLResult) error {
	data := make([][]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		r := make([]string, 0, len(row))
		for _, col := range row {
			r = append(r, fmt.Sprint(col))
		}

		data = append(data, r)
	}

	return cli.RenderTable(format, result.Columns, data, result)
}
