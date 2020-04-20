package simplestreams

import (
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/osarch"
)

var nativeName, _ = osarch.ArchitectureGetLocal()

type sortedImages []api.Image

func (a sortedImages) Len() int {
	return len(a)
}

func (a sortedImages) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a sortedImages) Less(i, j int) bool {
	if a[i].Properties["type"] != a[j].Properties["type"] {
		if a[i].Properties["type"] == "squashfs" {
			return true
		}

		if a[i].Properties["type"] == "disk-kvm.img" {
			return true
		}
	}

	if a[i].Properties["os"] == a[j].Properties["os"] {
		if a[i].Properties["release"] == a[j].Properties["release"] {
			if !shared.TimeIsSet(a[i].CreatedAt) {
				return true
			}

			if !shared.TimeIsSet(a[j].CreatedAt) {
				return false
			}

			if a[i].CreatedAt == a[j].CreatedAt {
				return a[i].Properties["serial"] > a[j].Properties["serial"]
			}

			return a[i].CreatedAt.UTC().Unix() > a[j].CreatedAt.UTC().Unix()
		}

		if a[i].Properties["release"] == "" {
			return false
		}

		if a[j].Properties["release"] == "" {
			return true
		}

		return a[i].Properties["release"] < a[j].Properties["release"]
	}

	if a[i].Properties["os"] == "" {
		return false
	}

	if a[j].Properties["os"] == "" {
		return true
	}

	return a[i].Properties["os"] < a[j].Properties["os"]
}

type sortedAliases []extendedAlias

func (a sortedAliases) Len() int {
	return len(a)
}

func (a sortedAliases) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

func (a sortedAliases) Less(i, j int) bool {
	// Check functions.
	isNative := func(arch string) bool {
		return nativeName == arch
	}

	isPersonality := func(arch string) bool {
		archID, err := osarch.ArchitectureId(nativeName)
		if err != nil {
			return false
		}

		personalities, err := osarch.ArchitecturePersonalities(archID)
		if err != nil {
			return false
		}

		for _, personality := range personalities {
			personalityName, err := osarch.ArchitectureName(personality)
			if err != nil {
				return false
			}

			if personalityName == arch {
				return true
			}
		}

		return false
	}

	// Same thing.
	if a[i].Architecture == a[j].Architecture {
		return false
	}

	// Look for native.
	if isNative(a[i].Architecture) {
		return true
	}

	// Look for personality.
	if isPersonality(a[i].Architecture) && !isNative(a[j].Architecture) {
		return true
	}

	return false
}
