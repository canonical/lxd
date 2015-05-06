package shared

// This package is intended for rendering ContainerState for use between LXC
// and LXD.

import (
	"database/sql"
	"fmt"
	"net"

	_ "github.com/mattn/go-sqlite3"
	"gopkg.in/lxc/go-lxc.v2"
)

type Ip struct {
	Interface string `json:"interface"`
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
}

type ContainerStatus struct {
	State     string    `json:"status"`
	StateCode lxc.State `json:"status_code"`
	Init      int       `json:"init"`
	Ips       []Ip      `json:"ips"`
}

func getIps(c *lxc.Container) []Ip {
	ips := []Ip{}
	names, err := c.Interfaces()
	if err != nil {
		return ips
	}
	for _, n := range names {
		addresses, err := c.IPAddress(n)
		if err != nil {
			continue
		}
		for _, a := range addresses {
			ip := Ip{Interface: n, Address: a}
			if net.ParseIP(a).To4() == nil {
				ip.Protocol = "IPV6"
			} else {
				ip.Protocol = "IPV4"
			}
			ips = append(ips, ip)
		}
	}
	return ips
}

func NewStatus(c *lxc.Container, state lxc.State) ContainerStatus {
	status := ContainerStatus{State: state.String(), StateCode: state}
	if state == lxc.RUNNING {
		status.Init = c.InitPid()
		status.Ips = getIps(c)
	}
	return status
}

type Device map[string]string
type Devices map[string]Device

type ContainerState struct {
	Name            string            `json:"name"`
	Profiles        []string          `json:"profiles"`
	Config          map[string]string `json:"config"`
	ExpandedConfig  map[string]string `json:"expanded_config"`
	Userdata        []byte            `json:"userdata"`
	Status          ContainerStatus   `json:"status"`
	Devices         Devices           `json:"devices"`
	ExpandedDevices Devices           `json:"expanded_devices"`
	Ephemeral       bool              `json:"ephemeral"`
}

/*
 * BriefContainerState contains a subset of the fields in
 * ContainerState, namely those which a user may update
 */
type BriefContainerState struct {
	Name      string            `json:"name"`
	Profiles  []string          `json:"profiles"`
	Config    map[string]string `json:"config"`
	Devices   Devices           `json:"devices"`
	Ephemeral bool              `json:"ephemeral"`
}

func (c *ContainerState) BriefState() BriefContainerState {
	retstate := BriefContainerState{Name: c.Name,
		Profiles:  c.Profiles,
		Config:    c.Config,
		Devices:   c.Devices,
		Ephemeral: c.Ephemeral}
	return retstate
}

func (c *ContainerState) State() lxc.State {
	return lxc.StateMap[c.Status.State]
}

type ContainerAction string

const (
	Stop     ContainerAction = "stop"
	Start    ContainerAction = "start"
	Restart  ContainerAction = "restart"
	Freeze   ContainerAction = "freeze"
	Unfreeze ContainerAction = "unfreeze"
)

type ProfileConfig struct {
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Devices Devices           `json:"devices"`
}

func ValidDeviceType(t string) bool {
	switch t {
	case "unix-char":
		return true
	case "unix-block":
		return true
	case "nic":
		return true
	case "disk":
		return true
	case "none":
		return true
	default:
		return false
	}
}

func ValidDeviceConfig(t, k, v string) bool {
	if k == "type" {
		return false
	}
	switch t {
	case "unix-char":
		switch k {
		case "path":
			return true
		case "major":
			return true
		case "minor":
			return true
		case "uid":
			return true
		case "gid":
			return true
		case "mode":
			return true
		default:
			return false
		}
	case "unix-block":
		switch k {
		case "path":
			return true
		case "major":
			return true
		case "minor":
			return true
		case "uid":
			return true
		case "gid":
			return true
		case "mode":
			return true
		default:
			return false
		}
	case "nic":
		switch k {
		case "parent":
			return true
		case "name":
			return true
		case "hwaddr":
			return true
		case "mtu":
			return true
		case "nictype":
			if v != "bridged" && v != "" {
				return false
			}
			return true
		default:
			return false
		}
	case "disk":
		switch k {
		case "path":
			return true
		case "source":
			return true
		case "readonly", "optional":
			return true
		default:
			return false
		}
	case "none":
		return false
	default:
		return false
	}
}

func AddDevices(tx *sql.Tx, w string, cId int, devices Devices) error {
	str1 := fmt.Sprintf("INSERT INTO %ss_devices (%s_id, name, type) VALUES (?, ?, ?)", w, w)
	stmt1, err := tx.Prepare(str1)
	if err != nil {
		return err
	}
	defer stmt1.Close()
	str2 := fmt.Sprintf("INSERT INTO %ss_devices_config (%s_device_id, key, value) VALUES (?, ?, ?)", w, w)
	stmt2, err := tx.Prepare(str2)
	if err != nil {
		return err
	}
	defer stmt2.Close()
	for k, v := range devices {
		if !ValidDeviceType(v["type"]) {
			return fmt.Errorf("Invalid device type %s\n", v["type"])
		}
		result, err := stmt1.Exec(cId, k, v["type"])
		if err != nil {
			return err
		}
		id64, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("Error inserting device %s into database", k)
		}
		// TODO: is this really int64? we should fix it everywhere if so
		id := int(id64)
		for ck, cv := range v {
			if ck == "type" {
				continue
			}
			if !ValidDeviceConfig(v["type"], ck, cv) {
				return fmt.Errorf("Invalid device config %s %s\n", ck, cv)
			}
			_, err = stmt2.Exec(id, ck, cv)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
