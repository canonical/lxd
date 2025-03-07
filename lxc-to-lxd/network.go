package main

import (
	"strconv"
	"strings"

	liblxc "github.com/lxc/go-lxc"
)

func networkGet(container *liblxc.Container, index int, configKey string) map[string]string {
	keys := container.ConfigKeys(configKey + "." + strconv.FormatInt(int64(index), 10))
	if len(keys) == 0 {
		return nil
	}

	dev := make(map[string]string)
	for _, k := range keys {
		value := container.ConfigItem(configKey + "." + strconv.FormatInt(int64(index), 10) + "." + k)
		if len(value) == 0 || strings.TrimSpace(value[0]) == "" {
			continue
		}

		dev[k] = value[0]
	}

	if len(dev) == 0 {
		return nil
	}

	return dev
}
