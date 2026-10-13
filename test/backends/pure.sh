pure_setup() {
  local LXD_DIR="${1}"

  echo "==> Setting up Everpure backend in ${1}"
}

# pure_configure creates Everpure storage pool and configures instance root disk
# device in default profile to use that storage pool.
pure_configure() {
  local LXD_DIR="${1}"
  local POOL_NAME="${2:-"lxdtest-${LXD_DIR##*/}"}" # Use the last part of the LXD_DIR as pool name
  local VOLUME_SIZE="${3:-"${DEFAULT_VOLUME_SIZE}"}"

  echo "==> Configuring Everpure backend in ${LXD_DIR}"

  # Create Everpure storage pool.
  lxc storage create "${POOL_NAME}" pure \
    pure.gateway="${PURE_GATEWAY}" \
    pure.gateway.verify="${PURE_GATEWAY_VERIFY:-true}" \
    pure.api.token="${PURE_API_TOKEN}" \
    pure.mode="${PURE_MODE:-nvme}" \
    volume.size="${VOLUME_SIZE}"

  # Add the storage pool to the default profile.
  lxc profile device add default root disk path="/" pool="${POOL_NAME}"
}

# configure_pure_pool creates new Everpure storage pool with a given name.
# Additional arguments are appended to the lxc storage create command.
# If there is anything on the stdin, the content is passed to the lxc storage create command as stdin as well.
configure_pure_pool() {
  poolName="${1}"
  shift 1

  if [ -p /dev/stdin ]; then
    # Use heredoc if there's input on stdin
    lxc storage create "${poolName}" pure \
      pure.gateway="${PURE_GATEWAY}" \
      pure.gateway.verify="${PURE_GATEWAY_VERIFY:-true}" \
      pure.api.token="${PURE_API_TOKEN}" \
      pure.mode="${PURE_MODE:-nvme}" \
      "$@" <<EOF
$(cat)
EOF
  else
    # Run without stdin if no heredoc is provided
    lxc storage create "${poolName}" pure \
      pure.gateway="${PURE_GATEWAY}" \
      pure.gateway.verify="${PURE_GATEWAY_VERIFY:-true}" \
      pure.api.token="${PURE_API_TOKEN}" \
      pure.mode="${PURE_MODE:-nvme}" \
      "$@"
  fi
}

pure_teardown() {
  local LXD_DIR="${1}"

  echo "==> Tearing down Everpure backend in ${LXD_DIR}"
}
