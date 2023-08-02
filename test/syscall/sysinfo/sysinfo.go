package main

import (
	"fmt"
	"log"

	"golang.org/x/sys/unix"
)

// Using the unix.Sysinfo_t struct, the main function retrieves and prints system information.
func main() {
	var s unix.Sysinfo_t

	err := unix.Sysinfo(&s)
	if err != nil {
		log.Fatal(err)
		return
	}

	fmt.Printf("%+v\n", s)
}
