// +build linux
// +build cgo

package shared

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

/*
#include "../shared/netns_getifaddrs.c"
*/
// #cgo CFLAGS: -std=gnu11 -Wvla
import "C"

func NetnsGetifaddrs(initPID int32) (map[string]api.ContainerStateNetwork, error) {
	var netnsid_aware C.bool
	var ifaddrs *C.struct_netns_ifaddrs
	var netnsID C.__s32

	if initPID > 0 {
		f, err := os.Open(fmt.Sprintf("/proc/%d/ns/net", initPID))
		if err != nil {
			return nil, err
		}
		defer f.Close()

		netnsID = C.netns_get_nsid(C.__s32(f.Fd()))
		if netnsID < 0 {
			return nil, fmt.Errorf("Failed to retrieve network namespace id")
		}
	} else {
		netnsID = -1
	}

	ret := C.netns_getifaddrs(&ifaddrs, netnsID, &netnsid_aware)
	if ret < 0 {
		return nil, fmt.Errorf("Failed to retrieve network interfaces and addresses")
	}
	defer C.netns_freeifaddrs(ifaddrs)

	if netnsID >= 0 && !netnsid_aware {
		return nil, fmt.Errorf("Netlink requests are not fully network namespace id aware")
	}

	// We're using the interface name as key here but we should really
	// switch to the ifindex at some point to handle ip aliasing correctly.
	networks := map[string]api.ContainerStateNetwork{}

	for addr := ifaddrs; addr != nil; addr = addr.ifa_next {
		var address [C.INET6_ADDRSTRLEN]C.char
		addNetwork, networkExists := networks[C.GoString(addr.ifa_name)]
		if !networkExists {
			addNetwork = api.ContainerStateNetwork{
				Addresses: []api.ContainerStateNetworkAddress{},
				Counters:  api.ContainerStateNetworkCounters{},
			}
		}

		if addr.ifa_addr != nil && (addr.ifa_addr.sa_family == C.AF_INET || addr.ifa_addr.sa_family == C.AF_INET6) {
			netState := "down"
			netType := "unknown"

			if (addr.ifa_flags & C.IFF_BROADCAST) > 0 {
				netType = "broadcast"
			}

			if (addr.ifa_flags & C.IFF_LOOPBACK) > 0 {
				netType = "loopback"
			}

			if (addr.ifa_flags & C.IFF_POINTOPOINT) > 0 {
				netType = "point-to-point"
			}

			if (addr.ifa_flags & C.IFF_UP) > 0 {
				netState = "up"
			}

			family := "inet"
			if addr.ifa_addr.sa_family == C.AF_INET6 {
				family = "inet6"
			}

			addr_ptr := C.get_addr_ptr(addr.ifa_addr)
			if addr_ptr == nil {
				return nil, fmt.Errorf("Failed to retrieve valid address pointer")
			}

			address_str := C.inet_ntop(C.int(addr.ifa_addr.sa_family), addr_ptr, &address[0], C.INET6_ADDRSTRLEN)
			if address_str == nil {
				return nil, fmt.Errorf("Failed to retrieve address string")
			}

			if addNetwork.Addresses == nil {
				addNetwork.Addresses = []api.ContainerStateNetworkAddress{}
			}

			goAddrString := C.GoString(address_str)
			scope := "global"
			if strings.HasPrefix(goAddrString, "127") {
				scope = "local"
			}

			if goAddrString == "::1" {
				scope = "local"
			}

			if strings.HasPrefix(goAddrString, "169.254") {
				scope = "link"
			}

			if strings.HasPrefix(goAddrString, "fe80:") {
				scope = "link"
			}

			address := api.ContainerStateNetworkAddress{}
			address.Family = family
			address.Address = goAddrString
			address.Netmask = fmt.Sprintf("%d", int(addr.ifa_prefixlen))
			address.Scope = scope

			addNetwork.Addresses = append(addNetwork.Addresses, address)
			addNetwork.State = netState
			addNetwork.Type = netType
			addNetwork.Mtu = int(addr.ifa_mtu)
		} else if addr.ifa_addr != nil && addr.ifa_addr.sa_family == C.AF_PACKET {
			if (addr.ifa_flags & C.IFF_LOOPBACK) == 0 {
				var buf [1024]C.char

				hwaddr := C.get_packet_address(addr.ifa_addr, &buf[0], 1024)
				if hwaddr == nil {
					return nil, fmt.Errorf("Failed to retrieve hardware address")
				}

				addNetwork.Hwaddr = C.GoString(hwaddr)
			}
		}

		if addr.ifa_stats_type == C.IFLA_STATS64 {
			addNetwork.Counters.BytesReceived = int64(addr.ifa_stats64.rx_bytes)
			addNetwork.Counters.BytesSent = int64(addr.ifa_stats64.tx_bytes)
			addNetwork.Counters.PacketsReceived = int64(addr.ifa_stats64.rx_packets)
			addNetwork.Counters.PacketsSent = int64(addr.ifa_stats64.tx_packets)
		}
		ifName := C.GoString(addr.ifa_name)

		networks[ifName] = addNetwork
	}

	return networks, nil
}

func WebsocketExecMirror(conn *websocket.Conn, w io.WriteCloser, r io.ReadCloser, exited chan bool, fd int) (chan bool, chan bool) {
	readDone := make(chan bool, 1)
	writeDone := make(chan bool, 1)

	go defaultWriter(conn, w, writeDone)

	go func(conn *websocket.Conn, r io.ReadCloser) {
		in := ExecReaderToChannel(r, -1, exited, fd)
		for {
			buf, ok := <-in
			if !ok {
				r.Close()
				logger.Debugf("sending write barrier")
				conn.WriteMessage(websocket.TextMessage, []byte{})
				readDone <- true
				return
			}
			w, err := conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				logger.Debugf("Got error getting next writer %s", err)
				break
			}

			_, err = w.Write(buf)
			w.Close()
			if err != nil {
				logger.Debugf("Got err writing %s", err)
				break
			}
		}
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteMessage(websocket.CloseMessage, closeMsg)
		readDone <- true
		r.Close()
	}(conn, r)

	return readDone, writeDone
}
