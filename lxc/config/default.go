package config

// LocalRemote is the default local remote (over the LXD unix socket).
var LocalRemote = Remote{
	Addr:   "unix://",
	Static: true,
	Public: false,
}

// ImagesRemote is the main image server (over simplestreams).
var ImagesRemote = Remote{
	Addr:     "https://images.lxd.canonical.com",
	Public:   true,
	Protocol: "simplestreams",
}

// UbuntuRemote is the Ubuntu image server (over simplestreams).
var UbuntuRemote = Remote{
	Addr:     "https://cloud-images.ubuntu.com/releases",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams",
}

// UbuntuDailyRemote is the Ubuntu daily image server (over simplestreams).
var UbuntuDailyRemote = Remote{
	Addr:     "https://cloud-images.ubuntu.com/daily",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams",
}

// UbuntuMinimalRemote is the Ubuntu minimal image server (over simplestreams).
var UbuntuMinimalRemote = Remote{
	Addr:     "https://cloud-images.ubuntu.com/minimal/releases/",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams",
}

// UbuntuMinimalDailyRemote is the Ubuntu daily minimal image server (over simplestreams).
var UbuntuMinimalDailyRemote = Remote{
	Addr:     "https://cloud-images.ubuntu.com/minimal/daily/",
	Static:   true,
	Public:   true,
	Protocol: "simplestreams",
}

// StaticRemotes is the list of remotes which can't be removed.
var StaticRemotes = map[string]Remote{
	"local":                LocalRemote,
	"images":               ImagesRemote,
	"ubuntu":               UbuntuRemote,
	"ubuntu-daily":         UbuntuDailyRemote,
	"ubuntu-minimal":       UbuntuMinimalRemote,
	"ubuntu-minimal-daily": UbuntuMinimalDailyRemote,
}

// DefaultRemotes is the list of default remotes.
var DefaultRemotes = map[string]Remote{
	"local":                LocalRemote,
	"ubuntu":               UbuntuRemote,
	"ubuntu-daily":         UbuntuDailyRemote,
	"ubuntu-minimal":       UbuntuMinimalRemote,
	"ubuntu-minimal-daily": UbuntuMinimalDailyRemote,
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	// Duplicate remotes from DefaultRemotes.
	defaultRoutes := make(map[string]Remote, len(DefaultRemotes))
	for k, v := range DefaultRemotes {
		defaultRoutes[k] = v
	}

	return &Config{
		Remotes:       defaultRoutes,
		Aliases:       make(map[string]string),
		DefaultRemote: "local",
	}
}
