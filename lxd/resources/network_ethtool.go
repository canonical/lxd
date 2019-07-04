package resources

import (
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/shared/api"
)

type ethtoolReq struct {
	name [16]byte
	data uintptr
}

type ethtoolMode struct {
	bit  uint
	name string
}

var ethtoolModes = []ethtoolMode{
	{0, "10baseT/Half"},
	{1, "10baseT/Full"},
	{2, "100baseT/Half"},
	{3, "100baseT/Full"},
	{4, "1000baseT/Half"},
	{5, "1000baseT/Full"},
	{12, "10000baseT/Full"},
	{15, "2500baseX/Full"},
	{17, "1000baseKX/Full"},
	{18, "10000baseKX4/Full"},
	{19, "10000baseKR/Full"},
	{21, "20000baseMLD2/Full"},
	{22, "20000baseKR2/Full"},
	{23, "40000baseKR4/Full"},
	{24, "40000baseCR4/Full"},
	{25, "40000baseSR4/Full"},
	{26, "40000baseLR4/Full"},
	{27, "56000baseKR4/Full"},
	{28, "56000baseCR4/Full"},
	{29, "56000baseSR4/Full"},
	{30, "56000baseLR4/Full"},
	{31, "25000baseCR/Full"},
	{32, "25000baseKR/Full"},
	{33, "25000baseSR/Full"},
	{34, "50000baseCR2/Full"},
	{35, "50000baseKR2/Full"},
	{36, "100000baseKR4/Full"},
	{37, "100000baseSR4/Full"},
	{38, "100000baseCR4/Full"},
	{39, "100000baseLR4_ER4/Full"},
	{40, "50000baseSR2/Full"},
	{41, "1000baseX/Full"},
	{42, "10000baseCR/Full"},
	{43, "10000baseSR/Full"},
	{44, "10000baseLR/Full"},
	{45, "10000baseLRM/Full"},
	{46, "10000baseER/Full"},
	{47, "2500baseT/Full"},
	{48, "5000baseT/Full"},
}

var ethtoolPorts = []ethtoolMode{
	{7, "twisted pair"},
	{8, "AUI"},
	{9, "media-independent"},
	{10, "fibre"},
	{11, "BNC"},
	{16, "backplane"},
}

type ethtoolCmd struct {
	cmd           uint32
	supported     uint32
	advertising   uint32
	speed         uint16
	duplex        uint8
	port          uint8
	phyAddress    uint8
	transceiver   uint8
	autoneg       uint8
	mdioSupport   uint8
	maxtxpkt      uint32
	maxrxpkt      uint32
	speedHi       uint16
	ethTpMdix     uint8
	ethTpMdixCtrl uint8
	lpAdvertising uint32
	reserved      [2]uint32
}

type ethtoolValue struct {
	cmd  uint32
	data uint32
}

func ethtoolAddInfo(info *api.ResourcesNetworkCardPort) error {
	// Open FD
	ethtoolFd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, unix.IPPROTO_IP)
	if err != nil {
		return err
	}
	defer unix.Close(ethtoolFd)

	// Link state
	ethGlink := ethtoolValue{
		cmd: 0x0000000a,
	}

	req := ethtoolReq{
		data: uintptr(unsafe.Pointer(&ethGlink)),
	}
	copy(req.name[:], []byte(info.ID))

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(ethtoolFd), unix.SIOCETHTOOL, uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		return unix.Errno(errno)
	}

	info.LinkDetected = ethGlink.data == 1

	// Interface info
	ethCmd := ethtoolCmd{
		cmd: 0x00000001,
	}
	req.data = uintptr(unsafe.Pointer(&ethCmd))

	_, _, errno = unix.Syscall(unix.SYS_IOCTL, uintptr(ethtoolFd), unix.SIOCETHTOOL, uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		return unix.Errno(errno)
	}

	// Link negotiation
	info.AutoNegotiation = ethCmd.autoneg == 1

	if info.LinkDetected {
		// Link duplex
		if ethCmd.duplex == 0x00 {
			info.LinkDuplex = "half"
		} else if ethCmd.duplex == 0x01 {
			info.LinkDuplex = "full"
		}

		// Link speed
		speed := uint64(uint32(ethCmd.speedHi)<<16 | uint32(ethCmd.speed))
		if speed != 65535 && speed != 4294967295 {
			info.LinkSpeed = speed
		}
	}

	// Transceiver
	if ethCmd.transceiver == 0x00 {
		info.TransceiverType = "internal"
	} else if ethCmd.transceiver == 0x01 {
		info.TransceiverType = "external"
	}

	// Port
	if ethCmd.port == 0x00 {
		info.PortType = "twisted pair"
	} else if ethCmd.port == 0x01 {
		info.PortType = "AUI"
	} else if ethCmd.port == 0x02 {
		info.PortType = "media-independent"
	} else if ethCmd.port == 0x03 {
		info.PortType = "fibre"
	} else if ethCmd.port == 0x04 {
		info.PortType = "BNC"
	} else if ethCmd.port == 0x05 {
		info.PortType = "direct attach"
	} else if ethCmd.port == 0xef {
		info.PortType = "none"
	} else if ethCmd.port == 0xff {
		info.PortType = "other"
	}

	// Supported modes
	info.SupportedModes = []string{}
	for _, mode := range ethtoolModes {
		if hasBit(ethCmd.supported, mode.bit) {
			info.SupportedModes = append(info.SupportedModes, mode.name)
		}
	}

	// Supported ports
	info.SupportedPorts = []string{}
	for _, port := range ethtoolPorts {
		if hasBit(ethCmd.supported, port.bit) {
			info.SupportedPorts = append(info.SupportedPorts, port.name)
		}
	}

	return nil
}
