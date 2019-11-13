package main

import (
	"math/rand"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/lxc/lxd/lxd/daemon"
	"github.com/lxc/lxd/lxd/events"
	"github.com/lxc/lxd/lxd/operations"
	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared/logger"
	"github.com/lxc/lxd/shared/logging"
	"github.com/lxc/lxd/shared/version"
)

// Initialize the random number generator
func init() {
	rand.Seed(time.Now().UTC().UnixNano())
}

type cmdGlobal struct {
	cmd *cobra.Command

	flagHelp    bool
	flagVersion bool

	flagLogFile    string
	flagLogDebug   bool
	flagLogSyslog  bool
	flagLogTrace   []string
	flagLogVerbose bool
}

func (c *cmdGlobal) Run(cmd *cobra.Command, args []string) error {
	// Set logging global variables
	daemon.Debug = c.flagLogDebug
	daemon.Verbose = c.flagLogVerbose

	// Set debug for the operations package
	operations.Init(daemon.Debug)

	// Set debug for the response package
	response.Init(daemon.Verbose)

	// Setup logger
	syslog := ""
	if c.flagLogSyslog {
		syslog = "lxd"
	}

	log, err := logging.GetLogger(syslog, c.flagLogFile, c.flagLogVerbose, c.flagLogDebug, events.NewEventHandler())
	if err != nil {
		return err
	}
	logger.Log = log

	return nil
}

func main() {
	// daemon command (main)
	daemonCmd := cmdDaemon{}
	app := daemonCmd.Command()
	app.SilenceUsage = true

	// Workaround for main command
	app.Args = cobra.ArbitraryArgs

	// Global flags
	globalCmd := cmdGlobal{cmd: app}
	daemonCmd.global = &globalCmd
	app.PersistentPreRunE = globalCmd.Run
	app.PersistentFlags().BoolVar(&globalCmd.flagVersion, "version", false, "Print version number")
	app.PersistentFlags().BoolVarP(&globalCmd.flagHelp, "help", "h", false, "Print help")
	app.PersistentFlags().StringVar(&globalCmd.flagLogFile, "logfile", "", "Path to the log file"+"``")
	app.PersistentFlags().BoolVar(&globalCmd.flagLogSyslog, "syslog", false, "Log to syslog")
	app.PersistentFlags().StringArrayVar(&globalCmd.flagLogTrace, "trace", []string{}, "Log tracing targets"+"``")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogDebug, "debug", "d", false, "Show all debug messages")
	app.PersistentFlags().BoolVarP(&globalCmd.flagLogVerbose, "verbose", "v", false, "Show all information messages")

	// Version handling
	app.SetVersionTemplate("{{.Version}}\n")
	app.Version = version.Version

	// activateifneeded sub-command
	activateifneededCmd := cmdActivateifneeded{global: &globalCmd}
	app.AddCommand(activateifneededCmd.Command())

	// callhook sub-command
	callhookCmd := cmdCallhook{global: &globalCmd}
	app.AddCommand(callhookCmd.Command())

	// forkconsole sub-command
	forkconsoleCmd := cmdForkconsole{global: &globalCmd}
	app.AddCommand(forkconsoleCmd.Command())

	// forkdns sub-command
	forkDNSCmd := cmdForkDNS{global: &globalCmd}
	app.AddCommand(forkDNSCmd.Command())

	// forkexec sub-command
	forkexecCmd := cmdForkexec{global: &globalCmd}
	app.AddCommand(forkexecCmd.Command())

	// forkfile sub-command
	forkfileCmd := cmdForkfile{global: &globalCmd}
	app.AddCommand(forkfileCmd.Command())

	// forkmigrate sub-command
	forkmigrateCmd := cmdForkmigrate{global: &globalCmd}
	app.AddCommand(forkmigrateCmd.Command())

	// forksyscall sub-command
	forksyscallCmd := cmdForksyscall{global: &globalCmd}
	app.AddCommand(forksyscallCmd.Command())

	// forkmount sub-command
	forkmountCmd := cmdForkmount{global: &globalCmd}
	app.AddCommand(forkmountCmd.Command())

	// forknet sub-command
	forknetCmd := cmdForknet{global: &globalCmd}
	app.AddCommand(forknetCmd.Command())

	// forkproxy sub-command
	forkproxyCmd := cmdForkproxy{global: &globalCmd}
	app.AddCommand(forkproxyCmd.Command())

	// forkstart sub-command
	forkstartCmd := cmdForkstart{global: &globalCmd}
	app.AddCommand(forkstartCmd.Command())

	// forkuevent sub-command
	forkueventCmd := cmdForkuevent{global: &globalCmd}
	app.AddCommand(forkueventCmd.Command())

	// forkzfs sub-command
	forkzfsCmd := cmdForkZFS{global: &globalCmd}
	app.AddCommand(forkzfsCmd.Command())

	// import sub-command
	importCmd := cmdImport{global: &globalCmd}
	app.AddCommand(importCmd.Command())

	// init sub-command
	initCmd := cmdInit{global: &globalCmd}
	app.AddCommand(initCmd.Command())

	// manpage sub-command
	manpageCmd := cmdManpage{global: &globalCmd}
	app.AddCommand(manpageCmd.Command())

	// migratedumpsuccess sub-command
	migratedumpsuccessCmd := cmdMigratedumpsuccess{global: &globalCmd}
	app.AddCommand(migratedumpsuccessCmd.Command())

	// netcat sub-command
	netcatCmd := cmdNetcat{global: &globalCmd}
	app.AddCommand(netcatCmd.Command())

	// shutdown sub-command
	shutdownCmd := cmdShutdown{global: &globalCmd}
	app.AddCommand(shutdownCmd.Command())

	// sql sub-command
	sqlCmd := cmdSql{global: &globalCmd}
	app.AddCommand(sqlCmd.Command())

	// version sub-command
	versionCmd := cmdVersion{global: &globalCmd}
	app.AddCommand(versionCmd.Command())

	// waitready sub-command
	waitreadyCmd := cmdWaitready{global: &globalCmd}
	app.AddCommand(waitreadyCmd.Command())

	// cluster sub-command
	clusterCmd := cmdCluster{global: &globalCmd}
	app.AddCommand(clusterCmd.Command())

	// Run the main command and handle errors
	err := app.Execute()
	if err != nil {
		os.Exit(1)
	}
}
