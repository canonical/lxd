package device

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/lxc/lxd/lxd/device/config"
	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/shared"
)

// SCIB Infiniband sys class path.
const SCIB = "/sys/class/infiniband"

// SCNET Net sys class path.
const SCNET = "/sys/class/net"

// IBDevPrefix Infiniband devices prefix.
const IBDevPrefix = "infiniband.unix"

// IBF structure representing Infiniband function config.
type IBF struct {
	// port the function belongs to.
	Port int64

	// name of the {physical,virtual} function.
	Fun string

	// whether this is a physical (true) or virtual (false) function.
	PF bool

	// device of the function.
	Device string

	// uverb device of the function.
	PerPortDevices []string
	PerFunDevices  []string
}

// infinibandLoadDevices inspects the system and returns information about all infiniband devices.
func infinibandLoadDevices() (map[string]IBF, error) {
	// check if there are any infiniband devices.
	fscib, err := os.Open(SCIB)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer fscib.Close()

	// eg.g. mlx_i for i = 0, 1, ..., n
	IBDevNames, err := fscib.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	if len(IBDevNames) == 0 {
		return nil, os.ErrNotExist
	}

	// Retrieve all network device names.
	fscnet, err := os.Open(SCNET)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	defer fscnet.Close()

	// Retrieve all network devices.
	NetDevNames, err := fscnet.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	if len(NetDevNames) == 0 {
		return nil, os.ErrNotExist
	}

	UseableDevices := make(map[string]IBF)
	for _, IBDevName := range IBDevNames {
		IBDevResourceFile := fmt.Sprintf("/sys/class/infiniband/%s/device/resource", IBDevName)
		IBDevResourceBuf, err := ioutil.ReadFile(IBDevResourceFile)
		if err != nil {
			return nil, err
		}

		for _, NetDevName := range NetDevNames {
			NetDevResourceFile := fmt.Sprintf("/sys/class/net/%s/device/resource", NetDevName)
			NetDevResourceBuf, err := ioutil.ReadFile(NetDevResourceFile)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			// If the device and the VF have the same address space
			// they belong together.
			if bytes.Compare(IBDevResourceBuf, NetDevResourceBuf) != 0 {
				continue
			}

			// Now let's find the ports.
			IBDevID := fmt.Sprintf("/sys/class/net/%s/dev_id", NetDevName)
			IBDevPort := fmt.Sprintf("/sys/class/net/%s/dev_port", NetDevName)
			DevIDBuf, err := ioutil.ReadFile(IBDevID)
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}

			DevIDString := strings.TrimSpace(string(DevIDBuf))
			DevIDPort, err := strconv.ParseInt(DevIDString, 0, 64)
			if err != nil {
				return nil, err
			}

			DevPort := int64(0)
			DevPortBuf, err := ioutil.ReadFile(IBDevPort)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				DevPortString := strings.TrimSpace(string(DevPortBuf))
				DevPort, err = strconv.ParseInt(DevPortString, 0, 64)
				if err != nil {
					return nil, err
				}
			}

			Port := DevIDPort
			if DevPort > DevIDPort {
				Port = DevPort
			}
			Port++

			NewIBF := IBF{
				Port:   Port,
				Fun:    IBDevName,
				Device: NetDevName,
			}

			// Identify the /dev/infiniband/uverb<idx> device.
			tmp := []string{}
			IBUverb := fmt.Sprintf("/sys/class/net/%s/device/infiniband_verbs", NetDevName)
			fuverb, err := os.Open(IBUverb)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				defer fuverb.Close()

				// Optional: retrieve all network devices.
				tmp, err = fuverb.Readdirnames(-1)
				if err != nil {
					return nil, err
				}

				if len(tmp) == 0 {
					return nil, os.ErrNotExist
				}
			}
			for _, v := range tmp {
				if strings.Index(v, "-") != -1 {
					return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", v)
				}
				NewIBF.PerPortDevices = append(NewIBF.PerPortDevices, v)
			}

			// Identify the /dev/infiniband/ucm<idx> device.
			tmp = []string{}
			IBcm := fmt.Sprintf("/sys/class/net/%s/device/infiniband_ucm", NetDevName)
			fcm, err := os.Open(IBcm)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				defer fcm.Close()

				// Optional: retrieve all network devices.
				tmp, err = fcm.Readdirnames(-1)
				if err != nil {
					return nil, err
				}

				if len(tmp) == 0 {
					return nil, os.ErrNotExist
				}
			}
			for _, v := range tmp {
				if strings.Index(v, "-") != -1 {
					return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", v)
				}
				devPath := fmt.Sprintf("/dev/infiniband/%s", v)
				NewIBF.PerPortDevices = append(NewIBF.PerPortDevices, devPath)
			}

			// Identify the /dev/infiniband/{issm,umad}<idx> devices.
			IBmad := fmt.Sprintf("/sys/class/net/%s/device/infiniband_mad", NetDevName)
			ents, err := ioutil.ReadDir(IBmad)
			if err != nil {
				if !os.IsNotExist(err) {
					return nil, err
				}
			} else {
				for _, ent := range ents {
					IBmadPort := fmt.Sprintf("%s/%s/port", IBmad, ent.Name())
					portBuf, err := ioutil.ReadFile(IBmadPort)
					if err != nil {
						if !os.IsNotExist(err) {
							return nil, err
						}
						continue
					}

					portStr := strings.TrimSpace(string(portBuf))
					PortMad, err := strconv.ParseInt(portStr, 0, 64)
					if err != nil {
						return nil, err
					}

					if PortMad != NewIBF.Port {
						continue
					}

					if strings.Index(ent.Name(), "-") != -1 {
						return nil, fmt.Errorf("Infiniband character device \"%s\" contains \"-\". Cannot guarantee unique encoding", ent.Name())
					}

					NewIBF.PerFunDevices = append(NewIBF.PerFunDevices, ent.Name())
				}
			}

			// Figure out whether this is a physical function.
			IBPF := fmt.Sprintf("/sys/class/net/%s/device/physfn", NetDevName)
			NewIBF.PF = !shared.PathExists(IBPF)

			UseableDevices[NetDevName] = NewIBF
		}
	}

	return UseableDevices, nil
}

// infinibandAddDevices creates the UNIX devices for the provided IBF device and then configures the
// supplied runConfig with the Cgroup rules and mount instructions to pass the device into instance.
func infinibandAddDevices(s *state.State, devicesPath string, deviceName string, ifDev *IBF, runConf *RunConfig) error {
	for _, unixCharDev := range ifDev.PerPortDevices {
		destPath := fmt.Sprintf("/dev/infiniband/%s", unixCharDev)
		dummyDevice := config.Device{
			"source": destPath,
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, dummyDevice, false, runConf)
		if err != nil {
			return err
		}
	}

	for _, unixCharDev := range ifDev.PerFunDevices {
		destPath := fmt.Sprintf("/dev/infiniband/%s", unixCharDev)
		dummyDevice := config.Device{
			"source": destPath,
		}

		err := unixDeviceSetup(s, devicesPath, IBDevPrefix, deviceName, dummyDevice, false, runConf)
		if err != nil {
			return err
		}
	}

	return nil
}
