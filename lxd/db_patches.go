package main

import (
	"database/sql"
	"fmt"
)

func dbPatches(db *sql.DB) ([]string, error) {
	inargs := []interface{}{}
	outfmt := []interface{}{""}

	query := fmt.Sprintf("SELECT name FROM patches")
	result, err := dbQueryScan(db, query, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

func dbPatchesMarkApplied(db *sql.DB, patch string) error {
	stmt := `INSERT INTO patches (name, applied_at) VALUES (?, strftime("%s"));`
	_, err := db.Exec(stmt, patch)
	return err
}
