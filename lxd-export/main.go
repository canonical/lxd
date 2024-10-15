package main

import (
	"fmt"
	"log"
	"os"

	"github.com/canonical/lxd/lxd-export/core"
	"github.com/spf13/cobra"
)

func main() {
	var srcClusterCert string
	var srcClusterKey string
	var srcClusterAddr string
	var srcClusterRemote string

	var dstClusterCert string
	var dstClusterKey string
	var dstClusterAddr string
	var dstClusterRemote string

	var bootstrap bool
	var tfDir string
	var outputHCLFileName string

	var autoPlan bool
	var autoApply bool
	var alignServers bool

	var verbose bool

	var rootCmd = &cobra.Command{
		Use:   "cluster-sync",
		Short: "Synchronize LXD clusters",
		Long:  `This is a 'best effort' tool to synchronize a source to a destination LXD clusters through the generation of a Terraform definition file.`,
		Run: func(cmd *cobra.Command, args []string) {
			if !bootstrap {
				if tfDir == "" {
					log.Fatal(fmt.Errorf("Terraform directory is required when not bootstrapping"))
				}
			}

			if dstClusterCert == "" || dstClusterKey == "" || dstClusterAddr == "" || dstClusterRemote == "" {
				// DST cluster is not provided, we are simply generating (or refreshing) the Terraform HCL file of a SRC cluster.
				log.Default().Println("Destination cluster is not provided, generating Terraform HCL file of the source cluster")
				if srcClusterRemote == "" {
					log.Fatal(fmt.Errorf("Source cluster remote is required when doing a "))
				}

				err := core.ClusterExport(
					srcClusterCert,
					srcClusterKey,
					srcClusterAddr,
					srcClusterRemote,
					bootstrap,
					tfDir,
					outputHCLFileName,
					verbose,
				)
				if err != nil {
					log.Fatal(err)
				}

				return
			}

			if autoApply && !autoPlan {
				log.Fatal(fmt.Errorf("Auto apply requires auto plan"))
			}

			// Else, we are cloning (or syncing) a SRC cluster to a DST cluster.
			err := core.ClusterSync(
				srcClusterCert,
				srcClusterKey,
				srcClusterAddr,
				srcClusterRemote,
				dstClusterCert,
				dstClusterKey,
				dstClusterAddr,
				dstClusterRemote,
				bootstrap,
				tfDir,
				outputHCLFileName,
				autoPlan,
				autoApply,
				alignServers,
				verbose,
			)
			if err != nil {
				log.Fatal(err)
			}
		},
	}

	// Define the flags
	rootCmd.Flags().StringVar(&srcClusterCert, "src-cluster-cert", "", ".crt file path for source cluster")
	rootCmd.Flags().StringVar(&srcClusterKey, "src-cluster-key", "", ".key file path for source cluster")
	rootCmd.Flags().StringVar(&srcClusterAddr, "src-cluster-addr", "", "HTTPS address of the source cluster")
	rootCmd.Flags().StringVar(&srcClusterRemote, "src-cluster-remote", "", "Name of the source cluster remote")

	rootCmd.Flags().StringVar(&dstClusterCert, "dst-cluster-cert", "", ".crt file path for destination cluster")
	rootCmd.Flags().StringVar(&dstClusterKey, "dst-cluster-key", "", ".key file path for destination cluster")
	rootCmd.Flags().StringVar(&dstClusterAddr, "dst-cluster-addr", "", "HTTPS address of the destination cluster")
	rootCmd.Flags().StringVar(&dstClusterRemote, "dst-cluster-remote", "", "Name of the destination cluster remote")

	rootCmd.Flags().BoolVar(&bootstrap, "bootstrap", false, "Bootstrap the synchronization process")
	rootCmd.Flags().StringVar(&tfDir, "tf-dir", "", "Terraform directory")
	rootCmd.Flags().StringVar(&outputHCLFileName, "out-hcl-filename", "", "Name of the generated Terraform HCL file")

	rootCmd.Flags().BoolVar(&autoPlan, "auto-plan", false, "Automatically plan the Terraform changes")
	rootCmd.Flags().BoolVar(&autoApply, "auto-apply", false, "Automatically apply the Terraform changes. Requires --auto-plan")
	rootCmd.Flags().BoolVar(&alignServers, "align-server-configurations", false, "Enforce server configurations and cluster members alignment")

	rootCmd.Flags().BoolVar(&verbose, "verbose", false, "Enable verbose output")

	// Mark required flags
	rootCmd.MarkFlagRequired("src-cluster-cert")
	rootCmd.MarkFlagRequired("src-cluster-key")
	rootCmd.MarkFlagRequired("src-cluster-addr")

	// Execute the command
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
