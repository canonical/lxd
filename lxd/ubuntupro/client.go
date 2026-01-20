package ubuntupro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"slices"

	"github.com/canonical/lxd/lxd/fsmonitor"
	"github.com/canonical/lxd/lxd/fsmonitor/drivers"
	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"
	"github.com/canonical/lxd/shared/version"
)

const (
	// guestAttachSettingOff indicates that guest attachment is turned off.
	// - When the host has this setting turned off, devlxd requests to `GET /1.0/ubuntu-pro` should return "off" and
	//   `POST /1.0/ubuntu-pro/token` should return a 403 Forbidden (regardless of the guest setting).
	// - When the guest has this setting turned off (`ubuntu_pro.guest_attach`), devlxd requests to `GET /1.0/ubuntu-pro`
	//   should return "off" and `POST /1.0/ubuntu-pro/token` should return a 403 Forbidden (regardless of the host setting).
	guestAttachSettingOff = "off"

	// guestAttachSettingAvailable indicates that guest attachment is available.
	// - When the host has this setting, devlxd requests to `GET /1.0/ubuntu-pro` should return the setting from the guest
	//   (`ubuntu_pro.guest_attach) and `POST /1.0/ubuntu-pro/token` should retrieve a guest token via the Ubuntu Pro client.
	// - When the guest has this setting, the pro client inside the guest will not try to retrieve a guest token at startup
	//   (though attachment with a guest token can still be performed with `pro auto-attach`.
	guestAttachSettingAvailable = "available"

	// guestAttachSettingOn indicates that guest attachment is on.
	// - When the host has this setting, devlxd requests to `GET /1.0/ubuntu-pro` should return the setting from the guest
	//   (`ubuntu_pro.guest_attach) and `POST /1.0/ubuntu-pro/token` should retrieve a guest token via the Ubuntu Pro client.
	// - When the guest has this setting, the pro client inside the guest will attempt to retrieve a guest token at startup.
	guestAttachSettingOn = "on"
)

// isValid returns an error if the GuestAttachSetting is not one of the pre-defined values.
func validateGuestAttachSetting(guestAttachSetting string) error {
	if !slices.Contains([]string{guestAttachSettingOff, guestAttachSettingAvailable, guestAttachSettingOn}, guestAttachSetting) {
		return fmt.Errorf("Invalid guest auto-attach setting %q", guestAttachSetting)
	}

	return nil
}

// ubuntuAdvantageDirectory is the base directory for Ubuntu Pro related configuration.
const ubuntuAdvantageDirectory = "/var/lib/ubuntu-advantage"

// Client is our wrapper for Ubuntu Pro configuration and the Ubuntu Pro CLI.
type Client struct {
	guestAttachSetting string
	monitor            fsmonitor.FSMonitor
	pro                pro
}

// pro is an internal interface that is used for mocking calls to the pro CLI.
type pro interface {
	getGuestToken(ctx context.Context) (*api.DevLXDUbuntuProGuestTokenResponse, error)
	isHostAttached(ctx context.Context) (bool, error)
}

// proCLI calls the actual Ubuntu Pro CLI to implement the interface.
type proCLI struct{}

// proAPIGetGuestTokenV1 represents the expected format of calls to `pro api u.pro.attach.guest.get_guest_token.v1`.
// Not all fields are modelled as they are not required for guest attachment functionality.
type proAPIGetGuestTokenV1 struct {
	Result string `json:"result"`
	Data   struct {
		Attributes api.DevLXDUbuntuProGuestTokenResponse `json:"attributes"`
	} `json:"data"`
	Errors []struct {
		Title string `json:"title"`
	} `json:"errors"`
}

// getTokenJSON runs `pro api u.pro.attach.guest.get_guest_token.v1` and returns the result.
func (proCLI) getGuestToken(ctx context.Context) (*api.DevLXDUbuntuProGuestTokenResponse, error) {
	// Run pro guest attach command.
	response, err := shared.RunCommand(ctx, "pro", "api", "u.pro.attach.guest.get_guest_token.v1")
	if err != nil {
		return nil, api.StatusErrorf(http.StatusServiceUnavailable, "Ubuntu Pro client command unsuccessful: %w", err)
	}

	var getGuestTokenResponse proAPIGetGuestTokenV1
	err = json.Unmarshal([]byte(response), &getGuestTokenResponse)
	if err != nil {
		return nil, api.StatusErrorf(http.StatusInternalServerError, "Received unexpected response from Ubuntu Pro contracts server: %w", err)
	}

	if getGuestTokenResponse.Result != "success" {
		if len(getGuestTokenResponse.Errors) > 0 && getGuestTokenResponse.Errors[0].Title != "" {
			return nil, api.StatusErrorf(http.StatusServiceUnavailable, "Ubuntu Pro contracts server returned %q when getting a guest token with error %q", getGuestTokenResponse.Result, getGuestTokenResponse.Errors[0].Title)
		}

		return nil, api.StatusErrorf(http.StatusServiceUnavailable, "Ubuntu Pro contracts server returned %q when getting a guest token", getGuestTokenResponse.Result)
	}

	return &getGuestTokenResponse.Data.Attributes, nil
}

// isHostAttached returns true if the host is attached to a pro subscription.
func (proCLI) isHostAttached(ctx context.Context) (bool, error) {
	// Run pro status command.
	response, err := shared.RunCommand(ctx, "pro", "status", "--format", "json")
	if err != nil {
		return false, fmt.Errorf("Ubuntu Pro client command unsuccessful: %w", err)
	}

	// Parse response.
	var statusResponse struct {
		Attached *bool `json:"attached"`
	}

	err = json.Unmarshal([]byte(response), &statusResponse)
	if err != nil {
		return false, fmt.Errorf("Received unexpected response from Ubuntu Pro client: %w", err)
	}

	if statusResponse.Attached == nil {
		return false, errors.New("Received unexpected response from Ubuntu Pro client: missing attached field")
	}

	return *statusResponse.Attached, nil
}

// New returns a new Client that watches /var/lib/ubuntu-advantage for changes to LXD configuration and contains a shim
// for the actual Ubuntu Pro CLI. If the host is not Ubuntu, it returns a static Client that always returns
// guestAttachSettingOff.
func New(ctx context.Context, osName string) *Client {
	if osName != "Ubuntu" {
		// If we're not on Ubuntu, return a static Client.
		return &Client{guestAttachSetting: guestAttachSettingOff}
	}

	s := &Client{}
	s.init(ctx, shared.HostPath(ubuntuAdvantageDirectory), proCLI{})
	return s
}

// getGuestAttachSetting returns the correct attachment setting for an instance based the on the instance configuration
// and the current GuestAttachSetting of the host.
func (s *Client) getGuestAttachSetting(instanceSetting string) string {
	// If the setting is "off" on the host then no guest attachment should take place.
	if s.guestAttachSetting == guestAttachSettingOff {
		return guestAttachSettingOff
	}

	// The `ubuntu_pro.guest_attach` setting is optional. If it is not set, return the host's guest attach setting.
	if instanceSetting == "" {
		return s.guestAttachSetting
	}

	// If the setting is not empty, check it is valid. This should have been validated already when setting the value so
	// log a warning if it is invalid.
	err := validateGuestAttachSetting(instanceSetting)
	if err != nil {
		logger.Warn("Received invalid Ubuntu Pro guest attachment setting", logger.Ctx{"setting": instanceSetting})
		return guestAttachSettingOff
	}

	return instanceSetting
}

// GuestAttachSettings returns UbuntuProSettings based on the instance configuration and the GuestAttachSetting of the host.
func (s *Client) GuestAttachSettings(instanceSetting string) api.DevLXDUbuntuProSettings {
	return api.DevLXDUbuntuProSettings{GuestAttach: s.getGuestAttachSetting(instanceSetting)}
}

// GetGuestToken returns a 403 Forbidden error if the host or the instance has guestAttachSettingOff, otherwise
// it calls the pro shim to get a token.
func (s *Client) GetGuestToken(ctx context.Context, instanceSetting string) (*api.DevLXDUbuntuProGuestTokenResponse, error) {
	if s.getGuestAttachSetting(instanceSetting) == guestAttachSettingOff {
		return nil, api.NewStatusError(http.StatusForbidden, "Guest attachment not allowed")
	}

	return s.pro.getGuestToken(ctx)
}

// init configures the Client to watch the ubuntu advantage directory for file changes.
func (s *Client) init(ctx context.Context, ubuntuAdvantageDir string, proShim pro) {
	s.pro = proShim

	// Determine if the host is attached to Ubuntu Pro and update the user agent accordingly.
	isAttached, err := s.pro.isHostAttached(ctx)
	if err != nil {
		logger.Debug("Failed to check if host is Ubuntu Pro attached", logger.Ctx{"err": err})
	} else if isAttached {
		err = version.UserAgentFeatures([]string{"pro"})
		if err != nil {
			logger.Warn("Failed to configure LXD user agent for Ubuntu Pro", logger.Ctx{"err": err})
		}
	}

	// Initial setting should be "off".
	s.guestAttachSetting = guestAttachSettingOff

	// Check that the given directory exists.
	_, err = os.Stat(ubuntuAdvantageDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Debug("Ubuntu Pro guest attachment disabled - host is Ubuntu but no Pro configuration directory exists")
		} else {
			logger.Error("Ubuntu Pro guest attachment disabled - failed to check existence of Ubuntu Pro configuration directory", logger.Ctx{"err": err})
		}

		return
	}

	// Set up a watcher on the ubuntu advantage directory.
	err = s.watch(ctx, ubuntuAdvantageDir)
	if err != nil {
		logger.Warn("Failed to configure Ubuntu Pro configuration watcher", logger.Ctx{"err": err})
	}
}

func (s *Client) watch(ctx context.Context, ubuntuAdvantageDir string) error {
	// On first call, attempt to read the contents of the config file.
	configFilePath := path.Join(ubuntuAdvantageDir, "interfaces", "lxd-config.json")
	err := s.parseConfigFile(configFilePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		logger.Warn("Failed to read Ubunto Pro LXD configuration file", logger.Ctx{"err": err})
	}

	// Watch /var/lib/ubuntu-advantage for write, remove, and rename events.
	monitor, err := drivers.Load(ctx, ubuntuAdvantageDir, fsmonitor.EventWrite, fsmonitor.EventRemove, fsmonitor.EventRename)
	if err != nil {
		return fmt.Errorf("Failed to create a file monitor: %w", err)
	}

	go func() {
		// Wait for the context to be cancelled.
		<-ctx.Done()

		// On cancel, set the guestAttachSetting back to "off" and unwatch the file.
		s.guestAttachSetting = guestAttachSettingOff
		err := monitor.Unwatch(path.Join(ubuntuAdvantageDir, "interfaces", "lxd-config.json"), "")
		if err != nil {
			logger.Warn("Failed to remove Ubuntu Pro configuration file watcher", logger.Ctx{"err": err})
		}
	}()

	// Add a hook for the config file.
	err = monitor.Watch(configFilePath, "", func(path string, event fsmonitor.Event) bool {
		if event == fsmonitor.EventRemove {
			// On remove, set guest attach to "off".
			s.guestAttachSetting = guestAttachSettingOff
			return true
		}

		// Otherwise, parse the config file and update the client accordingly.
		err := s.parseConfigFile(path)
		if err != nil {
			logger.Warn("Failed to read Ubunto Pro LXD configuration file", logger.Ctx{"err": err})
		}

		return true
	})
	if err != nil {
		return fmt.Errorf("Failed to configure file monitor: %w", err)
	}

	s.monitor = monitor
	return nil
}

// parseConfigFile reads the Ubuntu Pro `lxd-config.json` file, validates it, and sets appropriate values in the Client.
func (s *Client) parseConfigFile(lxdConfigFile string) error {
	// Default to "off" if any error occurs.
	s.guestAttachSetting = guestAttachSettingOff

	f, err := os.Open(lxdConfigFile)
	if err != nil {
		return fmt.Errorf("Failed to open Ubuntu Pro configuration file: %w", err)
	}

	defer f.Close()

	var settings api.DevLXDUbuntuProSettings
	err = json.NewDecoder(f).Decode(&settings)
	if err != nil {
		return fmt.Errorf("Failed to read Ubuntu Pro configuration file: %w", err)
	}

	err = validateGuestAttachSetting(settings.GuestAttach)
	if err != nil {
		return fmt.Errorf("Failed to read Ubuntu Pro configuration file: %w", err)
	}

	s.guestAttachSetting = settings.GuestAttach
	return nil
}
