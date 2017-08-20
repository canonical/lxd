package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lxc/lxd/shared/cmd"
)

// Check the default values of all command line arguments.
func TestParse_ArgsDefaults(t *testing.T) {
	context := cmd.NewMemoryContext(cmd.NewMemoryStreams(""))
	line := []string{"lxd"}
	args := &Args{}
	parser := cmd.NewParser(context, "")
	parser.Parse(line, args)

	assert.Equal(t, false, args.Auto)
	assert.Equal(t, false, args.Preseed)
	assert.Equal(t, "", args.CPUProfile)
	assert.Equal(t, false, args.Debug)
	assert.Equal(t, "", args.Group)
	assert.Equal(t, false, args.Help)
	assert.Equal(t, "", args.Logfile)
	assert.Equal(t, "", args.MemProfile)
	assert.Equal(t, "", args.NetworkAddress)
	assert.Equal(t, int64(-1), args.NetworkPort)
	assert.Equal(t, -1, args.PrintGoroutinesEvery)
	assert.Equal(t, "", args.StorageBackend)
	assert.Equal(t, "", args.StorageCreateDevice)
	assert.Equal(t, int64(-1), args.StorageCreateLoop)
	assert.Equal(t, "", args.StorageDataset)
	assert.Equal(t, false, args.Syslog)
	assert.Equal(t, -1, args.Timeout)
	assert.Equal(t, "", args.TrustPassword)
	assert.Equal(t, false, args.Verbose)
	assert.Equal(t, false, args.Version)
	assert.Equal(t, false, args.Force)
}

// Check that parsing the command line results in the correct attributes
// being set.
func TestParse_ArgsCustom(t *testing.T) {
	context := cmd.NewMemoryContext(cmd.NewMemoryStreams(""))
	line := []string{
		"lxd",
		"--auto",
		"--preseed",
		"--cpuprofile", "lxd.cpu",
		"--debug",
		"--group", "lxd",
		"--help",
		"--logfile", "lxd.log",
		"--memprofile", "lxd.mem",
		"--network-address", "127.0.0.1",
		"--network-port", "666",
		"--print-goroutines-every", "10",
		"--storage-backend", "btrfs",
		"--storage-create-device", "/dev/sda2",
		"--storage-create-loop", "8192",
		"--storage-pool", "default",
		"--syslog",
		"--timeout", "30",
		"--trust-password", "sekret",
		"--verbose",
		"--version",
		"--force",
	}
	args := &Args{}
	parser := cmd.NewParser(context, "")
	parser.Parse(line, args)

	assert.Equal(t, true, args.Auto)
	assert.Equal(t, true, args.Preseed)
	assert.Equal(t, "lxd.cpu", args.CPUProfile)
	assert.Equal(t, true, args.Debug)
	assert.Equal(t, "lxd", args.Group)
	assert.Equal(t, true, args.Help)
	assert.Equal(t, "lxd.log", args.Logfile)
	assert.Equal(t, "lxd.mem", args.MemProfile)
	assert.Equal(t, "127.0.0.1", args.NetworkAddress)
	assert.Equal(t, int64(666), args.NetworkPort)
	assert.Equal(t, 10, args.PrintGoroutinesEvery)
	assert.Equal(t, "btrfs", args.StorageBackend)
	assert.Equal(t, "/dev/sda2", args.StorageCreateDevice)
	assert.Equal(t, int64(8192), args.StorageCreateLoop)
	assert.Equal(t, "default", args.StorageDataset)
	assert.Equal(t, true, args.Syslog)
	assert.Equal(t, 30, args.Timeout)
	assert.Equal(t, "sekret", args.TrustPassword)
	assert.Equal(t, true, args.Verbose)
	assert.Equal(t, true, args.Version)
	assert.Equal(t, true, args.Force)
}
