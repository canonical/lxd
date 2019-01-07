package cluster

import (
	"fmt"
	"strings"

	"github.com/lxc/lxd/shared/version"
)

// Numeric type codes identifying different kind of entities.
const (
	TypeContainer = iota
	TypeImage
	TypeProfile
	TypeProject
)

// EntityNames associates an entity code to its name.
var EntityNames = map[int]string{
	TypeContainer: "container",
	TypeImage:     "image",
	TypeProfile:   "profile",
	TypeProject:   "project",
}

// EntityTypes associates an entity name to its type code.
var EntityTypes = map[string]int{}

// EntityURIs associates an entity code to its URI pattern.
var EntityURIs = map[int]string{
	TypeContainer: "/" + version.APIVersion + "/containers/%s?project=%s",
	TypeImage:     "/" + version.APIVersion + "/images/%s",
	TypeProfile:   "/" + version.APIVersion + "/profiles/%s?project=%s",
	TypeProject:   "/" + version.APIVersion + "/projects/%s",
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
}

func init() {
	for code, name := range EntityNames {
		EntityTypes[name] = code
	}
}
