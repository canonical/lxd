package device

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"

	deviceConfig "github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/instance"
	"github.com/lxc/lxd/lxd/instance/instancetype"
	"github.com/lxc/lxd/lxd/revert"
	"github.com/lxc/lxd/lxd/util"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/subprocess"
	"github.com/lxc/lxd/shared/validate"
)

type tpm struct {
	deviceCommon
}

// validateConfig checks the supplied config for correctness.
func (d *tpm) validateConfig(instConf instance.ConfigReader) error {
	if !instanceSupported(instConf.Type(), instancetype.Container, instancetype.VM) {
		return ErrUnsupportedDevType
	}

	rules := map[string]func(string) error{
		"path": validate.IsNotEmpty,
	}

	err := d.config.Validate(rules)
	if err != nil {
		return errors.Wrapf(err, "Failed to validate config")
	}

	return nil
}

// validateEnvironment checks if the TPM emulator is available.
func (d *tpm) validateEnvironment() error {
	// Validate the required binary.
	_, err := exec.LookPath("swtpm")
	if err != nil {
		return fmt.Errorf("Required tool '%s' is missing", "swtpm")
	}

	if d.inst.Type() == instancetype.Container {
		// Load module tpm_vtpm_proxy which creates the /dev/vtpmx device, required
		// by the TPM emulator.
		module := "tpm_vtpm_proxy"

		err := util.LoadModule(module)
		if err != nil {
			return errors.Wrapf(err, "Failed to load kernel module %q", module)
		}
	}

	return nil
}

// Start is run when the device is added to the instance.
func (d *tpm) Start() (*deviceConfig.RunConfig, error) {
	err := d.validateEnvironment()
	if err != nil {
		return nil, errors.Wrap(err, "Failed to validate environment")
	}

	tpmDevPath := filepath.Join(d.inst.Path(), fmt.Sprintf("tpm.%s", d.name))

	if !shared.PathExists(tpmDevPath) {
		err := os.Mkdir(tpmDevPath, 0700)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to create device path %q", tpmDevPath)
		}
	}

	if d.inst.Type() == instancetype.VM {
		return d.startVM()
	}

	return d.startContainer()
}

func (d *tpm) startContainer() (*deviceConfig.RunConfig, error) {
	tpmDevPath := filepath.Join(d.inst.Path(), fmt.Sprintf("tpm.%s", d.name))
	logFileName := fmt.Sprintf("tpm.%s.log", d.name)
	logPath := filepath.Join(d.inst.LogPath(), logFileName)

	proc, err := subprocess.NewProcess("swtpm", []string{"chardev", "--tpm2", "--tpmstate", fmt.Sprintf("dir=%s", tpmDevPath), "--vtpm-proxy"}, logPath, "")
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to create new process")
	}

	err = proc.Start()
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to start process %q", "swtpm")
	}

	revert := revert.New()
	defer revert.Fail()

	// Stop the TPM emulator if anything goes wrong.
	revert.Add(func() { proc.Stop() })

	pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.pid", d.name))

	err = proc.Save(pidPath)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to save swtpm state for device %q", d.name)
	}

	var major, minor int

	// We need to capture the output of the TPM emulator since it contains the device path. To do
	// that, we wait until something has been written to the log file (stdout redirect), and then
	// read it.
	for i := 0; i < 20; i++ {
		fi, err := os.Stat(logPath)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to stat %q", logPath)
		}

		if fi.Size() > 0 {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	line, err := ioutil.ReadFile(logPath)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to read %q", logPath)
	}

	// The output will be something like:
	//   New TPM device: /dev/tpm1 (major/minor = 253/1)
	// We just need the major/minor numbers.
	fields := strings.Split(string(line), " ")

	if len(fields) < 7 {
		return nil, fmt.Errorf("Failed to get TPM device information")
	}

	_, err = fmt.Sscanf(fields[6], "%d/%d)", &major, &minor)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to retrieve major/minor number")
	}

	// Return error as we were unable to retrieve information regarding the TPM device.
	if major == 0 && minor == 0 {
		return nil, fmt.Errorf("Failed to get TPM device information")
	}

	runConf := deviceConfig.RunConfig{}

	err = unixDeviceSetupCharNum(d.state, d.inst.DevicesPath(), "unix", d.name, d.config, uint32(major), uint32(minor), d.config["path"], false, &runConf)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to setup unix device")
	}

	revert.Success()

	return &runConf, nil
}

func (d *tpm) startVM() (*deviceConfig.RunConfig, error) {
	tpmDevPath := filepath.Join(d.inst.Path(), fmt.Sprintf("tpm.%s", d.name))
	socketPath := filepath.Join(tpmDevPath, fmt.Sprintf("swtpm-%s.sock", d.name))
	runConf := deviceConfig.RunConfig{
		TPMDevice: []deviceConfig.RunConfigItem{
			{Key: "devName", Value: d.name},
			{Key: "path", Value: socketPath},
		},
	}

	proc, err := subprocess.NewProcess("swtpm", []string{"socket", "--tpm2", "--tpmstate", fmt.Sprintf("dir=%s", tpmDevPath), "--ctrl", fmt.Sprintf("type=unixio,path=%s", socketPath)}, "", "")
	if err != nil {
		return nil, err
	}

	// Start the TPM emulator.
	err = proc.Start()
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to start swtpm for device %q", d.name)
	}

	revert := revert.New()
	defer revert.Fail()

	revert.Add(func() { proc.Stop() })

	pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.pid", d.name))

	err = proc.Save(pidPath)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to save swtpm state for device %q", d.name)
	}

	revert.Success()

	return &runConf, nil
}

// Stop terminates the TPM emulator.
func (d *tpm) Stop() (*deviceConfig.RunConfig, error) {
	pidPath := filepath.Join(d.inst.DevicesPath(), fmt.Sprintf("%s.pid", d.name))
	runConf := deviceConfig.RunConfig{}

	defer os.Remove(pidPath)

	if shared.PathExists(pidPath) {
		proc, err := subprocess.ImportProcess(pidPath)
		if err != nil {
			return nil, errors.Wrapf(err, "Failed to import process %q", pidPath)
		}

		// The TPM emulator will usually exit automatically when the tpm device is no longer in use,
		// i.e. the instance is stopped. Therefore, we only fail if the running process couldn't
		// be stopped.
		err = proc.Stop()
		if err != nil && err != subprocess.ErrNotRunning {
			return nil, errors.Wrapf(err, "Failed to stop imported process %q", pidPath)
		}
	}

	if d.inst.Type() == instancetype.Container {
		err := unixDeviceRemove(d.inst.DevicesPath(), "unix", d.name, "", &runConf)
		if err != nil {
			return nil, errors.Wrap(err, "Failed to remove unix device")
		}
	}

	return &runConf, nil
}

// Remove removes the TPM state file.
func (d *tpm) Remove() error {
	tpmDevPath := filepath.Join(d.inst.Path(), fmt.Sprintf("tpm.%s", d.name))

	return os.RemoveAll(tpmDevPath)
}
