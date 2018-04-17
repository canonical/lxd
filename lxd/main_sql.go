package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"

	"github.com/lxc/lxd/client"
	"github.com/lxc/lxd/shared"
)

type cmdSql struct {
	global *cmdGlobal
}

func (c *cmdSql) Command() *cobra.Command {
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

  If <query> is the special value "dump", the the command returns a SQL text
  dump of the given database.

  This internal command is mostly useful for debugging and disaster
  recovery. The LXD team will occasionally provide hotfixes to users as a
  set of database queries to fix some data inconsistency.

  This command targets the global LXD database and works in both local
  and cluster mode.
`
	cmd.RunE = c.Run
	cmd.Hidden = true

	return cmd
}

func (c *cmdSql) Run(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		cmd.Help()

		if len(args) == 0 {
			return nil
		}

		return fmt.Errorf("Missing required arguments")
	}

	database := args[0]
	query := args[1]

	if !shared.StringInSlice(database, []string{"local", "global"}) {
		cmd.Help()

		return fmt.Errorf("Invalid database type")
	}

	if query == "-" {
		// Read from stdin
		bytes, err := ioutil.ReadAll(os.Stdin)
		if err != nil {
			return errors.Wrap(err, "Failed to read from stdin")
		}
		query = string(bytes)
	}

	// Connect to LXD
	d, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	if query == "dump" {
		url := fmt.Sprintf("/internal/sql?database=%s", database)
		response, _, err := d.RawQuery("GET", url, nil, "")
		if err != nil {
			return errors.Wrap(err, "failed to request dump")
		}
		dump := internalSQLDump{}
		err = json.Unmarshal(response.Metadata, &dump)
		if err != nil {
			return errors.Wrap(err, "failed to parse dump response")
		}
		fmt.Printf(dump.Text)
		return nil
	}

	data := internalSQLQuery{
		Database: database,
		Query:    query,
	}
	response, _, err := d.RawQuery("POST", "/internal/sql", data, "")
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
			sqlPrintSelectResult(result)
		} else {
			fmt.Printf("Rows affected: %d\n", result.RowsAffected)
		}
		if len(batch.Results) > 1 {
			fmt.Printf("\n")
		}
	}
	return nil
}

func sqlPrintSelectResult(result internalSQLResult) {
	// Print results in tabular format
	widths := make([]int, len(result.Columns))
	for i, column := range result.Columns {
		widths[i] = len(column)
	}
	for _, row := range result.Rows {
		for i, v := range row {
			width := 10
			switch v := v.(type) {
			case string:
				width = len(v)
			case int:
				width = 6
			case int64:
				width = 6
			case time.Time:
				width = 12
			}
			if width > widths[i] {
				widths[i] = width
			}
		}
	}
	format := "|"
	separator := "+"
	columns := make([]interface{}, len(result.Columns))
	for i, column := range result.Columns {
		format += " %-" + strconv.Itoa(widths[i]) + "v |"
		columns[i] = column
		separator += strings.Repeat("-", widths[i]+2) + "+"
	}
	format += "\n"
	separator += "\n"
	fmt.Printf(separator)
	fmt.Printf(fmt.Sprintf(format, columns...))
	fmt.Printf(separator)
	for _, row := range result.Rows {
		fmt.Printf(format, row...)
	}
	fmt.Printf(separator)
}
