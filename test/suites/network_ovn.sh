test_network_ovn() {
  if [ -z "${LXD_OVN_NB_CONNECTION:-}" ]; then
    echo "OVN northbound connection not set. Skipping OVN tests..."
    return
  fi

  # Create an associative array holding table names and the expected number of rows for that table.
  declare -A tables

  reset_row_count() {
    # Populate the array with expected initial values (must be empty).
    # The "Conn" table may have a row depending on the protocol in the connection (so we ignore it).
    tables["ACL"]=0
    tables["Address_Set"]=0
    tables["BFD"]=0
    tables["Copp"]=0
    tables["DHCP_Options"]=0
    tables["DNS"]=0
    tables["Forwarding_Group"]=0
    tables["Gateway_Chassis"]=0
    tables["HA_Chassis"]=0
    tables["HA_Chassis_Group"]=0
    tables["Load_Balancer"]=0
    tables["Load_Balancer_Group"]=0
    tables["Load_Balancer_Health_Check"]=0
    tables["Logical_Router"]=0
    tables["Logical_Router_Policy"]=0
    tables["Logical_Router_Port"]=0
    tables["Logical_Router_Static_Route"]=0
    tables["Logical_Switch"]=0
    tables["Logical_Switch_Port"]=0
    tables["Meter"]=0
    tables["Meter_Band"]=0
    tables["NAT"]=0
    tables["NB_Global"]=0
    tables["Port_Group"]=0
    tables["QoS"]=0
    tables["SSL"]=0

    # NB_Global should always have one row.
    tables["NB_Global"]=1
  }

  # Function to assert that the associative array matches what is in the northbound database.
  assert_row_count() {
    for table_name in "${!tables[@]}"; do
      count="${tables[${table_name}]}"
      echo "Checking ${table_name} has ${count} rows..."
      [ "$(ovn-nbctl --format csv --no-headings list "${table_name}" | wc -l)" = "${count}" ]
    done
  }

  # Validate northbound database is initially empty (so we don't inadvertently break anything).
  reset_row_count
  assert_row_count

  # This assertions tests that the "BridgeExists" function works correctly and can find the integration bridge.
  # If the OVS package is not able to find the bridge, the "type" field is "unknown" instead of "bridge".
  [ "$(lxc query /1.0/networks/br-int | jq -er '.type')" = "bridge" ]

  lxc config set network.ovn.northbound_connection "${LXD_OVN_NB_CONNECTION}"

  # If the connection uses SSL we have more required environment variables.
  # Set the client cert, key, and CA cert.
  if [[ "${LXD_OVN_NB_CONNECTION}" =~ ^ssl: ]]; then
    lxc config set network.ovn.client_cert="$(< "${LXD_OVN_NB_CLIENT_CRT_FILE}")"
    lxc config set network.ovn.client_key="$(< "${LXD_OVN_NB_CLIENT_KEY_FILE}")"
    lxc config set network.ovn.ca_cert="$(< "${LXD_OVN_NB_CA_CRT_FILE}")"
  fi

  # Create a bridge for use as an uplink.
  uplink_network="uplink$$"
  lxc network create "${uplink_network}" \
      ipv4.address=10.10.10.1/24 ipv4.nat=true \
      ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
      ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
      ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
      ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254 \
      ipv4.routes=192.0.2.0/24 ipv6.routes=2001:db8:1:2::/64

  # Create an OVN network.
  ovn_network="ovn$$"
  lxc network create "${ovn_network}" --type ovn network="${uplink_network}" \
      ipv4.address=10.24.140.1/24 ipv4.nat=true \
      ipv6.address=fd42:bd85:5f89:5293::1/64 ipv6.nat=true

  # Check this created the correct number of entries.
  tables["ACL"]=15
  tables["Address_Set"]=2
  tables["DHCP_Options"]=2
  tables["HA_Chassis"]=1
  tables["HA_Chassis_Group"]=1
  tables["Logical_Router"]=1
  tables["Logical_Router_Policy"]=3
  tables["Logical_Router_Port"]=2
  tables["Logical_Router_Static_Route"]=2
  tables["Logical_Switch"]=2
  tables["Logical_Switch_Port"]=3
  tables["NAT"]=2
  tables["Port_Group"]=1
  assert_row_count

  ovn_network_id="$(lxd sql global --format csv "SELECT id FROM networks WHERE name = '${ovn_network}'")"

  # Check expected chassis and chassis group are created.
  chassis_group_name="lxd-net${ovn_network_id}"
  chassis_id="$(sudo ovn-nbctl --format json get ha_chassis_group "${chassis_group_name}" ha_chassis | tr -d '[]')"
  sudo ovn-nbctl get ha_chassis "${chassis_id}" priority

  # Check expected logical router has the correct name.
  logical_router_name="${chassis_group_name}-lr"
  ovn-nbctl get logical_router "${logical_router_name}" options

  # Get the expected MTU. This can be different when in different environments. Below replicates logic in LXD
  # for setting the optimal MTU when it isn't specified on network creation.
  mtu=1500
  geneve_overhead=58 # IPv4 overhead

  ovn_encap_ip="$(ovs-vsctl get open_vswitch . external_ids:ovn-encap-ip | tr -d '"')"
  if [[ "${ovn_encap_ip}" =~ .*:.* ]]; then
    geneve_overhead=78 # IPv6 overhead
  fi

  ovn_encap_iface_name="$(ip -json address | jq -r '.[] | select(.addr_info | .[] | .local == "'"${ovn_encap_ip}"'" ) | .ifname')"
  ovn_encap_iface_mtu="$(cat "/sys/class/net/${ovn_encap_iface_name}/mtu")"

  # MTU is 1500 if overlay MTU is greater than or equal to 1500 plus the overhead.
  # Otherwise it is 1500 minus the overhead.
  if [ "${ovn_encap_iface_mtu}" -lt $((mtu+geneve_overhead)) ]; then
    mtu=$((mtu-geneve_overhead))
  fi

  # Check external logical router port exists and has default gateway MTU.
  external_router_port_name="${logical_router_name}-lrp-ext"
  [ "$(ovn-nbctl get logical_router_port "${external_router_port_name}" options:gateway_mtu)" = '"'"${mtu}"'"' ]

  # Check IPs.
  [ "$(ovn-nbctl get logical_router_port "${external_router_port_name}" networks | jq -er '.[0]')" = "10.10.10.200/24" ]
  [ "$(ovn-nbctl get logical_router_port "${external_router_port_name}" networks | jq -er '.[1]')" = "fd42:4242:4242:1010::200/64" ]

  # Internal logical router port exists and has default gateway MTU.
  internal_router_port_name="${logical_router_name}-lrp-int"
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" options:gateway_mtu)" = '"'"${mtu}"'"' ]

  # Check ipv6 RA configs.
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" ipv6_ra_configs:address_mode)" = "dhcpv6_stateless" ]
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" ipv6_ra_configs:dnssl)" = "lxd" ]
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" ipv6_ra_configs:max_interval)" = '"60"' ]
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" ipv6_ra_configs:min_interval)" = '"30"' ]
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" ipv6_ra_configs:rdnss)" = '"fd42:4242:4242:1010::1"' ]
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" ipv6_ra_configs:send_periodic)" = '"true"' ]

  # Check IPs.
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" networks | jq -er '.[0]')" = "10.24.140.1/24" ]
  [ "$(ovn-nbctl get logical_router_port "${internal_router_port_name}" networks | jq -er '.[1]')" = "fd42:bd85:5f89:5293::1/64" ]

  # Check external switch is created.
  external_switch_name="${chassis_group_name}-ls-ext"
  ovn-nbctl get logical_switch "${external_switch_name}" other_config

  # Check internal switch settings.
  internal_switch_name="${chassis_group_name}-ls-int"
  [ "$(ovn-nbctl get logical_switch "${internal_switch_name}" other_config:exclude_ips)" = '"10.24.140.1"' ]
  [ "$(ovn-nbctl get logical_switch "${internal_switch_name}" other_config:ipv6_prefix)" = '"fd42:bd85:5f89:5293::/64"' ]
  [ "$(ovn-nbctl get logical_switch "${internal_switch_name}" other_config:subnet)" = '"10.24.140.0/24"' ]

  # Check external switch port settings (provider).
  provider_external_switch_port_name="${external_switch_name}-lsp-provider"
  [ "$(ovn-nbctl get logical_switch_port "${provider_external_switch_port_name}" type)" = "localnet" ]
  [ "$(ovn-nbctl get logical_switch_port "${provider_external_switch_port_name}" options:network_name)" = "${uplink_network}" ]

  # Check external switch port settings (router).
  router_external_switch_port_name="${external_switch_name}-lsp-router"
  [ "$(ovn-nbctl get logical_switch_port "${router_external_switch_port_name}" type)" = "router" ]
  [ "$(ovn-nbctl get logical_switch_port "${router_external_switch_port_name}" options:router-port)" = "${external_router_port_name}" ]
  [ "$(ovn-nbctl get logical_switch_port "${router_external_switch_port_name}" options:nat-addresses)" = "router" ]

  # Check internal switch port settings (router).
  router_internal_switch_port_name="${internal_switch_name}-lsp-router"
  [ "$(ovn-nbctl get logical_switch_port "${router_internal_switch_port_name}" type)" = "router" ]
  [ "$(ovn-nbctl get logical_switch_port "${router_internal_switch_port_name}" options:router-port)" = "${internal_router_port_name}" ]
  [ "$(ovn-nbctl get logical_switch_port "${router_internal_switch_port_name}" options:nat-addresses)" = "router" ]

  # Check port group settings.
  port_group_name="lxd_net${ovn_network_id}"
  [ "$(ovn-nbctl get port_group "${port_group_name}" external_ids:lxd_project_id)" = '"1"' ]
  [ "$(ovn-nbctl get port_group "${port_group_name}" external_ids:lxd_switch)" = "${internal_switch_name}" ]

  # Check address sets.
  address_set_ipv4_name="${port_group_name}_routes_ip4"
  [ "$(ovn-nbctl get address_set "${address_set_ipv4_name}" addresses | jq -er '.[0]')" = "10.24.140.0/24" ]

  address_set_ipv6_name="${port_group_name}_routes_ip6"
  [ "$(ovn-nbctl get address_set "${address_set_ipv6_name}" addresses | jq -er '.[0]')" = "fd42:bd85:5f89:5293::/64" ]

  # Check that uplink volatile address keys cannot be removed when associated network address is set.
  ! lxc network unset "${ovn_network}" volatile.network.ipv4.address || false
  ! lxc network unset "${ovn_network}" volatile.network.ipv6.address || false

  # Launch an instance on the OVN network and assert configuration changes.
  ensure_import_testimage
  lxc launch testimage c1 --network "${ovn_network}"

  # Check that this created the expected number of entries.
  tables["DNS"]=$((tables["DNS"]+1))
  tables["Logical_Switch_Port"]=$((tables["Logical_Switch_Port"]+1))
  assert_row_count

  c1_mac_address="$(lxc query /1.0/instances/c1 | jq -er '.config."volatile.eth0.hwaddr"')"
  c1_uuid="$(lxc query /1.0/instances/c1 | jq -er '.config."volatile.uuid"')"
  c1_internal_switch_port_name="${chassis_group_name}-instance-${c1_uuid}-eth0"

  # Busybox test image won't bring up the IPv4 interface by itself. Get the address and bring it up.
  c1_ipv4_address="$(ovn-nbctl get logical_switch_port "${c1_internal_switch_port_name}" dynamic_addresses | tr -d '"' | cut -d' ' -f 2)"
  c1_ipv6_address="$(ovn-nbctl get logical_switch_port "${c1_internal_switch_port_name}" dynamic_addresses | tr -d '"' | cut -d' ' -f 3)"
  lxc exec c1 -- ip -4 addr add "${c1_ipv4_address}/32" dev eth0
  lxc exec c1 -- ip -4 route add default dev eth0

  # Should now be able to get the same IPv4 address from the instance state.
  [ "$(lxc query /1.0/instances/c1?recursion=1 | jq -er '.state.network.eth0.addresses | .[] | select(.family == "inet").address')" = "${c1_ipv4_address}" ]

  # For IPv6, the interface will come up on it's own via SLAAC but we need to wait for DAD.
  wait_for_dad c1 eth0

  # Once up, we can verify the address is the same as in the dynamic addresses of the logical switch port.
  [ "$(lxc query /1.0/instances/c1?recursion=1 | jq -er '.state.network.eth0.addresses | .[] | select(.family == "inet6" and .scope == "global").address')" = "${c1_ipv6_address}" ]

  # Assert switch port configuration.
  [ "$(ovn-nbctl get logical_switch_port "${c1_internal_switch_port_name}" addresses | jq -er '.[0]')" = "${c1_mac_address} dynamic" ]
  [ "$(ovn-nbctl get logical_switch_port "${c1_internal_switch_port_name}" dynamic_addresses)" = '"'"${c1_mac_address} ${c1_ipv4_address} ${c1_ipv6_address}"'"' ]
  [ "$(ovn-nbctl get logical_switch_port "${c1_internal_switch_port_name}" external_ids:lxd_location)" = "none" ] # standalone location.
  [ "$(ovn-nbctl get logical_switch_port "${c1_internal_switch_port_name}" external_ids:lxd_switch)" = "${internal_switch_name}" ]

  # Assert DNS configuration.
  dns_entry_uuid="$(sudo ovn-nbctl --format csv --no-headings find dns "external_ids:lxd_switch_port=${c1_internal_switch_port_name}" | cut -d, -f1)"
  [ "$(ovn-nbctl get dns "${dns_entry_uuid}" external_ids:lxd_switch)" = "${internal_switch_name}" ]
  [ "$(ovn-nbctl get dns "${dns_entry_uuid}" records:c1.lxd)" = '"'"${c1_ipv4_address} ${c1_ipv6_address}"'"' ]

  # Clean up.
  lxc delete c1 --force
  lxc network delete "${ovn_network}"
  lxc network delete "${uplink_network}"

  # Validate northbound database is now empty.
  reset_row_count
  assert_row_count

  # More clean up.
  lxc config unset network.ovn.northbound_connection
  if [[ "${LXD_OVN_NB_CONNECTION}" =~ ^ssl: ]]; then
    lxc config unset network.ovn.client_cert
    lxc config unset network.ovn.client_key
    lxc config unset network.ovn.ca_cert
  fi
}
