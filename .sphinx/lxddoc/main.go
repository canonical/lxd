package main

import (
	"errors"
	"flag"
	"log"
	"strings"
)

type flagStrings struct {
	Map   map[string]bool
	Slice *[]string
}

func newFlagStrings() flagStrings {
	return flagStrings{
		Map:   make(map[string]bool),
		Slice: &[]string{},
	}
}

// String implements the flag.Value interface
func (f flagStrings) String() string {
	if f.Slice == nil {
		return ""
	}
	return strings.Join(*f.Slice, ",")
}

// Set implements the flag.Value interface
func (f flagStrings) Set(i string) error {
	if _, ok := f.Map[i]; ok {
		return nil
	}
	f.Map[i] = true
	*f.Slice = append(*f.Slice, i)
	return nil
}

// Flags
var (
	templateFolder = flag.String("t", "", "Path to the template folder")
	exclude        = newFlagStrings()
)

func main() {
	// Parse flags
	flag.Var(&exclude, "e", "Path that will be excluded from the process")
	flag.Parse()

	comments := make(map[DocCode]*Comment)
	var err error
	for _, path := range flag.Args() {
		newComments, err := Extract(path, *exclude.Slice...)
		if err != nil {
			log.Fatal(err)
		}

		// Merge comments
		for k, v := range newComments {
			comments[k] = v
		}
	}

	if *templateFolder == "" {
		log.Fatal(errors.New("template folder not specified"))
	}

	err = InsertTemplate(*templateFolder, comments)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("lxddoc finished successfully")
}
