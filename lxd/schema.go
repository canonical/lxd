// +build never
//
// We use the tag 'never' because this utility shouldn't normally be built,
// unless you're running the 'update-schema' target of the Makefile.

package main

import (
	"log"

	"github.com/lxc/lxd/lxd/db"
)

// Entry point for the "schema" development utility, which updates the content
// of lxd/db/schema.go according to the current schema updates declared in
// updates.go in the same package.
func main() {
	err := db.UpdateSchemasDotGo()
	if err != nil {
		log.Fatal(err)
	}
}
