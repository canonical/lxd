package main

import (
	"net/http"

	"github.com/lxc/lxd/shared"
)

func listGet(d *Daemon, r *http.Request) Response {
	shared.Debugf("responding to list")

	var result []string

	rows, err := d.db.Query("SELECT name FROM containers")
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
