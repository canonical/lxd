package cloudinit

import (
	"testing"
)

func TestMergeSSHKeyCloudConfig(t *testing.T) {
	instanceConfig := map[string]string{"cloud-init.ssh-keys.mykey": "root:gh:user1"}

	// First try with an empty cloud-config.
	out, err := MergeSSHKeyCloudConfig(instanceConfig, "")
	if err != nil {
		t.Fatal(err)
	}

	expectedOutput := `#cloud-config
users:
- name: root
  ssh-import-id:
  - gh:user1 #lxd:cloud-init.ssh-keys
  ssh_import_id:
  - gh:user1 #lxd:cloud-init.ssh-keys
`

	if expectedOutput != out {
		t.Fatalf("Output %q is different from expected %q", out, expectedOutput)
	}

	invalidCloudConfig := `#cloud-config
users:
	- name: root
ssh-import-id: gh:user2
`

	// Check merging into invalid config returns an error.
	_, err = MergeSSHKeyCloudConfig(instanceConfig, invalidCloudConfig)
	if err == nil {
		t.Fatal("Parsing invalid config did not return an error")
	}

	cloudConfig := `#cloud-config
users:
    - name: root
      ssh-import-id: gh:user2
      ssh-authorized-keys: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPfOyl6A6lSE+e57RLf4GwDzlg6PALjtiweokxQeCPL0
      shell: /bin/bash
`

	// Merge the instance config into a cloud-config that already contain some keys
	out, err = MergeSSHKeyCloudConfig(instanceConfig, cloudConfig)
	if err != nil {
		t.Fatal(err)
	}

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

	if expectedOutput != out {
		t.Fatalf("Output %q is different from expected %q", out, expectedOutput)
	}

	// Add a pure public key to instance config.
	instanceConfig["cloud-init.ssh-keys.otherkey"] = "user:ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPfOyl6A6lSE+e57RLf4GwDzlg6PALjtiweokxQeCPL0"

	scalarUserCloudConfig := `#cloud-config
users: foo
`

	// Merge the extended instance config with a cloud-config with a simple users string.
	out, err = MergeSSHKeyCloudConfig(instanceConfig, scalarUserCloudConfig)
	if err != nil {
		t.Fatal(err)
	}

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
	if expectedOutput+rootUserConfig+customUserConfig != out && expectedOutput+customUserConfig+rootUserConfig != out {
		t.Fatalf("Output %q is different from expected %q", out, expectedOutput)
	}
}
