test_network_acl() {
  ensure_import_testimage
  ensure_has_localhost_remote "${LXD_ADDR}"

  firewallDriver=$(lxc info | awk -F ":" '/firewall:/{gsub(/ /, "", $0); print $2}')
  netName=lxdt$$

  lxc network create "${netName}" \
        ipv4.address=192.0.2.1/24 \
        ipv6.address=fd42:4242:4242:1010::1/64

  # Check basic ACL creation, listing, deletion and project namespacing support.
  ! lxc network acl create 192.168.1.1 || false # Don't allow non-hostname compatible names.
  lxc network acl create testacl
  lxc project create testproj -c features.networks=true
  lxc project create testproj2 -c features.networks=false
  lxc project create testproj3 -c features.networks=true
  lxc network acl create testacl --project testproj
  lxc network acl create testacl2 --project testproj3
  [ "$(lxc project show testproj | grep -cwF 'testacl')" = 1 ] # Check project sees testacl using it.
  ! lxc network acl create testacl --project testproj2 || false
  [ "$(lxc network acl ls -f csv | grep -cwF 'testacl')" = 1 ]
  [ "$(lxc network acl ls -f csv --project testproj | grep -cwF 'testacl')" = 1 ]
  [ "$(lxc network acl ls --project testproj3 -f csv | grep -cwF 'testacl2')" = 1 ]
  [ "$(lxc network acl ls --all-projects -f csv | grep -cwF 'testacl2')" = 1 ]
  ! lxc network acl ls -f csv | grep -wF 'testacl2' || false
  lxc network acl delete testacl
  lxc network acl delete testacl --project testproj
  lxc network acl delete testacl2 --project testproj3
  [ "$(lxc network acl ls -f csv || echo fail)" = "" ]
  [ "$(lxc network acl ls -f csv || echo fail)" = "" ]
  [ "$(lxc network acl ls -f csv --project testproj || echo fail)" = "" ]
  [ "$(lxc network acl ls --project testproj3 -f csv || echo fail)" = "" ]
  [ "$(lxc network acl ls --all-projects -f csv || echo fail)" = "" ]
  lxc project delete testproj
  lxc project delete testproj3

  # ACL creation from stdin.
  cat <<EOF | lxc network acl create testacl
description: Test ACL
egress: []
ingress:
- action: allow
  source: 192.168.1.1/32
  destination: 192.168.1.2/32
  protocol: tcp
  source_port: ""
  destination_port: "22"
  icmp_type: ""
  icmp_code: ""
  description: ""
  state: enabled
config:
  user.mykey: foo
EOF
  acl_show_output=$(lxc network acl show testacl)
  [ "$(echo "$acl_show_output" | grep -cF 'description: Test ACL')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'action: allow')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'source: 192.168.1.1/32')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'destination: 192.168.1.2/32')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'destination_port: "22"')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'user.mykey: foo')" = 1 ]

  # ACL Patch. Check for merged config and replaced description, ingress and egress fields.
  lxc query -X PATCH -d '{"config": {"user.myotherkey": "bah"}}' /1.0/network-acls/testacl
  acl_show_output=$(lxc network acl show testacl)
  [ "$(echo "$acl_show_output" | grep -cF 'user.mykey: foo')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'user.myotherkey: bah')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'description: ""')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'ingress: []')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'egress: []')" = 1 ]

  # ACL edit from stdin.
  cat <<EOF | lxc network acl edit testacl
description: Test ACL updated
egress: []
ingress:
- action: allow
  source: 192.168.1.1/32
  destination: 192.168.1.2/32
  protocol: tcp
  source_port: ""
  destination_port: "22"
  icmp_type: ""
  icmp_code: ""
  description: "a rule description"
  state: enabled
config:
  user.mykey: foo
EOF
  acl_show_output=$(lxc network acl show testacl)
  [ "$(echo "$acl_show_output" | grep -cF 'description: Test ACL updated')" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'description: a rule description')" = 1 ]

  # ACL rule addition.
  ! lxc network acl rule add testacl outbound || false # Invalid direction
  ! lxc network acl rule add testacl ingress invalidfield=foo || false # Invalid field
  ! lxc network acl rule add testacl ingress action=accept || false # Invalid action
  ! lxc network acl rule add testacl ingress action=allow state=foo || false # Invalid state
  ! lxc network acl rule add testacl ingress action=allow source=foo || false # Invalid source
  ! lxc network acl rule add testacl ingress action=allow destination=foo || false # Invalid destination
  ! lxc network acl rule add testacl ingress action=allow source_port=foo || false # Invalid source port
  ! lxc network acl rule add testacl ingress action=allow destination_port=foo || false # Invalid destination port
  ! lxc network acl rule add testacl ingress action=allow source_port=999999999 || false # Invalid source port
  ! lxc network acl rule add testacl ingress action=allow destination_port=999999999 || false # Invalid destination port
  ! lxc network acl rule add testacl ingress action=allow protocol=foo || false # Invalid protocol
  ! lxc network acl rule add testacl ingress action=allow protocol=udp icmp_code=1 || false # Invalid icmp combination
  ! lxc network acl rule add testacl ingress action=allow protocol=icmp4 icmp_code=256 || false # Invalid icmp combination
  ! lxc network acl rule add testacl ingress action=allow protocol=icmp6 icmp_type=-1 || false # Invalid icmp combination

  echo "iptables does not support ipranges (192.168.1.1-192.168.1.3) so use CIDR instead"
  daddr="192.168.1.1-192.168.1.3"
  if [ "$firewallDriver" = "xtables" ]; then
    daddr="192.168.1.0/24"
  fi

  lxc network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination="${daddr}" destination_port="22, 2222-2223"
  ! lxc network acl rule add testacl ingress action=allow source=192.168.1.2/32 protocol=tcp destination="${daddr}" destination_port=22,2222-2223 || false # Dupe rule detection
  acl_show_output=$(lxc network acl show testacl)
  [ "$(echo "$acl_show_output" | grep -cF "destination: ${daddr}")" = 1 ]
  [ "$(echo "$acl_show_output" | grep -cF 'state: enabled')" -ge 2 ] # Default state enabled for new rules.

  echo "Apply ACL to network"
  lxc network set "${netName}" security.acls=testacl

  echo "Verify corresponding firewall rules"
  if [ "$firewallDriver" = "xtables" ]; then
    iptables -w -S | grep -xF -- "-A lxd_acl_${netName} -s 192.168.1.2/32 -d ${daddr} -o ${netName} -p tcp -m multiport --dports 22,2222:2223 -j ACCEPT"
  else
    nft -nn list chain inet lxd "acl.${netName}" | grep -F "oifname \"${netName}\" ip saddr 192.168.1.2 ip daddr ${daddr} tcp dport { 22, 2222-2223 } accept"
  fi

  echo "Stop applying ACL to test network"
  lxc network unset "${netName}" security.acls

  echo "Delete test network"
  lxc network delete "${netName}"

  # ACL rule removal.
  lxc network acl rule add testacl ingress action=allow source=192.168.1.3/32 protocol=tcp destination="${daddr}" destination_port=22,2222-2223 description="removal rule test"
  ! lxc network acl rule remove testacl ingress || false # Fail if match multiple rules with no filter and no --force.
  ! lxc network acl rule remove testacl ingress destination_port=22,2222-2223 || false # Fail if match multiple rules with filter and no --force.
  lxc network acl rule remove testacl ingress description="removal rule test" # Single matching rule removal.
  ! lxc network acl rule remove testacl ingress description="removal rule test" || false # No match for removal fails.
  lxc network acl rule remove testacl ingress --force # Remove all ingress rules.
  [ "$(lxc network acl show testacl | grep -cF 'ingress: []')" = 1 ] # Check all ingress rules removed.

  # ACL rename.
  ! lxc network acl rename testacl 192.168.1.1 || false # Don't allow non-hostname compatible names.
  lxc network acl rename testacl testacl2
  lxc network acl show testacl2

  # ACL custom config.
  lxc network acl set testacl2 user.somekey foo
  [ "$(lxc network acl get testacl2 user.somekey | grep -cwF 'foo')" = 1 ]
  ! lxc network acl set testacl2 non.userkey || false
  lxc network acl unset testacl2 user.somekey
  [ "$(lxc network acl get testacl2 user.somekey || echo fail)" = "" ]

  lxc network acl delete testacl2
}
