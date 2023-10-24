package main

import (
	"flag"
	"log"
	"os"
)

type flags struct {
	Endpoint  string
	CredsFile string
}

func main() {
	flags := parseFlags()
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime)
	s := newAuthService(flags.Endpoint, logger)

	err := s.Checker.LoadCreds(flags.CredsFile)
	if err != nil {
		panic(err)
	}

	err = s.Start(false)
	if err != nil {
		panic(err)
	}
}

func parseFlags() *flags {
	endpoint := flag.String("endpoint", "localhost:8081", "service endpoint")
	credsFile := flag.String("creds", "credentials.csv", "CSV file with credentials (username and password)")
	flag.Parse()
	f := &flags{
		Endpoint:  *endpoint,
		CredsFile: *credsFile,
	}

	return f
}
