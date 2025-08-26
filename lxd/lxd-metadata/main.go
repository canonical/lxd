package main

import (
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"
)

var exclude []string
var jsonOutput string
var txtOutput string
var substitutionDBPath string
var rootCmd = &cobra.Command{
	Use:   "lxd-metadata",
	Short: "lxd-metadata - a simple tool to generate configuration metadata and documentation for LXD",
	Long:  "lxd-metadata - a simple tool to generate configuration metadata documentation for LXD. It outputs a JSON and a Markdown file that contain the content of all `lxdmeta:generate` statements in the project.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			log.Fatal("Please provide a path to the project") //nolint:revive
		}

		path := args[0]
		_, err := parse(path, jsonOutput, exclude, substitutionDBPath)
		if err != nil {
			log.Fatal(err) //nolint:revive
		}

		if txtOutput != "" {
			err = writeDocFile(jsonOutput, txtOutput)
			if err != nil {
				log.Fatal(err) //nolint:revive
			}
		}
	},
}

func main() {
	rootCmd.Flags().StringSliceVarP(&exclude, "exclude", "e", []string{}, "Path to exclude from the process")
	rootCmd.Flags().StringVarP(&jsonOutput, "json", "j", "configuration.json", "Output JSON file containing the generated configuration")
	rootCmd.Flags().StringVarP(&txtOutput, "txt", "t", "", "Output TXT file containing the generated documentation")
	rootCmd.Flags().StringVarP(&substitutionDBPath, "substitution-db", "s", "", "Path to a YAML file containing substitution rules")
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lxd-metadata failed: %v", err)
		os.Exit(1)
	}

	log.Println("lxd-metadata finished successfully")
}
