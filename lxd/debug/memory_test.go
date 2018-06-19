package debug_test

import (
	"io/ioutil"
	"os"
	"syscall"
	"testing"
	"time"

	log "github.com/lxc/lxd/shared/log15"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lxc/lxd/lxd/debug"
	"github.com/lxc/lxd/shared/logging"
)

func TestMemory(t *testing.T) {
	// Create a logger that will block when emitting records.
	records := make(chan *log.Record)
	logger := log.New()
	logger.SetHandler(log.ChannelHandler(records))
	defer logging.SetLogger(logger)()

	// Create a temporary file.
	file, err := ioutil.TempFile("", "lxd-util-")
	assert.NoError(t, err)
	file.Close()
	defer os.Remove(file.Name())

	// Spawn the profiler and check that it dumps the memmory to the given
	// file and stops when we close the shutdown channel.
	stop, err := debug.Start(debug.Memory(file.Name()))
	require.NoError(t, err)
	syscall.Kill(os.Getpid(), syscall.SIGUSR1)
	record := logging.WaitRecord(records, time.Second)
	require.NotNil(t, record)
	assert.Equal(t, "Received 'user defined signal 1 signal', dumping memory", record.Msg)

	go stop()
	record = logging.WaitRecord(records, time.Second)
	require.NotNil(t, record)
	assert.Equal(t, "Shutdown memory profiler", record.Msg)

	// The memory dump actually exists on disk.
	_, err = os.Stat(file.Name())
	assert.NoError(t, err)
}

func TestMemory_EmptyFilename(t *testing.T) {
	stop, err := debug.Start(debug.Memory(""))
	require.NoError(t, err)
	stop()
}
