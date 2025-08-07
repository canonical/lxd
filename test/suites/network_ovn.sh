test_network_ovn() {
  if ! ovn_enabled; then
    echo "==> SKIP: OVN not configured. Skipping OVN tests..."
    return
  fi

  ensure_import_testimage

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

  setup_ovn

  uplink_network="uplink$$"
  ovn_network="ovn$$"

  ########################################################################################################################

  echo "Test OVN with an uplink network of type physical."

  echo "Create a dummy physical network for use as an uplink."
  ip link add dummy0 type dummy
  lxc network create "${uplink_network}" --type=physical parent=dummy0

  echo "Set OVN ranges."
  lxc network set "${uplink_network}" ipv4.ovn.ranges=192.0.2.100-192.0.2.254
  lxc network set "${uplink_network}" ipv6.ovn.ranges=2001:db8:1:2::100-2001:db8:1:2::254

  echo "Set IP routes that include OVN ranges."
  lxc network set "${uplink_network}" ipv4.routes=192.0.2.0/24
  lxc network set "${uplink_network}" ipv6.routes=2001:db8:1:2::/64

  echo "Create an OVN network."
  lxc network create "${ovn_network}" --type ovn network="${uplink_network}"

  echo "Check that no forward can be created with a listen address that is not in the uplink's routes."
  ! lxc network forward create "${ovn_network}" 192.0.3.1 || false
  ! lxc network forward create "${ovn_network}" 2001:db8:1:3::1 || false

  echo "Check that no forward can be created on the uplink with a listen address that overlaps with OVN ranges."
  ! lxc network forward create "${ovn_network}" 192.0.2.100 || false
  ! lxc network forward create "${ovn_network}" 2001:db8:1:2::100 || false

  echo "Create a couple of forwards outside of OVN ranges but on the same network."
  lxc network forward create "${ovn_network}" 192.0.2.10
  lxc network forward create "${ovn_network}" 2001:db8:1:2::10

  echo "Check that removing IP routes on uplink for existing OVN forwards fails."
  ! lxc network unset "${uplink_network}" ipv4.routes || false
  ! lxc network unset "${uplink_network}" ipv6.routes || false

  echo "Clean up forwards."
  lxc network forward delete "${ovn_network}" 192.0.2.10
  lxc network forward delete "${ovn_network}" 2001:db8:1:2::10

  echo "Check that no load balancer can be created with a listen address that is not in the uplink's routes."
  ! lxc network load-balancer create "${ovn_network}" 192.0.3.1 || false
  ! lxc network load-balancer create "${ovn_network}" 2001:db8:1:3::1 || false

  echo "Check that no load balancer can be created with a listen address that overlaps with the uplink's OVN ranges."
  ! lxc network load-balancer create "${ovn_network}" 192.0.2.100 || false
  ! lxc network load-balancer create "${ovn_network}" 2001:db8:1:2::100 || false

  echo "Create a couple of load balancers outside of OVN ranges but on the same network."
  lxc network load-balancer create "${ovn_network}" 192.0.2.10
  lxc network load-balancer create "${ovn_network}" 2001:db8:1:2::10

  echo "Check that removing IP routes on uplink for existing OVN load balancers fails."
  ! lxc network unset "${uplink_network}" ipv4.routes || false
  ! lxc network unset "${uplink_network}" ipv6.routes || false

  echo "Clean up load balancers."
  lxc network load-balancer delete "${ovn_network}" 192.0.2.10
  lxc network load-balancer delete "${ovn_network}" 2001:db8:1:2::10

  echo "Check that instance NIC passthrough with ipv4.routes.external does not allow using IPs from OVN range."
  ! lxc launch testimage c1 -n "${ovn_network}" -d eth0,ipv4.routes.external=192.0.2.100/32 || false

  echo "Check that instance NIC passthrough with ipv6.routes.external does not allow using IPs from OVN range."
  ! lxc launch testimage c2 -n "${ovn_network}" -d eth0,ipv6.routes.external=2001:db8:1:2::100/128 || false

  echo "Check that instance NIC passthrough with ipv4.routes.external allows using IPs outside of OVN ranges but on the same network."
  lxc launch testimage c1 -n "${ovn_network}" -d eth0,ipv4.routes.external=192.0.2.10/32

  echo "Check that instance NIC passthrough with ipv6.routes.external allows using IPs outside of OVN ranges but on the same network."
  lxc launch testimage c2 -n "${ovn_network}" -d eth0,ipv6.routes.external=2001:db8:1:2::10/128

  echo "Clean up instances."
  lxc delete c1 --force
  lxc delete c2 --force

  echo "Check that removing IP routes on uplink works when there are no dependent OVN forwards."
  lxc network unset "${uplink_network}" ipv4.routes
  lxc network unset "${uplink_network}" ipv6.routes

  echo "Clean up created networks."
  lxc network delete "${ovn_network}"
  lxc network delete "${uplink_network}"
  ip link delete dummy0

  ########################################################################################################################

  echo "Test OVN with an uplink network of type bridge."

  echo "Create a bridge for use as an uplink."
  lxc network create "${uplink_network}" \
      ipv4.address=10.10.10.1/24 ipv4.nat=true \
      ipv4.dhcp.ranges=10.10.10.2-10.10.10.199 \
      ipv4.ovn.ranges=10.10.10.200-10.10.10.254 \
      ipv6.address=fd42:4242:4242:1010::1/64 ipv6.nat=true \
      ipv6.ovn.ranges=fd42:4242:4242:1010::200-fd42:4242:4242:1010::254 \
      ipv4.routes=192.0.2.0/24 ipv6.routes=2001:db8:1:2::/64

  echo "Check that no forward can be created on the uplink bridge with a listen address that overlaps with OVN ranges."
  ! lxc network forward create "${uplink_network}" 10.10.10.200 || false
  ! lxc network forward create "${uplink_network}" fd42:4242:4242:1010::200 || false

  echo "Create an OVN network."
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
  chassis_id="$(ovn-nbctl --format json get ha_chassis_group "${chassis_group_name}" ha_chassis | tr -d '[]')"
  ovn-nbctl get ha_chassis "${chassis_id}" priority

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
  [ "$(ovn-nbctl get logical_switch "${internal_switch_name}" load_balancer | tr -d '[]' | awk -F, '{print NF}')" = "0" ]

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

  # Check internal switch DHCP options (excluding server_mac address which is random).
  ovn-nbctl --data=bare --no-headings --columns=options find dhcp_options cidr=10.24.140.0/24 | grep -F 'dns_server={10.10.10.1} domain_name="lxd" lease_time=3600 mtu='"${mtu}"' router=10.24.140.1 server_id=10.24.140.1'
  ovn-nbctl --data=bare --no-headings --columns=options find dhcp_options cidr="fd42\:bd85\:5f89\:5293\:\:/64" | grep -F 'dns_server={fd42:4242:4242:1010::1} domain_search="lxd"'

  # Check that uplink volatile address keys cannot be removed when associated network address is set.
  ! lxc network unset "${ovn_network}" volatile.network.ipv4.address || false
  ! lxc network unset "${ovn_network}" volatile.network.ipv6.address || false

  # Check that volatile uplink IPs must be in the allowed ranges specified on the uplink.
  ! lxc network set "${ovn_network}" volatile.network.ipv4.address=10.10.10.199 || false
  ! lxc network set "${ovn_network}" volatile.network.ipv6.address=fd42:4242:4242:1010::199 || false

  echo "Launch an instance on the OVN network and assert configuration changes."
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
  lxc exec c1 -- ip -4 addr add "${c1_ipv4_address}/24" dev eth0
  lxc exec c1 -- ip -4 route add default via 10.24.140.1 dev eth0

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
  dns_entry_uuid="$(ovn-nbctl --format csv --no-headings find dns "external_ids:lxd_switch_port=${c1_internal_switch_port_name}" | cut -d, -f1)"
  [ "$(ovn-nbctl get dns "${dns_entry_uuid}" external_ids:lxd_switch)" = "${internal_switch_name}" ]
  [ "$(ovn-nbctl get dns "${dns_entry_uuid}" records:c1.lxd)" = '"'"${c1_ipv4_address} ${c1_ipv6_address}"'"' ]

  # Test DNS resolution.
  [ "$(lxc exec c1 -- nslookup c1.lxd 10.10.10.1 | grep -cF "${c1_ipv6_address}")" = 1 ]
  [ "$(lxc exec c1 -- nslookup c1.lxd fd42:4242:4242:1010::1 | grep -cF "${c1_ipv6_address}")" = 1 ]

  [ "$(lxc exec c1 -- nslookup "${c1_ipv4_address}" 10.10.10.1 | grep -cF c1.lxd)" = 1 ]
  [ "$(lxc exec c1 -- nslookup "${c1_ipv4_address}" fd42:4242:4242:1010::1 | grep -cF c1.lxd)" = 1 ]

  [ "$(lxc exec c1 -- nslookup "${c1_ipv6_address}" 10.10.10.1 | grep -cF c1.lxd)" = 1 ]
  [ "$(lxc exec c1 -- nslookup "${c1_ipv6_address}" fd42:4242:4242:1010::1 | grep -cF c1.lxd)" = 1 ]

  echo "Create a couple of forwards without a target address."
  lxc network forward create "${ovn_network}" 192.0.2.1
  lxc network forward create "${ovn_network}" 2001:db8:1:2::1
  [ "$(ovn-nbctl list load_balancer | grep -cF name)" = 0 ]

  volatile_ip4=$(lxc network get "${ovn_network}" volatile.network.ipv4.address | cut -d/ -f1)
  volatile_ip6=$(lxc network get "${ovn_network}" volatile.network.ipv6.address | cut -d/ -f1)

  echo "Add volatile.network.ipv4.address to the uplink's routes."
  lxc network set "${uplink_network}" ipv4.routes=192.0.2.0/24,"${volatile_ip4}/32"

  echo "Add volatile.network.ipv6.address to the uplink's routes."
  lxc network set "${uplink_network}" ipv6.routes=2001:db8:1:2::/64,"${volatile_ip6}/128"

  echo "Create a forward with a listener on volatile.network.ipv4.address."
  lxc network forward create "${ovn_network}" "${volatile_ip4}"

  echo "Create a forward with a listener on volatile.network.ipv6.address."
  lxc network forward create "${ovn_network}" "${volatile_ip6}"

  echo "Check that removing IP routes on uplink for existing OVN forwards fails."
  ! lxc network unset "${uplink_network}" ipv4.routes || false
  ! lxc network unset "${uplink_network}" ipv6.routes || false

  echo "Configure ports for the forwards."
  lxc network forward port add "${ovn_network}" 192.0.2.1 tcp 80 "${c1_ipv4_address}" 80
  lxc network forward port add "${ovn_network}" 2001:db8:1:2::1 tcp 80 "${c1_ipv6_address}" 80
  lxc network forward port add "${ovn_network}" "${volatile_ip4}" udp 162 "${c1_ipv4_address}" 162
  lxc network forward port add "${ovn_network}" "${volatile_ip6}" udp 162 "${c1_ipv6_address}" 162

  echo "Check that forwards are associated with the internal OVN switch."
  [ "$(ovn-nbctl get logical_switch "${internal_switch_name}" load_balancer | tr -d '[]' | awk -F, '{print NF}')" = "4" ]

  echo "Clean up forwards."
  lxc network forward delete "${ovn_network}" 192.0.2.1
  lxc network forward delete "${ovn_network}" 2001:db8:1:2::1
  lxc network forward delete "${ovn_network}" "${volatile_ip4}"
  lxc network forward delete "${ovn_network}" "${volatile_ip6}"

  echo "Create a couple of load balancers."
  lxc network load-balancer create "${ovn_network}" 192.0.2.1
  lxc network load-balancer create "${ovn_network}" 2001:db8:1:2::1
  [ "$(ovn-nbctl list load_balancer | grep -cF name)" = 0 ]

  echo "Create a load balancer with a listener on volatile.network.ipv4.address."
  lxc network load-balancer create "${ovn_network}" "${volatile_ip4}"

  echo "Create a load balancer with a listener on volatile.network.ipv6.address."
  lxc network load-balancer create "${ovn_network}" "${volatile_ip6}"

  echo "Check that removing IP routes on uplink for existing OVN load balancers fails."
  ! lxc network unset "${uplink_network}" ipv4.routes || false
  ! lxc network unset "${uplink_network}" ipv6.routes || false

  echo "Create a backend for each load balancer."
  lxc network load-balancer backend add "${ovn_network}" 192.0.2.1 c1-backend "${c1_ipv4_address}" 80
  lxc network load-balancer backend add "${ovn_network}" 2001:db8:1:2::1 c1-backend "${c1_ipv6_address}" 80
  lxc network load-balancer backend add "${ovn_network}" "${volatile_ip4}" c1-backend "${c1_ipv4_address}" 162
  lxc network load-balancer backend add "${ovn_network}" "${volatile_ip6}" c1-backend "${c1_ipv6_address}" 162

  echo "Configure ports for the load balancers."
  lxc network load-balancer port add "${ovn_network}" 192.0.2.1 tcp 80 c1-backend
  lxc network load-balancer port add "${ovn_network}" 2001:db8:1:2::1 tcp 80 c1-backend
  lxc network load-balancer port add "${ovn_network}" "${volatile_ip4}" udp 162 c1-backend
  lxc network load-balancer port add "${ovn_network}" "${volatile_ip6}" udp 162 c1-backend

  echo "Check that load balancers are associated with the internal OVN switch."
  [ "$(ovn-nbctl get logical_switch "${internal_switch_name}" load_balancer | tr -d '[]' | awk -F, '{print NF}')" = "4" ]

  echo "Clean up load balancers."
  lxc network load-balancer delete "${ovn_network}" 192.0.2.1
  lxc network load-balancer delete "${ovn_network}" 2001:db8:1:2::1
  lxc network load-balancer delete "${ovn_network}" "${volatile_ip4}"
  lxc network load-balancer delete "${ovn_network}" "${volatile_ip6}"

  echo "Test internal OVN network forwards and load balancers."

  echo "Check that no internal forward or load balancer can be created with a listen address of OVN gateway."
  ! lxc network forward create "${ovn_network}" 10.24.140.1 || false
  ! lxc network forward create "${ovn_network}" fd42:bd85:5f89:5293::1 || false
  ! lxc network load-balancer create "${ovn_network}" 10.24.140.1 || false
  ! lxc network load-balancer create "${ovn_network}" fd42:bd85:5f89:5293::1 || false

  echo "Check that no internal forward or load balancer can be created with a listen address taken by instance NIC."
  ! lxc network forward create "${ovn_network}" "${c1_ipv4_address}" || false
  ! lxc network forward create "${ovn_network}" "${c1_ipv6_address}" || false
  ! lxc network load-balancer create "${ovn_network}" "${c1_ipv4_address}" || false
  ! lxc network load-balancer create "${ovn_network}" "${c1_ipv6_address}" || false

  echo "Create internal forwards with a listen address that is an internal OVN IP."
  lxc network forward create "${ovn_network}" 10.24.140.10
  lxc network forward create "${ovn_network}" fd42:bd85:5f89:5293::10

  echo "Create internal load balancers with a listen address that is an internal OVN IP."
  lxc network load-balancer create "${ovn_network}" 10.24.140.20
  lxc network load-balancer create "${ovn_network}" fd42:bd85:5f89:5293::20

  echo "Check that no internal forward or load balancer can be created with a listen address taken by another listener."
  ! lxc network forward create "${ovn_network}" 10.24.140.10 || false
  ! lxc network forward create "${ovn_network}" 10.24.140.20 || false
  ! lxc network forward create "${ovn_network}" fd42:bd85:5f89:5293::10 || false
  ! lxc network forward create "${ovn_network}" fd42:bd85:5f89:5293::20 || false
  ! lxc network load-balancer create "${ovn_network}" 10.24.140.10 || false
  ! lxc network load-balancer create "${ovn_network}" 10.24.140.20 || false
  ! lxc network load-balancer create "${ovn_network}" fd42:bd85:5f89:5293::10 || false
  ! lxc network load-balancer create "${ovn_network}" fd42:bd85:5f89:5293::20 || false

  echo "Configure ports for internal forwards."
  lxc network forward port add "${ovn_network}" 10.24.140.10 tcp 80 "${c1_ipv4_address}" 80
  lxc network forward port add "${ovn_network}" fd42:bd85:5f89:5293::10 tcp 80 "${c1_ipv6_address}" 80

  echo "Clean up internal forwards."
  lxc network forward delete "${ovn_network}" 10.24.140.10
  lxc network forward delete "${ovn_network}" fd42:bd85:5f89:5293::10

  echo "Create a backend for each internal load balancer."
  lxc network load-balancer backend add "${ovn_network}" 10.24.140.20 c1-backend "${c1_ipv4_address}" 80
  lxc network load-balancer backend add "${ovn_network}" fd42:bd85:5f89:5293::20 c1-backend "${c1_ipv6_address}" 80

  echo "Configure ports for internal load balancers."
  lxc network load-balancer port add "${ovn_network}" 10.24.140.20 tcp 80 c1-backend
  lxc network load-balancer port add "${ovn_network}" fd42:bd85:5f89:5293::20 tcp 80 c1-backend

  echo "Clean up internal load balancers."
  lxc network load-balancer delete "${ovn_network}" 10.24.140.20
  lxc network load-balancer delete "${ovn_network}" fd42:bd85:5f89:5293::20

  echo "Clean up the instance."
  lxc delete c1 --force

  echo "Check that instance NIC passthrough with ipv4.routes.external does not allow using volatile.network.ipv4.address."
  ! lxc launch testimage c1 -n "${ovn_network}" -d eth0,ipv4.routes.external="${volatile_ip4}/32" || false

  echo "Check that instance NIC passthrough with ipv6.routes.external does not allow using volatile.network.ipv6.address."
  ! lxc launch testimage c1 -n "${ovn_network}" -d eth0,ipv6.routes.external="${volatile_ip6}/128" || false

  echo "Delete the OVN network in the default project."
  lxc network delete "${ovn_network}"

  echo "Test ha_chassis removal on shutdown."
  shutdown_lxd "${LXD_DIR}"
  ! ovn-nbctl get ha_chassis "${chassis_id}" priority || false
  respawn_lxd "${LXD_DIR}" true

  ########################################################################################################################

  echo "Create project for following tests."
  lxc project create testovn \
    -c features.images=false \
    -c features.profiles=false \
    -c features.storage.volumes=false

  lxc project switch testovn

  # Project uplink IP limits are exclusive to projects with features.networks enabled.
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 0 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 0 || false
  lxc project set testovn features.networks true
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 3
  lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 3

  # We cannot restrict a project with uplink IP limits set.
  lxc project set testovn features.profiles true # Needed to restrict project
  ! lxc project set testovn restricted true || false
  lxc project unset testovn limits.networks.uplink_ips.ipv4."${uplink_network}"
  lxc project unset testovn limits.networks.uplink_ips.ipv6."${uplink_network}"

  # Cannot restrict a project that is using a forbidden uplink.
  lxc network create restriction-test network="${uplink_network}" --project testovn
  ! lxc project set testovn restricted true || false
  lxc project set testovn restricted.networks.uplinks="${uplink_network}"
  lxc project set testovn restricted true
  ! lxc project unset testovn restricted.networks.uplinks="${uplink_network}" || false
  lxc network delete restriction-test --project testovn
  lxc project unset testovn restricted.networks.uplinks

  # We cannot set uplink IP limits on a restricted project unless the target network is in its allowed uplinks.
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 1 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 1 || false
  lxc project set testovn restricted.networks.uplinks="${uplink_network}"
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 1
  lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 1

  # Project uplink IP limits have to be non negative numbers.
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" true || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" something || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" -1 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" true || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" something || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" -1 || false

  # Check project uplink IP limits are enforced on OVN network creation.
  lxc network create first-ovn-network network="${uplink_network}"
  ! lxc network create second-ovn-network network="${uplink_network}" --type=ovn || false
  lxc network delete first-ovn-network
  lxc network create second-ovn-network network="${uplink_network}" --type=ovn

  # Only when both limits are relaxed, we are able to create another network.
  ! lxc network create failed-ovn-network --project testovn --type=ovn || false
  lxc project unset testovn limits.networks.uplink_ips.ipv6."${uplink_network}"
  ! lxc network create failed-ovn-network --project testovn --type=ovn || false
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 2
  lxc network create third-ovn-network --project testovn --type=ovn

  # Cannot set uplink IP limits lower than the currently used uplink IPs.
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 3
  lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 3
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 1 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 1 || false
  lxc network delete third-ovn-network --project testovn
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 1

  # Cannot set uplink IP limits for a network that is not suitable to be an uplink.
  ! lxc project set testovn limits.networks.uplink_ips.ipv4.non-existent 2 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6.non-existent 2 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv4.third-ovn-network 2 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6.third-ovn-network 2 || false

  # A bit of cleanup.
  lxc network delete second-ovn-network --project testovn
  ! lxc project unset testovn restricted.networks.uplinks || false # Cannot unset while having limits set for the uplink network.
  lxc project set testovn restricted false
  lxc project unset testovn restricted.networks.uplinks
  lxc project unset testovn limits.networks.uplink_ips.ipv4."${uplink_network}"
  lxc project unset testovn limits.networks.uplink_ips.ipv6."${uplink_network}"
  lxc project set testovn features.profiles false

  echo "Create an OVN network isolated in a project."
  project_ovn_network="project-ovn$$"
  lxc network create "${project_ovn_network}" --type ovn network="${uplink_network}" \
    ipv4.address=10.24.140.1/24 ipv4.nat=true \
    ipv6.address=fd42:bd85:5f89:5293::1/64 ipv6.nat=true

  echo "Check that no forward can be created with a listen address that is not in the uplink's routes."
  ! lxc network forward create "${project_ovn_network}" 192.0.3.1 || false
  ! lxc network forward create "${project_ovn_network}" 2001:db8:1:3::1 || false

  echo "Create a couple of forwards without a target address."
  lxc network forward create "${project_ovn_network}" 192.0.2.1
  lxc network forward create "${project_ovn_network}" 2001:db8:1:2::1
  [ "$(ovn-nbctl list load_balancer | grep -cF name)" = 0 ]

  # Cannot set uplink IP limits lower than the currently used uplink IPs.
  # There is one ovn network created and one forward of each protocol, so 2 IPs in use for each protocol.
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 1 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 1 || false
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 2
  lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 2

  # Check project uplink IP limits are enforced on network forward creation.
  ! lxc network forward create "${project_ovn_network}" 192.0.2.2 || false
  ! lxc network forward create "${project_ovn_network}" 2001:db8:1:2::2 || false
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 3
  lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 3
  lxc network forward create "${project_ovn_network}" 192.0.2.2
  lxc network forward create "${project_ovn_network}" 2001:db8:1:2::2

  # Clean up
  lxc network forward delete "${project_ovn_network}" 192.0.2.2
  lxc network forward delete "${project_ovn_network}" 2001:db8:1:2::2
  lxc project unset testovn limits.networks.uplink_ips.ipv4."${uplink_network}"
  lxc project unset testovn limits.networks.uplink_ips.ipv6."${uplink_network}"
  lxc network forward delete "${project_ovn_network}" 192.0.2.1
  lxc network forward delete "${project_ovn_network}" 2001:db8:1:2::1

  echo "Check that no load balancer can be created with a listen address that is not in the uplink's routes."
  ! lxc network load-balancer create "${project_ovn_network}" 192.0.3.1 || false
  ! lxc network load-balancer create "${project_ovn_network}" 2001:db8:1:3::1 || false

  echo "Create a couple of load balancers."
  lxc network load-balancer create "${project_ovn_network}" 192.0.2.1
  lxc network load-balancer create "${project_ovn_network}" 2001:db8:1:2::1
  [ "$(ovn-nbctl list load_balancer | grep -cF name)" = 0 ]

  # Cannot set uplink IP limits lower than the currently used uplink IPs.
  # There is one ovn network created and one load balancer for each protocol, so 2 IPs in use for each protocol.
  ! lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 1 || false
  ! lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 1 || false
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 2
  lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 2

  # Check project uplink IP limits are enforced on load balancer creation.
  ! lxc network load-balancer create "${project_ovn_network}" 192.0.2.2 || false
  ! lxc network load-balancer create "${project_ovn_network}" 2001:db8:1:2::2 || false
  lxc project set testovn limits.networks.uplink_ips.ipv4."${uplink_network}" 3
  lxc project set testovn limits.networks.uplink_ips.ipv6."${uplink_network}" 3
  lxc network load-balancer create "${project_ovn_network}" 192.0.2.2
  lxc network load-balancer create "${project_ovn_network}" 2001:db8:1:2::2

  # Clean up
  lxc network load-balancer delete "${project_ovn_network}" 192.0.2.2
  lxc network load-balancer delete "${project_ovn_network}" 2001:db8:1:2::2
  lxc network delete "${project_ovn_network}"
  lxc project switch default
  lxc project delete testovn

  lxc network delete "${uplink_network}"

  # Validate northbound database is now empty.
  reset_row_count
  assert_row_count

  unset_ovn_configuration
}
