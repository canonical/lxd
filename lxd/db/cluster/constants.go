package cluster

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared/version"
)

// Numeric type codes identifying different kind of entities.
const (
	TypeContainer             = 0
	TypeImage                 = 1
	TypeProfile               = 2
	TypeProject               = 3
	TypeCertificate           = 4
	TypeInstance              = 5
	TypeInstanceBackup        = 6
	TypeInstanceSnapshot      = 7
	TypeNetwork               = 8
	TypeNetworkACL            = 9
	TypeNode                  = 10
	TypeOperation             = 11
	TypeStoragePool           = 12
	TypeStorageVolume         = 13
	TypeStorageVolumeBackup   = 14
	TypeStorageVolumeSnapshot = 15
	TypeWarning               = 16
	TypeClusterGroup          = 17
)

// EntityNames associates an entity code to its name.
var EntityNames = map[int]string{
	TypeContainer:             "container",
	TypeImage:                 "image",
	TypeProfile:               "profile",
	TypeProject:               "project",
	TypeCertificate:           "certificate",
	TypeInstance:              "instance",
	TypeInstanceBackup:        "instance backup",
	TypeInstanceSnapshot:      "instance snapshot",
	TypeNetwork:               "network",
	TypeNetworkACL:            "network acl",
	TypeNode:                  "node",
	TypeOperation:             "operation",
	TypeStoragePool:           "storage pool",
	TypeStorageVolume:         "storage volume",
	TypeStorageVolumeBackup:   "storage volume backup",
	TypeStorageVolumeSnapshot: "storage volume snapshot",
	TypeWarning:               "warning",
	TypeClusterGroup:          "cluster group",
}

// EntityTypes associates an entity name to its type code.
var EntityTypes = map[string]int{}

// EntityURIs associates an entity code to its URI pattern.
var EntityURIs = map[int]string{
	TypeContainer:             "/" + version.APIVersion + "/containers/%s?project=%s",
	TypeImage:                 "/" + version.APIVersion + "/images/%s?project=%s",
	TypeProfile:               "/" + version.APIVersion + "/profiles/%s?project=%s",
	TypeProject:               "/" + version.APIVersion + "/projects/%s",
	TypeCertificate:           "/" + version.APIVersion + "/certificates/%s",
	TypeInstance:              "/" + version.APIVersion + "/instances/%s?project=%s",
	TypeInstanceBackup:        "/" + version.APIVersion + "/instances/%s/backups/%s?project=%s",
	TypeInstanceSnapshot:      "/" + version.APIVersion + "/instances/%s/snapshots/%s?project=%s",
	TypeNetwork:               "/" + version.APIVersion + "/networks/%s?project=%s",
	TypeNetworkACL:            "/" + version.APIVersion + "/network-acls/%s?project=%s",
	TypeNode:                  "/" + version.APIVersion + "/cluster/members/%s",
	TypeOperation:             "/" + version.APIVersion + "/operations/%s",
	TypeStoragePool:           "/" + version.APIVersion + "/storage-pools/%s",
	TypeStorageVolume:         "/" + version.APIVersion + "/storage-pools/%s/volumes/%s/%s?project=%s",
	TypeStorageVolumeBackup:   "/" + version.APIVersion + "/storage-pools/%s/volumes/%s/%s/backups/%s?project=%s",
	TypeStorageVolumeSnapshot: "/" + version.APIVersion + "/storage-pools/%s/volumes/%s/%s/snapshots/%s?project=%s",
	TypeWarning:               "/" + version.APIVersion + "/warnings/%s",
	TypeClusterGroup:          "/" + version.APIVersion + "/cluster/groups/%s",
}

// EntityFormatURIs associates an entity code to a formatter function that can be
// used to format its URI.
var EntityFormatURIs = map[int]func(a ...interface{}) string{
	TypeContainer: func(a ...interface{}) string {
		uri := fmt.Sprintf(EntityURIs[TypeContainer], a[1], a[0])
		if a[0] == "default" {
			return strings.Split(uri, fmt.Sprintf("?project=%s", a[0]))[0]
		}

		return uri
	},
	TypeInstance: func(a ...interface{}) string {
		uri := fmt.Sprintf(EntityURIs[TypeInstance], a[1], a[0])
		if a[0] == "default" {
			return strings.Split(uri, fmt.Sprintf("?project=%s", a[0]))[0]
		}

		return uri
	},
	TypeProfile: func(a ...interface{}) string {
		uri := fmt.Sprintf(EntityURIs[TypeProfile], a[1], a[0])
		if a[0] == "default" {
			return strings.Split(uri, fmt.Sprintf("?project=%s", a[0]))[0]
		}

		return uri
	},
	TypeProject: func(a ...interface{}) string {
		uri := fmt.Sprintf(EntityURIs[TypeProject], a[0])
		return uri
	},
	TypeNetwork: func(a ...interface{}) string {
		uri := fmt.Sprintf(EntityURIs[TypeNetwork], a[1], a[0])
		if a[0] == "default" {
			return strings.Split(uri, fmt.Sprintf("?project=%s", a[0]))[0]
		}

		return uri
	},
	TypeNetworkACL: func(a ...interface{}) string {
		uri := fmt.Sprintf(EntityURIs[TypeNetworkACL], a[1], a[0])
		if a[0] == "default" {
			return strings.Split(uri, fmt.Sprintf("?project=%s", a[0]))[0]
		}

		return uri
	},
	TypeImage: func(a ...interface{}) string {
		uri := fmt.Sprintf(EntityURIs[TypeImage], a[1], a[0])
		if a[0] == "default" {
			return strings.Split(uri, fmt.Sprintf("?project=%s", a[0]))[0]
		}

		return uri
	},
	TypeClusterGroup: func(a ...interface{}) string {
		return fmt.Sprintf(EntityURIs[TypeClusterGroup], a[0])
	},
}

func init() {
	for code, name := range EntityNames {
		EntityTypes[name] = code
	}
}
