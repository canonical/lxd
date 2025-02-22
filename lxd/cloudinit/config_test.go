package cloudinit

import (
	"testing"
)

func TestGetEffectiveConfig(t *testing.T) {
	instanceConfig := map[string]string{"cloud-init.ssh-keys.mykey": "root:gh:user1"}

	// Parsing an invalid config should leave it unchanged.
	instanceConfig["user.vendor-data"] = `users:
    - name: root
      ssh-import-id: gh:user2
`

	cloudInitConfig := GetEffectiveConfig(instanceConfig, "", "instance", "project")

	// Parsing invalid cloud-config should leave it unchanged.
	if cloudInitConfig.VendorData != instanceConfig["user.vendor-data"] {
		t.Fatalf("Output %q is different from expected %q", cloudInitConfig.VendorData, instanceConfig["user.vendor-data"])
	}

	expectedOutput := `#cloud-config
users:
- name: root
  ssh-import-id:
  - gh:user1 #lxd:cloud-init.ssh-keys
  ssh_import_id:
  - gh:user1 #lxd:cloud-init.ssh-keys
`

	if cloudInitConfig.UserData != expectedOutput {
		t.Fatalf("Output %q is different from expected %q", cloudInitConfig.UserData, expectedOutput)
	}

	instanceConfig["cloud-init.vendor-data"] = `#cloud-config
users:
    - name: root
      ssh-import-id: gh:user2
      ssh-authorized-keys: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPfOyl6A6lSE+e57RLf4GwDzlg6PALjtiweokxQeCPL0
      shell: /bin/bash
`

	cloudInitConfig = GetEffectiveConfig(instanceConfig, "", "instance", "project")

	expectedOutput = `#cloud-config
users:
- name: root
  shell: /bin/bash
  ssh-authorized-keys: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPfOyl6A6lSE+e57RLf4GwDzlg6PALjtiweokxQeCPL0
  ssh-import-id:
  - gh:user2
  - gh:user1 #lxd:cloud-init.ssh-keys
  ssh_import_id:
  - gh:user1 #lxd:cloud-init.ssh-keys
`

	if cloudInitConfig.VendorData != expectedOutput {
		t.Fatalf("Output %q is different from expected %q", cloudInitConfig.VendorData, expectedOutput)
	}

	// Should not merge to user-data since vendor-data has "users" defined.
	if cloudInitConfig.UserData != "" {
		t.Fatalf(`Output %q is different from expected ""`, cloudInitConfig.UserData)
	}

	// Add a pure public key to instance config.
	instanceConfig["cloud-init.ssh-keys.otherkey"] = "user:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPfOyl6A6lSE+e57RLf4GwDzlg6PALjtiweokxQeCPL0"
	instanceConfig["cloud-init.user-data"] = `#cloud-config
users: foo
`

	cloudInitConfig = GetEffectiveConfig(instanceConfig, "", "instance", "project")

	expectedOutput = `#cloud-config
users:
- foo
`

	rootUserConfig := `- name: root
  ssh-import-id:
  - gh:user1 #lxd:cloud-init.ssh-keys
  ssh_import_id:
  - gh:user1 #lxd:cloud-init.ssh-keys
`

	customUserConfig := `- name: user
  ssh-authorized-keys:
  - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPfOyl6A6lSE+e57RLf4GwDzlg6PALjtiweokxQeCPL0 #lxd:cloud-init.ssh-keys
  ssh_authorized_keys:
  - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPfOyl6A6lSE+e57RLf4GwDzlg6PALjtiweokxQeCPL0 #lxd:cloud-init.ssh-keys
`

	// The order of maps inside a list is not predicatable during YAML marshalling, so the order
	// of users can change and generate two different but equivalent results.
	if expectedOutput+rootUserConfig+customUserConfig != cloudInitConfig.UserData && expectedOutput+customUserConfig+rootUserConfig != cloudInitConfig.UserData {
		t.Fatalf("Output %q conflicts with expected %q", cloudInitConfig.UserData, expectedOutput+rootUserConfig+customUserConfig)
	}
}
