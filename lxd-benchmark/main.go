package main

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/lxd-benchmark/benchmark"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/version"
)

type cmdGlobal struct {
	flagHelp        bool
	flagParallel    int
	flagProject     string
	flagReportFile  string
	flagReportLabel string
	flagVersion     bool

	srv            lxd.ContainerServer
	report         *benchmark.CSVReport
	reportDuration time.Duration
}

// Establishes LXD connection, prints server info, and prepares report handling.
func (c *cmdGlobal) Run(cmd *cobra.Command, args []string) error {
	// Connect to LXD
	srv, err := lxd.ConnectLXDUnix("", nil)
	if err != nil {
		return err
	}

	c.srv = srv.UseProject(c.flagProject)

	// Print the initial header
	err = benchmark.PrintServerInfo(srv)
	if err != nil {
		return err
	}

	// Setup report handling
	if c.flagReportFile != "" {
		c.report = &benchmark.CSVReport{Filename: c.flagReportFile}
		if shared.PathExists(c.flagReportFile) {
			err := c.report.Load()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Adds a report record and writes the report, if reporting is enabled.
func (c *cmdGlobal) Teardown(cmd *cobra.Command, args []string) error {
	// Nothing to do with not reporting
	if c.report == nil {
		return nil
	}

	label := cmd.Name()
	if c.flagReportLabel != "" {
		label = c.flagReportLabel
	}

	err := c.report.AddRecord(label, c.reportDuration)
	if err != nil {
		return err
	}

	err = c.report.Write()
	if err != nil {
		return err
	}

	return nil
}

// Executes application with command-line arguments.
func main() {
	app := &cobra.Command{}
	app.Use = "lxd-benchmark"
	app.Short = "Benchmark performance of LXD"
	app.Long = `Description:
  Benchmark performance of LXD

  This tool lets you benchmark various actions on a local LXD daemon.

  It can be used just to check how fast a given LXD host is, to
  compare performance on different servers or for performance tracking
  when doing changes to the LXD codebase.

  A CSV report can be produced to be consumed by graphing software.
`
	app.Example = `  # Spawn 20 Ubuntu containers in batches of 4
  lxd-benchmark launch --count 20 --parallel 4

  # Create 50 Alpine containers in batches of 10
  lxd-benchmark init --count 50 --parallel 10 images:alpine/edge

  # Delete all test containers using dynamic batch size
  lxd-benchmark delete`
	app.SilenceUsage = true
	app.CompletionOptions = cobra.CompletionOptions{DisableDefaultCmd: true}

	// Global flags
	globalCmd := cmdGlobal{}
	app.PersistentPreRunE = globalCmd.Run
	app.PersistentPostRunE = globalCmd.Teardown
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().IntVarP(&globalCmd.flagParallel, "parallel", "P", -1, "Number of threads to use"+"``")
	app.PersistentFlags().StringVar(&globalCmd.flagReportFile, "report-file", "", "Path to the CSV report file"+"``")
	app.PersistentFlags().StringVar(&globalCmd.flagReportLabel, "report-label", "", "Label for the new entry in the report [default=ACTION]"+"``")
	app.PersistentFlags().StringVar(&globalCmd.flagProject, "project", "default", "Project to use")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// init sub-command
	initCmd := cmdInit{global: &globalCmd}
	app.AddCommand(initCmd.Command())

	// launch sub-command
	launchCmd := cmdLaunch{global: &globalCmd, init: &initCmd}
	app.AddCommand(launchCmd.Command())

	// start sub-command
	startCmd := cmdStart{global: &globalCmd}
	app.AddCommand(startCmd.Command())

	// stop sub-command
	stopCmd := cmdStop{global: &globalCmd}
	app.AddCommand(stopCmd.Command())

	// delete sub-command
	deleteCmd := cmdDelete{global: &globalCmd}
	app.AddCommand(deleteCmd.Command())

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
