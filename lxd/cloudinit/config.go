package cloudinit

import (
	"bufio"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.yaml.in/yaml/v2"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// sshKeyExtendedConfigTag defines comment to be added on the side of added keys.
var sshKeyExtendedConfigTag = "#lxd:cloud-init.ssh-keys"

// VendorDataKeys contains the keys used to store cloud-init's vendor-data.
var VendorDataKeys = []string{"cloud-init.vendor-data", "user.vendor-data"}

// UserDataKeys contains the keys used to store cloud-init's user-data.
var UserDataKeys = []string{"cloud-init.user-data", "user.user-data"}

// GetEffectiveConfigKey gets the correct config key for some type of cloud-init configuration.
// Supported configTypes are "user-data", "vendor-data" or "network-config".
func GetEffectiveConfigKey(instanceConfig map[string]string, configType string) string {
	// cloud-init.* keys take precedence over user.* ones
	key := "cloud-init." + configType
	value := instanceConfig["cloud-init."+configType]
	// If cloud-init.* is not defined but user.* is, fallback on the latter.
	if value == "" {
		fallbackKey := "user." + configType
		value = instanceConfig[fallbackKey]
		if value != "" {
			key = fallbackKey
		}
	}

	return key
}

// Config contains the user-data and vendor-data used as configuration data for cloud-init.
type Config struct {
	UserData   string
	VendorData string
}

// GetEffectiveConfig returns the resulting vendor-data and/or user-data for a certain instance.
// This method takes in an optional requestedKey that point either user-data or vendor-data. If no requiredKey is
// provided, it is understood that the caller wants the resulting values for both [vendor|user]-data.
func GetEffectiveConfig(instanceConfig map[string]string, requestedKey string, instanceName string, instanceProject string) (config Config) {
	// Assign requestedKey according to the type of seed data it refers to.
	vendorKeyProvided := slices.Contains(VendorDataKeys, requestedKey)
	userKeyProvided := slices.Contains(UserDataKeys, requestedKey)

	var vendorDataKey string
	var userDataKey string

	if vendorKeyProvided {
		vendorDataKey = requestedKey
	} else {
		vendorDataKey = GetEffectiveConfigKey(instanceConfig, "vendor-data")
	}

	if userKeyProvided {
		userDataKey = requestedKey
	} else {
		userDataKey = GetEffectiveConfigKey(instanceConfig, "user-data")
	}

	// Extract additional SSH keys to merge into cloud-config.
	userKeys := extractAdditionalSSHKeys(instanceConfig)

	var vendorErr error
	var userErr error

	// Defer logging a warning for each desired output in case of failing to merge SSH keys into cloud-init data due to a parsing error.
	// An output is considered desired if it is the expected output for requestedKey's effective value or no requestedKey was provided.
	defer func() {
		if len(userKeys) > 0 && (requestedKey == "" || vendorKeyProvided) && vendorErr != nil {
			logger.Warn("Failed merging SSH keys into cloud-init seed data, abstain from injecting additional keys", logger.Ctx{"err": vendorErr, "project": instanceProject, "instance": instanceName, "dataConfigKey": vendorDataKey})
		}

		if len(userKeys) > 0 && (requestedKey == "" || userKeyProvided) && userErr != nil {
			logger.Warn("Failed merging SSH keys into cloud-init seed data, abstain from injecting additional keys", logger.Ctx{"err": userErr, "project": instanceProject, "instance": instanceName, "dataConfigKey": vendorDataKey})
		}
	}()

	// Parse data from instance config.
	vendorCloudConfig, vendorErr := parseCloudConfig(instanceConfig[vendorDataKey])
	userCloudConfig, userErr := parseCloudConfig(instanceConfig[userDataKey])

	// user-data's fields overwrite vendor-data's fields, so merging SSH keys can result in adding a "users" field
	// that did not exist before, having the side effect of overwriting vendor-data's "users" field.
	// So only merge into "user-data" when safe to do.
	canMergeUserData := (userErr == nil && userCloudConfig.hasUsers()) || vendorErr != nil || !vendorCloudConfig.hasUsers()

	// Merge additional SSH keys into parsed config.
	// If merging is not possible return the raw value for the target key.
	if requestedKey == "" || vendorKeyProvided {
		if vendorErr == nil {
			config.VendorData, vendorErr = vendorCloudConfig.mergeSSHKeyCloudConfig(userKeys)
		}

		if config.VendorData == "" {
			config.VendorData = instanceConfig[vendorDataKey]
		}
	}

	if requestedKey == "" || userKeyProvided {
		if userErr == nil && canMergeUserData {
			config.UserData, userErr = userCloudConfig.mergeSSHKeyCloudConfig(userKeys)
		}

		if config.UserData == "" {
			config.UserData = instanceConfig[userDataKey]
		}
	}

	return config
}

// parseCloudConfig attempts to unmarshal a string into a cloudConfig object. Returns an error if the
// provided string is not a valid YAML or lacks the needed "#cloud-config" comment.
func parseCloudConfig(rawCloudConfig string) (*cloudConfig, error) {
	// Check if rawCloudConfig is in a supported format.
	// A YAML cloud config without #cloud-config is invalid.
	// The "#cloud-config" tag can be either on the first or second lines.
	if rawCloudConfig != "" && !slices.Contains(shared.SplitNTrimSpace(rawCloudConfig, "\n", 3, false), "#cloud-config") {
		return nil, errors.New(`Parsing configuration is not supported as it is not "#cloud-config"`)
	}

	// Parse YAML cloud-config into map.
	cloudConfigMap := cloudConfig{
		comments: "",
		keys:     make(map[any]any),
	}

	// Parse the initial comments in the cloud config file.
	scanner := bufio.NewScanner(strings.NewReader(rawCloudConfig))
	for scanner.Scan() {
		if !strings.HasPrefix(scanner.Text(), "#") {
			break
		}

		cloudConfigMap.comments += scanner.Text() + "\n"
	}

	err := yaml.Unmarshal([]byte(rawCloudConfig), cloudConfigMap.keys)
	if err != nil {
		return nil, fmt.Errorf("Could not unmarshall cloud-config: %w", err)
	}

	return &cloudConfigMap, nil
}

// userSSHKeys is a struct that keeps the SSH keys to be injected using cloud-init for a certain user.
type userSSHKeys struct {
	importIDs  []string
	publicKeys []string
}

// extractAdditionalSSHKeys extracts additional SSH keys from the instance config.
// Returns a map of userSSHKeys keyed on the name of the user that the keys should be injected for.
func extractAdditionalSSHKeys(instanceConfig map[string]string) map[string]*userSSHKeys {
	// Use a pointer to userSSHKeys so we can append to its fields.
	users := make(map[string]*userSSHKeys)

	// Populate map of userSSHKeys.
	for key, value := range instanceConfig {
		if strings.HasPrefix(key, "cloud-init.ssh-keys.") {
			user, sshKey, found := strings.Cut(value, ":")

			// If the "cloud-init.ssh-keys." is badly formatted or is "none", skip it.
			if !found {
				continue
			}

			// Create an empty userSSHKeys if the user is not configured.
			_, ok := users[user]
			if !ok {
				users[user] = &userSSHKeys{}
			}

			// Check if ssh key is an import ID with with the "keyServer:UserName".
			// This is done by checking if the value does not contain a space which is always present in
			// valid public key representations and never present on import IDs.
			if !strings.Contains(sshKey, " ") {
				users[user].importIDs = append(users[user].importIDs, sshKey)
				continue
			}

			users[user].publicKeys = append(users[user].publicKeys, sshKey)
		}
	}

	return users
}

// cloudConfig represents a cloud-config parsed into a map.
type cloudConfig struct {
	comments string
	keys     map[any]any
}

// string marshals a cloud-config map into a YAML string.
func (config *cloudConfig) string() (string, error) {
	resultingConfigBytes, err := yaml.Marshal(config.keys)
	if err != nil {
		return "", err
	}

	// If there was no config provided, generate one.
	if config.comments == "" {
		config.comments = "#cloud-config\n"
	}

	// Add cloud-config tag and space before comments, as doing the latter
	// while parsing would result in the comment to be included in the value on the same line.
	resultingConfig := config.comments + strings.ReplaceAll(string(resultingConfigBytes), sshKeyExtendedConfigTag, " "+sshKeyExtendedConfigTag)
	return resultingConfig, nil
}

// string marshals a cloud-config map into a YAML string.
func (config *cloudConfig) hasUsers() bool {
	value, ok := config.keys["users"]
	return ok && value != ""
}

// mergeSSHKeyCloudConfig merges keys present in a map of userSSHKeys into a CloudConfig.
// The provided map can be obtained by extracting user keys from an instance config with extractAdditionalSSHKeys.
// This also returns the resulting YAML string after the merging is done.
func (config *cloudConfig) mergeSSHKeyCloudConfig(userKeys map[string]*userSSHKeys) (string, error) {
	// If no keys are defined, return the original config passed in.
	if len(userKeys) == 0 {
		return config.string()
	}

	// Get previously defined users list in provided config, if present.
	userList, err := findOrCreateListInMap(config.keys, "users")
	if err != nil {
		return "", err
	}

	// Define comment to be added on the side of added keys.
	sshKeyExtendedConfigTag := "#lxd:cloud-init.ssh-keys"

	// Merge the specified additional keys into the provided cloud config.
	for user, keys := range userKeys {
		var targetUser map[any]any

		for index, field := range userList {
			mapField, ok := field.(map[any]any)

			// The user has to be either a mapping yaml node or a simple string indicating the name of a user to be created.
			if !ok {
				// If the field is not the user name we want, skip this one.
				userName, isString := field.(string)
				if isString && userName == user {
					// Else, create a user map for us to add the keys into. Use the previously defined name as the name in the user map.
					targetUser = make(map[any]any)
					targetUser["name"] = userName
					userList[index] = targetUser
					break
				} else if !isString {
					return "", errors.New("Invalid user item on users list")
				}
			} else if mapField["name"] == user {
				// If it is a map, check the name.
				targetUser = mapField
				break
			}
		}

		// If the target user was not present in the cloud config, create an entry for it.
		if targetUser == nil {
			targetUser = make(map[any]any)
			targetUser["name"] = user
			userList = append(userList, targetUser)
		}

		// Using both the older and newer keys, since we do not know what version of cloud-init will be consuming this.
		sshAuthorizedKeys := []string{"ssh_authorized_keys", "ssh-authorized-keys"}
		importIDKeys := []string{"ssh_import_id", "ssh-import-id"}

		// Add public keys to cloud-config.
		err = addValueToListsInMap(targetUser, keys.publicKeys, sshAuthorizedKeys, sshKeyExtendedConfigTag)
		if err != nil {
			return "", err
		}

		// Add import IDs to cloud-config.
		err = addValueToListsInMap(targetUser, keys.importIDs, importIDKeys, sshKeyExtendedConfigTag)
		if err != nil {
			return "", err
		}
	}

	// Only modify the original config map if everything went well.
	config.keys["users"] = userList
	return config.string()
}

// addValueToListsInMap finds or creates a list referenced on the provided user map for each key on fieldKeys
// and adds all provided values along with addedValueTag on the side to mark added values.
// addedKeyTag is simply appended to the values added, any parsing to separate the tag from the
// value should be done outside this function.
func addValueToListsInMap(user map[any]any, addedValues []string, fieldKeys []string, addedValueTag string) error {
	// If there are no keys to add, this function should be a no-op.
	if len(addedValues) == 0 {
		return nil
	}

	for _, fieldKey := range fieldKeys {
		// Get the field with the provided key, if it exists.
		// If it does not exist, create it as an empty list.
		// If it exists and is not a list, switch it for a list containing the previously defined value.
		targetList, err := findOrCreateListInMap(user, fieldKey)
		if err != nil {
			return err
		}

		// Add the keys to the lists that will not be filled with an alias afterwards.
		// Do not add if the key is already present on the slice and mark added keys.
		for _, key := range addedValues {
			if !slices.Contains(targetList, any(key)) {
				targetList = append(targetList, key+addedValueTag)
			}
		}

		// Update the map with the slice with appended keys.
		user[fieldKey] = targetList
	}

	return nil
}

// findOrCreateListInMap finds a list under the provided key on a map that represents a YAML map field.
// If there is no value for the provided key, this returns a slice than can be used for the key, but
// this function does not change the provided map.
// If the value under the key is a string, the returned slice will contain it.
// If the value for the key is of any other type, return an error.
func findOrCreateListInMap(yamlMap map[any]any, key string) ([]any, error) {
	// Get previously defined list in provided map, if present.
	field, hasField := yamlMap[key]
	listField, isSlice := field.([]any)
	_, isString := field.(string)

	// If the field under the key is set to something other than a list or a string, both of which
	// would be valid, return an error.
	if hasField && !isSlice && !isString {
		return nil, fmt.Errorf("Invalid value under %q", key)
	}

	// If provided map did not include a field under the key or included one that was simply a string and
	// not a list, create a slice.
	if !hasField || isString {
		listField = make([]any, 0)
		// Preserve the previous string field as an item on the new list so it is still applied.
		if isString {
			listField = append(listField, field)
		}
	}

	return listField, nil
}
