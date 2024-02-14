package uefi

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/canonical/lxd/lxd/apparmor"
	"github.com/canonical/lxd/lxd/sys"
	"github.com/canonical/lxd/shared/api"
)

// PyUEFIVars represents the JSON output format of the python-uefivars utility
// URL: https://github.com/awslabs/python-uefivars
type PyUEFIVars struct {
	Version   uint32      `json:"version"`
	Variables []PyUEFIVar `json:"variables"`
}

// PyUEFIVar represents a UEFI variable entry.
type PyUEFIVar struct {
	// UEFI variable name
	GUID string `json:"guid"`

	// UEFI variable name
	Name string `json:"name"`

	// UEFI variable data (HEX-encoded)
	Data string `json:"data"`

	// UEFI variable attributes
	Attr uint32 `json:"attr"`

	// UEFI variable timestamp (HEX-encoded)
	Timestamp string `json:"timestamp"`

	// UEFI variable digest (HEX-encoded)
	Digest string `json:"digest"`
}

// Validate checks whether the InstanceUEFIVars structure is valid.
func Validate(e api.InstanceUEFIVars) error {
	for k, v := range e.Variables {
		// Hashmap key format is <Var name>-<UUID>
		// UUID length is 36
		// Var name length is at least 1
		// and we have "-" as a separator between name and UUID
		if len(k) < 36+1+1 {
			return fmt.Errorf("Bad UEFI variable key: %q", k)
		}

		guid := k[len(k)-36:]
		_, err := uuid.Parse(guid)
		if err != nil {
			return fmt.Errorf("Bad UEFI variable key: %q. Bad UUID: %w", k, err)
		}

		name := k[:len(k)-37]

		_, err = hex.DecodeString(v.Timestamp)
		if err != nil {
			return fmt.Errorf("Bad UEFI variable (key: %q) timestamp (HEX-encoding expected): %w", k, err)
		}

		_, err = hex.DecodeString(v.Digest)
		if err != nil {
			return fmt.Errorf("Bad UEFI variable (key: %q) digest (HEX-encoding expected): %w", k, err)
		}

		// Linux kernel efivarfs limits [1] maximum capacity for
		// the variable data and name to 1024 bytes, while edk2
		// allows up to 33KiB. [2]
		// [1] https://github.com/torvalds/linux/blob/1f719a2f3fa67665578c759ac34fd3d3690c1a20/fs/efivarfs/vars.c#L393
		// [2] https://github.com/tianocore/edk2/blob/e32b58ab5a12d37c82327f28376e7d12cccc8b3a/OvmfPkg/OvmfPkgX64.dsc#L526
		decodedData, err := hex.DecodeString(v.Data)
		if err != nil {
			return fmt.Errorf("Bad UEFI variable (key: %q) data (HEX-encoding expected): %w", k, err)
		}

		if len(name)+len(decodedData) > 0x8400 {
			return fmt.Errorf("Bad UEFI variable key: %q", k)
		}
	}

	return nil
}

// UEFIVars reads UEFI Variables for instance.
func UEFIVars(sysOS *sys.OS, uefiVarsPath string) (*api.InstanceUEFIVars, error) {
	var stdout bytes.Buffer
	err := apparmor.PythonUEFIVars(sysOS, nil, &stdout, uefiVarsPath)
	if err != nil {
		return nil, err
	}

	pyUEFIVars := PyUEFIVars{}
	err = json.Unmarshal(stdout.Bytes(), &pyUEFIVars)
	if err != nil {
		return nil, err
	}

	if pyUEFIVars.Version != 2 {
		return nil, fmt.Errorf("python-uefivars utility version is not compatible with LXD server")
	}

	instanceUEFI := api.InstanceUEFIVars{}
	instanceUEFI.Variables = make(map[string]api.InstanceUEFIVariable)

	for _, v := range pyUEFIVars.Variables {
		key := v.Name + "-" + v.GUID
		instanceUEFI.Variables[key] = api.InstanceUEFIVariable{
			Data:      v.Data,
			Attr:      v.Attr,
			Timestamp: v.Timestamp,
			Digest:    v.Digest,
		}
	}

	return &instanceUEFI, nil
}

// UEFIVarsUpdate updates UEFI Variables for instance.
func UEFIVarsUpdate(sysOS *sys.OS, newUEFIVarsSet api.InstanceUEFIVars, uefiVarsPath string) error {
	err := Validate(newUEFIVarsSet)
	if err != nil {
		return err
	}

	pyUEFIVars := PyUEFIVars{
		Version:   2,
		Variables: make([]PyUEFIVar, 0),
	}

	for k, v := range newUEFIVarsSet.Variables {
		pyUEFIVars.Variables = append(pyUEFIVars.Variables, PyUEFIVar{
			GUID:      k[len(k)-36:],
			Name:      k[:len(k)-37],
			Data:      v.Data,
			Attr:      v.Attr,
			Timestamp: v.Timestamp,
			Digest:    v.Digest,
		})
	}

	pyUEFIVarsJSON, err := json.Marshal(pyUEFIVars)
	if err != nil {
		return err
	}

	err = apparmor.PythonUEFIVars(sysOS, strings.NewReader(string(pyUEFIVarsJSON)), nil, uefiVarsPath)
	if err != nil {
		return err
	}

	return nil
}
