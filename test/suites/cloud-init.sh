test_cloud_init() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  lxc init testimage c1
  ID1=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID1}" ]

  lxc rename c1 c2
  ID2=$(lxc config get c2 volatile.cloud-init.instance-id)
  [ -n "${ID2}" ] && [ "${ID2}" != "${ID1}" ]

  lxc copy c2 c1
  ID3=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID3}" ] && [ "${ID3}" != "${ID2}" ]

  lxc config set c1 cloud-init.user-data blah
  ID4=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID4}" ] && [ "${ID4}" != "${ID3}" ]

  lxc config device override c1 eth0 user.foo=bar
  ID5=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ "${ID5}" = "${ID4}" ]

  lxc config device set c1 eth0 name=foo
  ID6=$(lxc config get c1 volatile.cloud-init.instance-id)
  [ -n "${ID6}" ] && [ "${ID6}" != "${ID5}" ]

  lxc delete -f c1 c2

  lxc launch testimage devlxd
  lxc file push --quiet "$(command -v devlxd-client)" devlxd/bin/

  echo "Check that unknown cloud-init format is passed to the instance unmodified"
  lxc config set devlxd cloud-init.user-data="invalid-yaml"
  [ "$(lxc exec devlxd -- devlxd-client cloud-init user-data)" = "invalid-yaml" ]
  lxc config unset devlxd cloud-init.user-data

  echo  "Check that with SSH keys configured, unknown cloud-init format is passed to the instance unmodified"
  lxc config set devlxd cloud-init.user-data="invalid-yaml"
  lxc config set devlxd cloud-init.ssh-keys.mykey="root:gh:user1"
  [ "$(lxc exec devlxd -- devlxd-client cloud-init user-data)" = "invalid-yaml" ]
  lxc config unset devlxd cloud-init.ssh-keys.mykey
  lxc config unset devlxd cloud-init.user-data

  echo "Check that configured ssh-keys in user-data do not replace vendor-data with users provided"
  CLOUD_INIT="#cloud-config
users:
  - name: root
    ssh-import-id: gh:user2"

  lxc config set devlxd cloud-init.vendor-data="${CLOUD_INIT}"
  lxc config set devlxd cloud-init.ssh-keys.mykey="root:gh:user1"
  VENDOR_DATA="$(lxc exec devlxd -- devlxd-client cloud-init vendor-data)"
  grep "gh:user1" <<< "${VENDOR_DATA}"
  grep "gh:user2" <<< "${VENDOR_DATA}"
  [ "$(lxc exec devlxd -- devlxd-client cloud-init user-data)" = "Not Found" ]
  lxc config unset devlxd cloud-init.ssh-keys.mykey
  lxc config unset devlxd cloud-init.vendor-data

  echo  "Check that valid jinja template config works with ssh keys configured"
  CLOUD_INIT="## template: jinja
#cloud-config
runcmd:
  - echo {{devlxd.local_hostname}} > /var/tmp/runcmd_output
  - echo {{merged_system_cfg._doc}} >> /var/tmp/runcmd_output"
  lxc config set devlxd cloud-init.user-data="${CLOUD_INIT}"
  lxc config set devlxd cloud-init.ssh-keys.mykey="root:gh:user1"
  EXPECTED_CLOUD_INIT="## template: jinja
#cloud-config
runcmd:
- echo {{devlxd.local_hostname}} > /var/tmp/runcmd_output
- echo {{merged_system_cfg._doc}} >> /var/tmp/runcmd_output
users:
- name: root
  ssh-import-id:
  - gh:user1 #lxd:cloud-init.ssh-keys
  ssh_import_id:
  - gh:user1 #lxd:cloud-init.ssh-keys"
  [ "$(lxc exec devlxd -- devlxd-client cloud-init user-data)" = "${EXPECTED_CLOUD_INIT}" ]

  lxc delete -f devlxd
}
