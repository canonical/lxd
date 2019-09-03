package simplestreams

import (
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

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
