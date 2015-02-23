package main

import (
	"net/http"

	"database/sql"

	"github.com/lxc/lxd/shared"
	_ "github.com/mattn/go-sqlite3"
)

func listGet(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to list")

	var result []string
	certdbname := shared.VarPath("lxd.db")
	db, err := sql.Open("sqlite3", certdbname)
	if err != nil {
		shared.Debugf("Error opening lxd database: %s\n", err)
		return nil
	}
	defer db.Close()

	rows, err := db.Query("SELECT name FROM containers")
	if err != nil {
		shared.Debugf("Error reading containers from database: %s\n", err)
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		rows.Scan(&name)
		result = append(result, name)
	}

	return SyncResponse(true, result)
}

var listCmd = Command{name: "list", get: listGet}
