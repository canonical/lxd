package cloudinit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/canonical/lxd/shared"
	"gopkg.in/yaml.v2"
)

// cloudInitUserSSHKeys is a struct that keeps the SSH keys to be injected using cloud-init for a certain user.
type cloudInitUserSSHKeys struct {
	importIDs  []string
	publicKeys []string
}

// MergeSSHKeyCloudConfig merges any existing SSH keys defined in an instance config into a provided
// cloud-config YAML string.
// In the case where we were not able to parse the cloud config, return the original, unchanged config and
// the error.
func MergeSSHKeyCloudConfig(instanceConfig map[string]string, cloudConfig string) (string, error) {
	// Use a pointer to cloudInitUserSSHKeys so we can append to its fields.
	users := make(map[string]*cloudInitUserSSHKeys)

	for key, value := range instanceConfig {
		if strings.HasPrefix(key, "cloud-init.ssh-keys.") {
			user, sshKey, found := strings.Cut(value, ":")

			// If the "cloud-init.ssh-keys." is badly formatted, skip it.
			if !found {
				continue
			}

			// Create an empty cloudInitUserSSHKeys if the user is not configured.
			_, ok := users[user]
			if !ok {
				users[user] = &cloudInitUserSSHKeys{}
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

	// If no keys are defined, return the original config passed in.
	if len(users) == 0 {
		return cloudConfig, nil
	}

	// Parse YAML cloud-config into map.
	cloudConfigMap := make(map[any]any)
	err := yaml.Unmarshal([]byte(cloudConfig), cloudConfigMap)
	if err != nil {
		return cloudConfig, fmt.Errorf("Could not unmarshall cloud-config: %w", err)
	}

	// Get previously defined users list in provided config, if present.
	userList, err := findOrCreateListInMap(cloudConfigMap, "users")
	if err != nil {
		return cloudConfig, err
	}

	// Define comment to be added on the side of added keys.
	sshKeyExtendedConfigTag := "#lxd:cloud-init.ssh-keys"

	// Merge the specified additional keys into the provided cloud config.
	for user, keys := range users {
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
					return cloudConfig, errors.New("Invalid user item on users list")
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
			return cloudConfig, err
		}

		// Add import IDs to cloud-config.
		err = addValueToListsInMap(targetUser, keys.importIDs, importIDKeys, sshKeyExtendedConfigTag)
		if err != nil {
			return cloudConfig, err
		}
	}

	// Only modify the original config map if everything went well.
	cloudConfigMap["users"] = userList
	resultingConfigBytes, err := yaml.Marshal(cloudConfigMap)
	if err != nil {
		return cloudConfig, err
	}

	// Add cloud-config tag and space before comments, as doing the latter
	// while parsing would result in the comment to be included in the value on the same line.
	resultingConfig := "#cloud-config\n" + strings.ReplaceAll(string(resultingConfigBytes), sshKeyExtendedConfigTag, " "+sshKeyExtendedConfigTag)
	return resultingConfig, nil
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
			if !shared.ValueInSlice(any(key), targetList) {
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
