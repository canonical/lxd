package callhook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
)

// ParseArgs parses callhook request into constituent parts.
func ParseArgs(args []string) (lxdPath string, projectName string, instanceRef string, hook string, cdiHooksFiles []string, err error) {
	argsLen := len(args)

	if argsLen < 2 {
		return "", "", "", "", nil, errors.New("Missing required arguments")
	}

	lxdPath = args[0]

	if argsLen == 3 {
		instanceRef = args[1]
		hook = args[2]
	} else if argsLen == 4 {
		projectName = args[1]
		instanceRef = args[2]
		hook = args[3]
	} else if argsLen >= 5 {
		projectName = args[1]
		instanceRef = args[2]
		hook = args[3]
		cdiHooksFiles = make([]string, len(args[4:]))
		copy(cdiHooksFiles, args[4:])
	}

	return lxdPath, projectName, instanceRef, hook, cdiHooksFiles, nil
}

// HandleContainerHook passes the callhook request to the LXD server via the UNIX socket.
func HandleContainerHook(lxdPath string, projectName string, instanceRef string, hook string) error {
	// Connect to LXD.
	socket := os.Getenv("LXD_SOCKET")
	if socket == "" {
		socket = filepath.Join(lxdPath, "unix.socket")
	}

	// Detect stop target.
	var target string
	if hook == "stop" || hook == "stopns" {
		target = os.Getenv("LXC_TARGET")
		if target == "" {
			target = "unknown"
		}
	}

	// Timeout hook request to LXD after 30s.
	ctx, done := context.WithTimeout(context.Background(), time.Second*30)
	defer done()

	// Setup the request to LXD.
	lxdArgs := lxd.ConnectionArgs{
		SkipGetServer: true,
	}

	d, err := lxd.ConnectLXDUnixWithContext(ctx, socket, &lxdArgs)
	if err != nil {
		return err
	}

	u := api.NewURL().Path("internal", "containers", instanceRef, "on"+hook)
	u.WithQuery("target", target)

	if projectName != "" {
		u.WithQuery("project", projectName)
	}

	if hook == "stopns" {
		u.WithQuery("netns", os.Getenv("LXC_NET_NS"))
	}

	_, _, err = d.RawQuery("GET", u.String(), nil, "")
	if err != nil {
		return err
	}

	// If the container is rebooting, we purposefully tell LXC that this hook failed so that
	// it won't reboot the container, which lets LXD start it again in the OnStop function.
	// Other hook types can return without error safely.
	if hook == "stop" && target == "reboot" {
		return errors.New("Reboot must be handled by LXD")
	}

	return nil
}
