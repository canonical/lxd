// +build linux dragonfly freebsd netbsd openbsd

package shared

import (
	"net"
	"os"
	"path"
)

const (
	SYS_CLASS_NET = "/sys/class/net"
)

func IsBridge(iface *net.Interface) bool {
	p := path.Join(SYS_CLASS_NET, iface.Name, "bridge")
	stat, err := os.Stat(p)
	if err != nil {
		return false
	}

	return stat.IsDir()
}
