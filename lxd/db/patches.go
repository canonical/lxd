package db

import (
	"fmt"
)

func (n *Node) Patches() ([]string, error) {
	inargs := []interface{}{}
	outfmt := []interface{}{""}

	query := fmt.Sprintf("SELECT name FROM patches")
	result, err := queryScan(n.db, query, inargs, outfmt)
	if err != nil {
		return []string{}, err
	}

	response := []string{}
	for _, r := range result {
		response = append(response, r[0].(string))
	}

	return response, nil
}

func (n *Node) PatchesMarkApplied(patch string) error {
	stmt := `INSERT INTO patches (name, applied_at) VALUES (?, strftime("%s"));`
	_, err := n.db.Exec(stmt, patch)
	return err
}
