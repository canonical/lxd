ovn_enabled() {
  [ -n "${LXD_OVN_NB_CONNECTION:-}" ]
}

setup_ovn() {
  # Not available, fail.
  if ! ovn_enabled; then
    false
  fi

  # Already configured.
  if [ "$(lxc config get network.ovn.northbound_connection)" != "" ]; then
    return
  fi

  if [[ "${LXD_OVN_NB_CONNECTION}" =~ ^ssl: ]]; then
    # If the connection uses SSL we have more required environment variables.
    # Set the connection string and client cert, key, and CA cert.
    lxc config set network.ovn.northbound_connection="${LXD_OVN_NB_CONNECTION}" network.ovn.client_cert="$(< "${LXD_OVN_NB_CLIENT_CRT_FILE}")" network.ovn.client_key="$(< "${LXD_OVN_NB_CLIENT_KEY_FILE}")" network.ovn.ca_cert="$(< "${LXD_OVN_NB_CA_CRT_FILE}")"
  else
    # Otherwise just set the connection.
    lxc config set network.ovn.northbound_connection "${LXD_OVN_NB_CONNECTION}"
  fi
}

unset_ovn_configuration() {
  if ! ovn_enabled; then
    return
  fi

  # Unset the config keys. Can't unset multiple values at once (yet - see https://github.com/canonical/lxd/issues/15933).
  lxc config set network.ovn.northbound_connection="" network.ovn.client_cert="" network.ovn.client_key="" network.ovn.ca_cert=""
}

clear_ovn_nb_db() {
  # Not configured - don't modify host.
  if ! ovn_enabled; then
    return
  fi

  # List tables with ovsdb client.
  for tbl in $(ovsdb-client list-tables "${LXD_OVN_NB_CONNECTION}" --format csv --no-headings); do
    # Don't modify NB_Global (should always have one row).
    if [ "${tbl}" = "NB_Global" ]; then
      continue
    fi

    # Delete any remaining data.
    ovn-nbctl --format csv --no-headings list "${tbl}" | cut -d, -f1 | ovn-nbctl destroy "${tbl}"
  done
}