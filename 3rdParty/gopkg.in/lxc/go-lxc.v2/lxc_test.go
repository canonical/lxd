// Copyright © 2013, 2014, The Go-LXC Authors. All rights reserved.
// Use of this source code is governed by a LGPLv2.1
// license that can be found in the LICENSE file.

// +build linux,cgo

package lxc

import (
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	ContainerName             = "lorem"
	SnapshotName              = "snap0"
	ContainerRestoreName      = "ipsum"
	ContainerCloneName        = "consectetur"
	ContainerCloneOverlayName = "adipiscing"
	ContainerCloneAufsName    = "pellentesque"
)

func unprivileged() bool {
	if os.Geteuid() != 0 {
		return true
	}
	return false
}

func supported(moduleName string) bool {
	if _, err := os.Stat("/sys/module/" + moduleName); err != nil {
		return false
	}
	return true
}

func TestVersion(t *testing.T) {
	t.Logf("LXC version: %s", Version())
}

func TestDefaultConfigPath(t *testing.T) {
	if DefaultConfigPath() == "" {
		t.Errorf("DefaultConfigPath failed...")
	}
}

func TestSetConfigPath(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	currentPath := c.ConfigPath()
	if err := c.SetConfigPath("/tmp"); err != nil {
		t.Errorf(err.Error())
	}
	newPath := c.ConfigPath()

	if currentPath == newPath {
		t.Errorf("SetConfigPath failed...")
	}
}

func TestAcquire(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	Acquire(c)
	Release(c)
}

func TestConcurrentDefined_Negative(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.NumCPU())

	var wg sync.WaitGroup

	for i := 0; i <= 100; i++ {
		wg.Add(1)
		go func() {
			c, err := NewContainer(strconv.Itoa(rand.Intn(10)))
			if err != nil {
				t.Errorf(err.Error())
			}

			// sleep for a while to simulate some dummy work
			time.Sleep(time.Millisecond * time.Duration(rand.Intn(250)))

			if c.Defined() {
				t.Errorf("Defined_Negative failed...")
			}
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestDefined_Negative(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if c.Defined() {
		t.Errorf("Defined_Negative failed...")
	}
}

func TestExecute(t *testing.T) {
	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.Execute("/bin/true"); err != nil {
		t.Errorf(err.Error())
	}
}

func TestSetVerbosity(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	c.SetVerbosity(Quiet)
}

func TestCreate(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	options := DownloadTemplateOptions
	if !unprivileged() {
		options = BusyboxTemplateOptions
	}
	if err := c.Create(options); err != nil {
		t.Errorf(err.Error())
	}
}

func TestClone(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err = c.Clone(ContainerCloneName, DefaultCloneOptions); err != nil {
		t.Errorf(err.Error())
	}
}

func TestCloneUsingOverlayfs(t *testing.T) {
	if !supported("overlayfs") {
		t.Skip("skipping test as overlayfs support is missing.")
	}

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	err = c.Clone(ContainerCloneOverlayName, CloneOptions{
		Backend:  Overlayfs,
		KeepName: true,
		KeepMAC:  true,
		Snapshot: true,
	})
	if err != nil {
		t.Errorf(err.Error())
	}
}

func TestCloneUsingAufs(t *testing.T) {
	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	if !supported("aufs") {
		t.Skip("skipping test as aufs support is missing.")
	}

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	err = c.Clone(ContainerCloneAufsName, CloneOptions{
		Backend:  Aufs,
		KeepName: true,
		KeepMAC:  true,
		Snapshot: true,
	})
	if err != nil {
		t.Errorf(err.Error())
	}
}

func TestCreateSnapshot(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.CreateSnapshot(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestRestoreSnapshot(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	snapshot := Snapshot{Name: SnapshotName}
	if err := c.RestoreSnapshot(snapshot, ContainerRestoreName); err != nil {
		t.Errorf(err.Error())
	}
}

func TestConcurrentCreate(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.NumCPU())

	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	var wg sync.WaitGroup

	options := BusyboxTemplateOptions
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			c, err := NewContainer(strconv.Itoa(i))
			if err != nil {
				t.Errorf(err.Error())
			}

			// sleep for a while to simulate some dummy work
			time.Sleep(time.Millisecond * time.Duration(rand.Intn(250)))

			if err := c.Create(options); err != nil {
				t.Errorf(err.Error())
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestSnapshots(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.Snapshots(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestConcurrentStart(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.NumCPU())

	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			c, err := NewContainer(strconv.Itoa(i))
			if err != nil {
				t.Errorf(err.Error())
			}

			if err := c.Start(); err != nil {
				t.Errorf(err.Error())
			}

			c.Wait(RUNNING, 30*time.Second)
			if !c.Running() {
				t.Errorf("Starting the container failed...")
			}

			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestConfigFileName(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if c.ConfigFileName() == "" {
		t.Errorf("ConfigFileName failed...")
	}
}

func TestDefined_Positive(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if !c.Defined() {
		t.Errorf("Defined_Positive failed...")
	}
}

func TestConcurrentDefined_Positive(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.NumCPU())

	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	var wg sync.WaitGroup

	for i := 0; i <= 100; i++ {
		wg.Add(1)
		go func() {
			c, err := NewContainer(strconv.Itoa(rand.Intn(10)))
			if err != nil {
				t.Errorf(err.Error())
			}

			// sleep for a while to simulate some dummy work
			time.Sleep(time.Millisecond * time.Duration(rand.Intn(250)))

			if !c.Defined() {
				t.Errorf("Defined_Positive failed...")
			}
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestInitPid_Negative(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if c.InitPid() != -1 {
		t.Errorf("InitPid failed...")
	}
}

func TestStart(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Start(); err != nil {
		t.Errorf(err.Error())
	}

	c.Wait(RUNNING, 30*time.Second)
	if !c.Running() {
		t.Errorf("Starting the container failed...")
	}
}

func TestWaitIPAddresses(t *testing.T) {
	if !unprivileged() {
		t.Skip("skipping test in privileged mode.")
	}

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.WaitIPAddresses(30 * time.Second); err != nil {
		t.Errorf(err.Error())
	}
}

func TestControllable(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if !c.Controllable() {
		t.Errorf("Controling the container failed...")
	}
}

func TestContainerNames(t *testing.T) {
	if ContainerNames() == nil {
		t.Errorf("ContainerNames failed...")
	}
}

func TestDefinedContainerNames(t *testing.T) {
	if DefinedContainerNames() == nil {
		t.Errorf("DefinedContainerNames failed...")
	}
}

func TestActiveContainerNames(t *testing.T) {
	if ActiveContainerNames() == nil {
		t.Errorf("ActiveContainerNames failed...")
	}
}

func TestContainers(t *testing.T) {
	if Containers() == nil {
		t.Errorf("Containers failed...")
	}
}

func TestDefinedContainers(t *testing.T) {
	if DefinedContainers() == nil {
		t.Errorf("DefinedContainers failed...")
	}
}

func TestActiveContainers(t *testing.T) {
	if ActiveContainers() == nil {
		t.Errorf("ActiveContainers failed...")
	}
}

func TestRunning(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if !c.Running() {
		t.Errorf("Checking the container failed...")
	}
}

func TestWantDaemonize(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.WantDaemonize(false); err != nil || c.Daemonize() {
		t.Errorf("WantDaemonize failed...")
	}
}

func TestWantCloseAllFds(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.WantCloseAllFds(true); err != nil {
		t.Errorf("WantCloseAllFds failed...")
	}
}

func TestSetLogLevel(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.SetLogLevel(WARN); err != nil || c.LogLevel() != WARN {
		t.Errorf("SetLogLevel( failed...")
	}
}

func TestSetLogFile(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.SetLogFile("/tmp/" + ContainerName); err != nil || c.LogFile() != "/tmp/"+ContainerName {
		t.Errorf("SetLogFile failed...")
	}
}

func TestInitPid_Positive(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if c.InitPid() == -1 {
		t.Errorf("InitPid failed...")
	}
}

func TestName(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if c.Name() != ContainerName {
		t.Errorf("Name failed...")
	}
}

func TestFreeze(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Freeze(); err != nil {
		t.Errorf(err.Error())
	}

	c.Wait(FROZEN, 30*time.Second)
	if c.State() != FROZEN {
		t.Errorf("Freezing the container failed...")
	}
}

func TestUnfreeze(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Unfreeze(); err != nil {
		t.Errorf(err.Error())
	}

	c.Wait(RUNNING, 30*time.Second)
	if !c.Running() {
		t.Errorf("Unfreezing the container failed...")
	}
}

func TestLoadConfigFile(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.LoadConfigFile(c.ConfigFileName()); err != nil {
		t.Errorf(err.Error())
	}
}

func TestSaveConfigFile(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.SaveConfigFile(c.ConfigFileName()); err != nil {
		t.Errorf(err.Error())
	}
}

func TestConfigItem(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if c.ConfigItem("lxc.utsname")[0] != ContainerName {
		t.Errorf("ConfigItem failed...")
	}
}

func TestSetConfigItem(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.SetConfigItem("lxc.utsname", ContainerName); err != nil {
		t.Errorf(err.Error())
	}

	if c.ConfigItem("lxc.utsname")[0] != ContainerName {
		t.Errorf("ConfigItem failed...")
	}
}

func TestRunningConfigItem(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if c.RunningConfigItem("lxc.network.0.type") == nil {
		t.Errorf("RunningConfigItem failed...")
	}
}

func TestSetCgroupItem(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	maxMem := c.CgroupItem("memory.max_usage_in_bytes")[0]
	currentMem := c.CgroupItem("memory.limit_in_bytes")[0]
	if err := c.SetCgroupItem("memory.limit_in_bytes", maxMem); err != nil {
		t.Errorf(err.Error())
	}
	newMem := c.CgroupItem("memory.limit_in_bytes")[0]

	if newMem == currentMem {
		t.Errorf("SetCgroupItem failed...")
	}
}

func TestClearConfigItem(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.ClearConfigItem("lxc.cap.drop"); err != nil {
		t.Errorf(err.Error())
	}
	if c.ConfigItem("lxc.cap.drop")[0] != "" {
		t.Errorf("ClearConfigItem failed...")
	}
}

func TestConfigKeys(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	keys := strings.Join(c.ConfigKeys("lxc.network.0"), " ")
	if !strings.Contains(keys, "mtu") {
		t.Errorf("Keys failed...")
	}
}

func TestInterfaces(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.Interfaces(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestMemoryUsage(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.MemoryUsage(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestKernelMemoryUsage(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.KernelMemoryUsage(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestMemorySwapUsage(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.MemorySwapUsage(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestBlkioUsage(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.BlkioUsage(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestMemoryLimit(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.MemoryLimit(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestSoftMemoryLimit(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.SoftMemoryLimit(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestKernelMemoryLimit(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.KernelMemoryLimit(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestMemorySwapLimit(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.MemorySwapLimit(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestSetMemoryLimit(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	oldMemLimit, err := c.MemoryLimit()
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.SetMemoryLimit(oldMemLimit * 4); err != nil {
		t.Errorf(err.Error())
	}

	newMemLimit, err := c.MemoryLimit()
	if err != nil {
		t.Errorf(err.Error())
	}

	if newMemLimit != oldMemLimit*4 {
		t.Errorf("SetMemoryLimit failed")
	}
}

func TestSetSoftMemoryLimit(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	oldMemLimit, err := c.MemoryLimit()
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.SetSoftMemoryLimit(oldMemLimit * 4); err != nil {
		t.Errorf(err.Error())
	}

	newMemLimit, err := c.SoftMemoryLimit()
	if err != nil {
		t.Errorf(err.Error())
	}

	if newMemLimit != oldMemLimit*4 {
		t.Errorf("SetSoftMemoryLimit failed")
	}
}

func TestSetKernelMemoryLimit(t *testing.T) {
	t.Skip("skipping the test as it requires memory.kmem.limit_in_bytes to be set")

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	oldMemLimit, err := c.KernelMemoryLimit()
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.SetKernelMemoryLimit(oldMemLimit * 4); err != nil {
		t.Errorf(err.Error())
	}

	newMemLimit, err := c.KernelMemoryLimit()
	if err != nil {
		t.Errorf(err.Error())
	}
	if newMemLimit != oldMemLimit*4 {
		t.Errorf("SetKernelMemoryLimit failed")
	}
}

func TestSetMemorySwapLimit(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	oldMemorySwapLimit, err := c.MemorySwapLimit()
	if err != nil {
		t.Errorf(err.Error())
	}
	if err := c.SetMemorySwapLimit(oldMemorySwapLimit / 4); err != nil {
		t.Errorf(err.Error())
	}

	newMemorySwapLimit, err := c.MemorySwapLimit()
	if err != nil {
		t.Errorf(err.Error())
	}

	if newMemorySwapLimit != oldMemorySwapLimit/4 {
		t.Errorf("SetSwapLimit failed")
	}
}

func TestCPUTime(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.CPUTime(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestCPUTimePerCPU(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.CPUTimePerCPU(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestCPUStats(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.CPUStats(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestRunCommand(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	argsThree := []string{"/bin/sh", "-c", "exit 0"}
	ok, err := c.RunCommand(argsThree, DefaultAttachOptions)
	if err != nil {
		t.Errorf(err.Error())
	}
	if ok != true {
		t.Errorf("Expected success")
	}

	argsThree = []string{"/bin/sh", "-c", "exit 1"}
	ok, err = c.RunCommand(argsThree, DefaultAttachOptions)
	if err != nil {
		t.Errorf(err.Error())
	}
	if ok != false {
		t.Errorf("Expected failure")
	}
}

func TestCommandWithEnv(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	options := DefaultAttachOptions
	options.Env = []string{"FOO=BAR"}
	options.ClearEnv = true

	args := []string{"/bin/sh", "-c", "test $FOO = 'BAR'"}
	ok, err := c.RunCommand(args, options)
	if err != nil {
		t.Errorf(err.Error())
	}
	if ok != true {
		t.Errorf("Expected success")
	}
}

func TestCommandWithEnvToKeep(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	options := DefaultAttachOptions
	options.ClearEnv = true
	options.EnvToKeep = []string{"TERM"}

	args := []string{"/bin/sh", "-c", "test $TERM = 'xterm-256color'"}
	ok, err := c.RunCommand(args, DefaultAttachOptions)
	if err != nil {
		t.Errorf(err.Error())
	}
	if ok != true {
		t.Errorf("Expected success")
	}
}

func TestCommandWithCwd(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	options := DefaultAttachOptions
	options.Cwd = "/tmp"

	args := []string{"/bin/sh", "-c", "test `pwd` = /tmp"}
	ok, err := c.RunCommand(args, options)
	if err != nil {
		t.Errorf(err.Error())
	}
	if ok != true {
		t.Errorf("Expected success")
	}
}

func TestCommandWithUIDGID(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	options := DefaultAttachOptions
	options.UID = 1000
	options.GID = 1000

	args := []string{"/bin/sh", "-c", "test `id -u` = 1000 && test `id -g` = 1000"}
	ok, err := c.RunCommand(args, options)
	if err != nil {
		t.Errorf(err.Error())
	}
	if ok != true {
		t.Errorf("Expected success")
	}
}

func TestCommandWithArch(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	options := DefaultAttachOptions
	options.Arch = X86

	args := []string{"/bin/sh", "-c", "test `uname -m` = i686"}
	ok, err := c.RunCommand(args, options)
	if err != nil {
		t.Errorf(err.Error())
	}
	if ok != true {
		t.Errorf("Expected success")
	}
}

func TestConsoleFd(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.ConsoleFd(0); err != nil {
		t.Errorf(err.Error())
	}
}

func TestIPAddress(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if unprivileged() {
		time.Sleep(3 * time.Second)
	}

	if _, err := c.IPAddress("lo"); err != nil {
		t.Errorf(err.Error())
	}
}

func TestAddDeviceNode(t *testing.T) {
	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.AddDeviceNode("/dev/network_latency"); err != nil {
		t.Errorf(err.Error())
	}
}

func TestRemoveDeviceNode(t *testing.T) {
	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.RemoveDeviceNode("/dev/network_latency"); err != nil {
		t.Errorf(err.Error())
	}
}

func TestIPv4Addresses(t *testing.T) {
	if !unprivileged() {
		t.Skip("skipping test in privileged mode.")
	}

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.IPv4Addresses(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestIPv6Addresses(t *testing.T) {
	t.Skip("skipping test")

	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if _, err := c.IPv6Addresses(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestReboot(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Reboot(); err != nil {
		t.Errorf("Rebooting the container failed...")
	}
	c.Wait(RUNNING, 30*time.Second)
}

func TestConcurrentShutdown(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.NumCPU())

	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			c, err := NewContainer(strconv.Itoa(i))
			if err != nil {
				t.Errorf(err.Error())
			}

			if err := c.Shutdown(30 * time.Second); err != nil {
				t.Errorf(err.Error())
			}

			c.Wait(STOPPED, 30*time.Second)
			if c.Running() {
				t.Errorf("Shutting down the container failed...")
			}

			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestShutdown(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Shutdown(30 * time.Second); err != nil {
		t.Errorf(err.Error())
	}

	c.Wait(STOPPED, 30*time.Second)
	if c.Running() {
		t.Errorf("Shutting down the container failed...")
	}
}

func TestStop(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Start(); err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Stop(); err != nil {
		t.Errorf(err.Error())
	}

	c.Wait(STOPPED, 30*time.Second)
	if c.Running() {
		t.Errorf("Stopping the container failed...")
	}
}

func TestDestroySnapshot(t *testing.T) {
	c, err := NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	snapshot := Snapshot{Name: SnapshotName}
	if err := c.DestroySnapshot(snapshot); err != nil {
		t.Errorf(err.Error())
	}
}

func TestDestroy(t *testing.T) {
	if supported("overlayfs") {
		c, err := NewContainer(ContainerCloneOverlayName)
		if err != nil {
			t.Errorf(err.Error())
		}

		if err := c.Destroy(); err != nil {
			t.Errorf(err.Error())
		}
	}

	if !unprivileged() && supported("aufs") {
		c, err := NewContainer(ContainerCloneAufsName)
		if err != nil {
			t.Errorf(err.Error())
		}

		if err := c.Destroy(); err != nil {
			t.Errorf(err.Error())
		}
	}
	c, err := NewContainer(ContainerCloneName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Destroy(); err != nil {
		t.Errorf(err.Error())
	}

	c, err = NewContainer(ContainerRestoreName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Destroy(); err != nil {
		t.Errorf(err.Error())
	}

	c, err = NewContainer(ContainerName)
	if err != nil {
		t.Errorf(err.Error())
	}

	if err := c.Destroy(); err != nil {
		t.Errorf(err.Error())
	}
}

func TestConcurrentDestroy(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.NumCPU())

	if unprivileged() {
		t.Skip("skipping test in unprivileged mode.")
	}

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			c, err := NewContainer(strconv.Itoa(i))
			if err != nil {
				t.Errorf(err.Error())
			}

			// sleep for a while to simulate some dummy work
			time.Sleep(time.Millisecond * time.Duration(rand.Intn(250)))

			if err := c.Destroy(); err != nil {
				t.Errorf(err.Error())
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestBackendStore(t *testing.T) {
	var X struct {
		store BackendStore
	}

	if X.store.String() != "" {
		t.Error("zero value of BackendStore should be invalid")
	}
}

func TestState(t *testing.T) {
	var X struct {
		state State
	}

	if X.state.String() != "" {
		t.Error("zero value of State should be invalid")
	}
}
