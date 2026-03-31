package connectors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/storage/block"
	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/revert"
)

type connectorNVMe struct {
	common
}

func newConnectorNVMe(serverUUID string) (Connector, error) {
	c := &connectorNVMe{
		common: common{
			serverUUID: serverUUID,
		},
	}

	return c, nil
}

const (
	// nvmeDefaultDiscoveryPort is the default port number for NVMe/TCP
	// discovery controller.
	nvmeDefaultDiscoveryPort = "8009"

	// nvmeDefaultTransportPort is the default port number for NVMe/TCP I/O
	// controller.
	nvmeDefaultTransportPort = "4420"
)

// nvmeDiskDevicePrefix is the prefix of the NVMe disk device name in /dev/disk/by-id/.
const nvmeDiskDevicePrefix = "nvme-eui."

// Transport type definitions (from https://github.com/linux-nvme/libnvme/blob/97886cb68d238ccbbed804a275851f63e490b22f/src/nvme/fabrics.c#L73).
const (
	nvmeTransportTypeTCP = "tcp"
)

// nvmeSubtypeNVMeSubsystem defines an NVMe subsystem type (from https://github.com/linux-nvme/libnvme/blob/97886cb68d238ccbbed804a275851f63e490b22f/src/nvme/fabrics.c#L99).
const nvmeSubtypeNVMeSubsystem = "nvme subsystem"

// nvmeDiscoveryLog contains output of nvme discovery call.
type nvmeDiscoveryLog struct {
	Records []nvmeDiscoveryLogRecord `json:"records"`
}

// nvmeDiscoveryLogRecord represents an NVMe discovery entry.
type nvmeDiscoveryLogRecord struct {
	TransportType              string `json:"trtype"`
	TransportAddress           string `json:"traddr"`
	TransportServiceIdentifier string `json:"trsvcid"`
	SubType                    string `json:"subtype"`
	SubNQN                     string `json:"subnqn"`
}

// nvmeRangeDiscoveryLog ranges over filtered and normalized discovery log.
//
// During filtering skips entries from the provided discovery log that do not
// describe NVMe targets with specified transport type.
//
// Normalization depends on record transport type:
//   - For entries with TCP transport type ensure all port numbers (transport
//     service identifiers) are set. For non specified ports function uses
//     the default NVMe transport port number.
func nvmeRangeDiscoveryLog(log *nvmeDiscoveryLog, transportType string) iter.Seq[nvmeDiscoveryLogRecord] {
	return func(yield func(nvmeDiscoveryLogRecord) bool) {
		for _, record := range log.Records {
			if record.SubType != nvmeSubtypeNVMeSubsystem {
				continue
			}

			if record.TransportType != transportType {
				continue
			}

			if record.TransportType == nvmeTransportTypeTCP && record.TransportServiceIdentifier == "" {
				record.TransportServiceIdentifier = nvmeDefaultTransportPort
			}

			if !yield(record) {
				return
			}
		}
	}
}

// Type returns the type of the connector.
func (c *connectorNVMe) Type() ConnectorType {
	return TypeNVME
}

// Version returns the version of the NVMe CLI.
func (c *connectorNVMe) Version() (string, error) {
	// Detect and record the version of the NVMe CLI.
	out, err := shared.RunCommand(context.Background(), "nvme", "version")
	if err != nil {
		return "", fmt.Errorf("Failed getting nvme-cli version: %w", err)
	}

	fields := strings.Split(strings.TrimSpace(out), " ")
	if strings.HasPrefix(out, "nvme version ") && len(fields) > 2 {
		return fields[2] + " (nvme-cli)", nil
	}

	return "", fmt.Errorf("Failed getting nvme-cli version: Unexpected output %q", out)
}

// LoadModules loads the NVMe/TCP kernel modules. Returns nil error if
// the modules can be loaded.
func (c *connectorNVMe) LoadModules() error {
	err := util.LoadModule("nvme_fabrics")
	if err != nil {
		return err
	}

	return util.LoadModule("nvme_tcp")
}

// QualifiedName returns a custom NQN generated from the server UUID.
// Getting the NQN from /etc/nvme/hostnqn would require the nvme-cli
// package to be installed on the host.
func (c *connectorNVMe) QualifiedName() (string, error) {
	return "nqn.2014-08.org.nvmexpress:uuid:" + c.serverUUID, nil
}

// Discover returns the targets found on one of the discovery addresses.
func (c *connectorNVMe) Discover(ctx context.Context, discoveryAddresses ...string) ([]Target, error) {
	hostNQN, err := c.QualifiedName()
	if err != nil {
		return nil, err
	}

	discoverOperation := func(ctx context.Context, discoveryAddress string) ([]Target, error) {
		discoveryAddr, discoveryServiceID, err := net.SplitHostPort(discoveryAddress)
		if err != nil {
			return nil, fmt.Errorf("Bad discovery address %q: %w", discoveryAddress, err)
		}

		stdout, err := shared.RunCommand(ctx, "nvme", "discover",
			"--transport", "tcp",
			"--traddr", discoveryAddr,
			"--trsvcid", discoveryServiceID,
			"--hostnqn", hostNQN,
			"--hostid", c.serverUUID,
			"--output-format", "json",
		)
		if err != nil {
			// Exit code 110 is returned if the target address cannot be reached.
			return nil, fmt.Errorf("Failed connecting to discovery target %q: %w", discoveryAddress, err)
		}

		stdout = strings.TrimSpace(stdout)

		// In case no discovery log entries can be fetched the nvme command doesn't
		// return JSON formatted text.
		if stdout == "No discovery log entries to fetch." {
			return nil, fmt.Errorf("Failed finding discovery log entries from %q: %w", discoveryAddress, err)
		}

		// Try to unmarshal the returned log entries.
		discoveryLog := &nvmeDiscoveryLog{}
		err = json.Unmarshal([]byte(stdout), discoveryLog)
		if err != nil {
			// Don't just log this error. Something is clearly wrong with the returned
			// output.
			return nil, fmt.Errorf("Failed unmarshaling the returned discovery log entries from %q: %w", discoveryAddress, err)
		}

		// Ensure all port numbers are set.
		for i := range discoveryLog.Records {
			if discoveryLog.Records[i].TransportServiceIdentifier != "" {
				continue
			}

			discoveryLog.Records[i].TransportServiceIdentifier = nvmeDefaultTransportPort
		}

		targets := []Target(nil)
		for record := range nvmeRangeDiscoveryLog(discoveryLog, nvmeTransportTypeTCP) {
			target := Target{
				QualifiedName: record.SubNQN,
				Address:       net.JoinHostPort(record.TransportAddress, record.TransportServiceIdentifier),
			}

			targets = append(targets, target)
		}

		return targets, nil
	}

	// Make sure the provided addresses are unique and in an uniform format.
	discoveryAddresses = shared.Unique(slices.Clone(discoveryAddresses))
	for i := range discoveryAddresses {
		discoveryAddresses[i] = shared.EnsurePort(discoveryAddresses[i], nvmeDefaultDiscoveryPort)
	}

	return discover(ctx, discoverOperation, discoveryAddresses...)
}

// Connect establishes connections to targets.
func (c *connectorNVMe) Connect(ctx context.Context, targets ...Target) (revert.Hook, error) {
	// Find an existing NVMe subsystems matching the provided targets.
	subsystems, err := nvmeSubsystems(targets...)
	if err != nil {
		return nil, err
	}

	hostNQN, err := c.QualifiedName()
	if err != nil {
		return nil, err
	}

	connectOperation := func(ctx context.Context, target Target) error {
		_, has := subsystems.ForTarget(target)
		if has {
			// Already connected.
			return nil
		}

		transportAddr, transportServiceID, err := net.SplitHostPort(target.Address)
		if err != nil {
			return fmt.Errorf("Bad transport address %q: %w", target.Address, err)
		}

		_, err = shared.RunCommand(ctx, "nvme", "connect",
			"--transport", "tcp",
			"--traddr", transportAddr,
			"--trsvcid", transportServiceID,
			"--nqn", target.QualifiedName,
			"--hostnqn", hostNQN,
			"--hostid", c.serverUUID,
		)
		if err != nil {
			return fmt.Errorf("Failed connecting to target %q [%s] via NVMe: %w", target.QualifiedName, target.Address, err)
		}

		return nil
	}

	revert, err := connect(ctx, connectOperation, targets...)
	if err != nil && subsystems.Len() == 0 {
		// On failure, if no connection existed before the connect call, attempt to
		// restore the system state.
		_ = c.Disconnect(ctx, targets...)
	}

	return revert, err
}

// Disconnect terminates connections to targets.
func (c *connectorNVMe) Disconnect(_ context.Context, targets ...Target) error {
	// Find an existing NVMe subsystems matching the provided targets.
	subsystems, err := nvmeSubsystems(targets...)
	if err != nil {
		return err
	}

	disconnectOperation := func(ctx context.Context, target Target) error {
		subsystem, has := subsystems.ForTarget(target)
		if !has {
			// There is no subsystem associated with the provided target.
			return nil
		}

		// Check if the entire subsystem can be disconnected in one command.
		if subsystem.FullyContainedWithinTargets(targets...) {
			// Lock subsystem NQN to avoid races with concurrent disconnection attempts.
			unlock, err := lockQualifiedName(ctx, subsystem.NQN)
			if err != nil {
				return fmt.Errorf("Failed disconnecting from target %q [%s] due to the subsystem NQN lock acquisition failure: %w", target.QualifiedName, target.Address, err)
			}

			defer unlock()

			// Disconnect all controllers in one command.
			_, err = shared.RunCommand(ctx, "nvme", "disconnect",
				"--nqn", target.QualifiedName,
			)
			if err != nil {
				return fmt.Errorf("Failed disconnecting from NVMe subsystem for target %q [%s]: %w", target.QualifiedName, target.Address, err)
			}

			return nil
		}

		// Otherwise disconnect single controller device.
		path, has := subsystem.PathForAddress(target.Address)
		if !has {
			return fmt.Errorf("Failed determining NVMe controller for target %q [%s]", target.QualifiedName, target.Address)
		}

		// Disconnect associated controller device.
		_, err = shared.RunCommand(ctx, "nvme", "disconnect",
			"--device", path.Device,
		)
		if err != nil {
			return fmt.Errorf("Failed disconnecting from NVMe controller for target %q [%s]: %w", target.QualifiedName, target.Address, err)
		}

		return nil
	}

	// Do not restrict the context as the operation is relatively short and most
	// importantly we do not want to "partially" disconnect from the target,
	// potentially leaving some unclosed sessions.
	return disconnect(context.Background(), disconnectOperation, targets...)
}

// GetDiskDevicePath returns the path of the mapped device if it exists. If
// the wait parameter is true additionally waits for the mapped device to
// appear and returns its path.
func (c *connectorNVMe) GetDiskDevicePath(ctx context.Context, wait bool, diskNameFilter block.DeviceNameFilterFunc) (string, error) {
	if diskNameFilter == nil {
		diskNameFilter = func(diskPath string) bool { return true }
	}

	return c.common.GetDiskDevicePath(ctx, wait, func(diskPath string) bool {
		return strings.HasPrefix(diskPath, nvmeDiskDevicePrefix) && diskNameFilter(diskPath)
	})
}

// nvmePath encapsulates information about single path within an NVMe
// subsystem.
type nvmePath struct {
	Device                  string
	TransportType           string
	TargetAddress           string
	TargetServiceIdentifier string

	// lookupTargetAddress contains the address in the same form as it appears on
	// the target. For TCP transport it is an IP address with port number.
	lookupTargetAddress string
}

// nvmeSubsystem encapsulates information about NVMe subsystem.
type nvmeSubsystem struct {
	ID    string
	NQN   string
	Paths []nvmePath
}

// PathForAddress returns path associated with the provided address, if any.
func (ss nvmeSubsystem) PathForAddress(addr string) (nvmePath, bool) {
	for _, path := range ss.Paths {
		if path.lookupTargetAddress == addr {
			return path, true
		}
	}
	return nvmePath{}, false
}

// FullyContainedWithinTargets returns true if there is at least one target
// matching each path and NQN within the NVMe subsystem.
func (ss nvmeSubsystem) FullyContainedWithinTargets(targets ...Target) bool {
	targets = slices.DeleteFunc(slices.Clone(targets), func(target Target) bool { return !nvmeCompareNQN(ss.NQN, target.QualifiedName) })
	if len(targets) == 0 {
		return false
	}

	targetsAddresses := targetsAddresses(targets...)
	for _, path := range ss.Paths {
		if !slices.Contains(targetsAddresses, path.lookupTargetAddress) {
			return false
		}
	}

	return true
}

// nvmeSubsystemsSet set of NVMe subsystems.
type nvmeSubsystemsSet struct {
	list []*nvmeSubsystem
}

// Add adds the given subsystem to the set.
func (set *nvmeSubsystemsSet) Add(ss nvmeSubsystem) {
	// Override is subsystem with this NQN already exists.
	for i := range set.list {
		if set.list[i].NQN == ss.NQN {
			set.list[i] = &ss
			return
		}
	}

	set.list = append(set.list, &ss)
}

// Len returns total number of NVMe subsystems.
func (set nvmeSubsystemsSet) Len() int {
	return len(set.list)
}

func nvmeCompareNQN(x, y string) bool {
	// Compare using contains, as target qualified name may not be the entire NQN.
	// For example, PowerFlex target qualified name is a substring of the full NQN.
	return strings.Contains(x, y) || strings.Contains(y, x)
}

// ForNQN retrieves NVMe subsystem associated with the provided NQN from
// the set, if any.
func (set nvmeSubsystemsSet) ForNQN(nqn string) (nvmeSubsystem, bool) {
	for _, ss := range set.list {
		if nvmeCompareNQN(ss.NQN, nqn) {
			return *ss, true
		}
	}

	return nvmeSubsystem{}, false
}

// ForTarget retrieves NVMe subsystem associated with the provided target from
// the set, if any.
func (set nvmeSubsystemsSet) ForTarget(target Target) (nvmeSubsystem, bool) {
	for _, ss := range set.list {
		if !nvmeCompareNQN(ss.NQN, target.QualifiedName) {
			continue
		}

		for _, path := range ss.Paths {
			if path.lookupTargetAddress == target.Address {
				return *ss, true
			}
		}
	}

	return nvmeSubsystem{}, false
}

const (
	nvmeSubsystemsPath = "/sys/class/nvme-subsystem"
)

// nvmeSubsystems returns information about NVMe subsystems associated with
// the provided targets or their qualified names.
//
// This function handles the distinction between an "inactive" subsystems (with
// no active controllers/connections) and a completely "non-existent"
// subsystems. While checking "/sys/class/nvme" for active controllers is
// sufficient to identify if the subsystem is currently in use, it does not
// account for cases where a subsystem exists but is temporarily inactive
// (e.g., due to network issues). Removing such a subsystem during this state
// would prevent it from automatically recovering once the connection is
// restored.
//
// To ensure we detect "existing" subsystems, we first check for
// the subsystem's presence in "/sys/class/nvme-subsystem", which tracks all
// associated NVMe subsystems regardless of their current connection state. If
// such subsystem is found the function determines addresses of the active
// connections by checking "/sys/class/nvme", and fills path information for
// all found subsystems.
func nvmeSubsystems(targets ...Target) (nvmeSubsystemsSet, error) {
	subsystemsDirs, err := os.ReadDir(nvmeSubsystemsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Subsystem do not exists or was just removed.
			return nvmeSubsystemsSet{}, nil
		}

		return nvmeSubsystemsSet{}, fmt.Errorf("Failed getting a list of existing NVMe subsystems: %w", err)
	}

	subsystems := nvmeSubsystemsSet{}
	nqns := targetsQualifiedNames(targets...)
	for _, subsystemDir := range subsystemsDirs {
		subsystemID := subsystemDir.Name()

		// Get the target NQN.
		nqn, err := nvmeSubsystemNQN(subsystemID)
		if err != nil {
			return nvmeSubsystemsSet{}, err
		}

		if nqn == "" {
			// Subsystem do not exists or was just removed.
			continue
		}

		if !slices.ContainsFunc(nqns, func(targetNQN string) bool { return nvmeCompareNQN(nqn, targetNQN) }) {
			// Subsystem is not related to any of the specified targets.
			continue
		}

		subsystem := nvmeSubsystem{
			ID:  subsystemID,
			NQN: nqn,
		}

		controllersIDs, err := nvmeControllersIDs(subsystemID)
		if err != nil {
			return nvmeSubsystemsSet{}, err
		}

		for _, controllerID := range controllersIDs {
			paths, err := nvmeControllerPaths(subsystemID, controllerID)
			if err != nil {
				return nvmeSubsystemsSet{}, err
			}

			subsystem.Paths = append(subsystem.Paths, paths...)
		}

		subsystems.Add(subsystem)
	}

	return subsystems, nil
}

func nvmeSubsystemNQN(subsystemID string) (string, error) {
	nqnFilePath := filepath.Join(nvmeSubsystemsPath, subsystemID, "subsysnqn")
	nqnBytes, err := os.ReadFile(nqnFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Subsystem do not exists or was just removed.
			return "", nil
		}

		return "", fmt.Errorf("Failed getting the target NQN for subsystem %q: %w", subsystemID, err)
	}

	return string(bytes.TrimSpace(nqnBytes)), nil
}

func nvmeControllersIDs(subsystemID string) ([]string, error) {
	subsystemDirPath := filepath.Join(nvmeSubsystemsPath, subsystemID)
	subsystemElems, err := os.ReadDir(subsystemDirPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Subsystem do not exists or was just removed.
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting a list of NVMe subsystem %q controllers: %w", subsystemID, err)
	}

	controllersIDs := []string(nil)
	for _, elem := range subsystemElems {
		// Controller links are inf form "nameX" where "X" is a number
		// (eg. "nvme0",or "nvme4").
		name := elem.Name()

		index, ok := strings.CutPrefix(name, "name")
		if !ok {
			// Not a controller link.
			continue
		}

		_, err := strconv.ParseUint(index, 10, 32)
		if err != nil {
			// Not a controller link.
			continue
		}

		controllersIDs = append(controllersIDs, name)
	}

	return controllersIDs, nil
}

func nvmeControllerPaths(subsystemID, controllerID string) ([]nvmePath, error) {
	transportFilePath := filepath.Join(nvmeSubsystemsPath, subsystemID, controllerID, "transport")
	transportBytes, err := os.ReadFile(transportFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No connections associated with the NVMe controller.
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting NVMe transport type of controller %q in subsystem %q: %w", controllerID, subsystemID, err)
	}

	transportType := string(bytes.TrimSpace(transportBytes))

	addressFilePath := filepath.Join(nvmeSubsystemsPath, subsystemID, controllerID, "address")
	addressBytes, err := os.ReadFile(addressFilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No connections associated with the NVMe controller.
			return nil, nil
		}

		return nil, fmt.Errorf("Failed getting NVMe paths of controller %q in subsystem %q: %w", controllerID, subsystemID, err)
	}

	paths := []nvmePath(nil)

	// Extract the addresses from the file. The "address" file contains one line
	// per connection, each in format "traddr=<ip>,trsvcid=<port>,...". However
	// there usually is only one connection per controller.
	for line := range strings.SplitSeq(string(addressBytes), "\n") {
		keysAndValues := map[string]string{}
		for part := range strings.SplitSeq(strings.TrimSpace(line), ",") {
			key, value, ok := strings.Cut(part, "=")
			if !ok {
				// Skip invalid key-value pairs.
				continue
			}

			keysAndValues[key] = value
		}

		path := nvmePath{
			Device:                  "/dev/" + controllerID,
			TransportType:           transportType,
			TargetAddress:           keysAndValues["traddr"],
			TargetServiceIdentifier: keysAndValues["trsvcid"],
		}

		path.lookupTargetAddress = net.JoinHostPort(path.TargetAddress, path.TargetServiceIdentifier)
		paths = append(paths, path)
	}

	return paths, nil
}
