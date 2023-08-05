package main

import (
	"encoding/csv"
	"fmt"
	"os"
)

type credentialsChecker struct {
	creds map[string]string
}

// newCredentialsChecker returns a new instance of credentialsChecker.
func newCredentialsChecker() credentialsChecker {
	return credentialsChecker{creds: map[string]string{}}
}

// Check validates the credentials in the form map.
func (c *credentialsChecker) Check(form any) bool {
	m := form.(map[string]any)
	username := m["username"].(string)
	password := m["password"].(string)
	pass, ok := c.creds[username]
	return ok && pass == password
}

// AddCreds adds the provided credentials to the internal map.
func (c *credentialsChecker) AddCreds(creds map[string]string) {
	for user, pass := range creds {
		c.creds[user] = pass
	}
}

// LoadCreds loads credentials from a CSV file and adds them to the internal map.
func (c *credentialsChecker) LoadCreds(csvFile string) error {
	f, err := os.Open(csvFile)
	if err != nil {
		return err
	}

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return err
	}

	creds := map[string]string{}
	for i, row := range rows {
		if len(row) != 2 {
			return fmt.Errorf("invalid length on row %d", i+1)
		}

		creds[row[0]] = row[1]
	}

	c.AddCreds(creds)
	return nil
}
