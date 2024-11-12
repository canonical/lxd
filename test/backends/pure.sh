pure_setup() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Setting up PureStorage backend in ${LXD_DIR}"
}

pure_check_vars() {
  # Quick checks to error out with intuitive message.
  if [ -z "${PURESTORAGE_GATEWAY}" ]; then
    echo "PureStorage gateway has to be set using PURESTORAGE_GATEWAY environment variable"
    exit 1
  fi

  if [ -z "${PURESTORAGE_API_TOKEN}" ]; then
    echo "PureStorage API token has to be set using PURESTORAGE_API_TOKEN environment variable"
    exit 1
  fi

  if [ -z "${PURESTORAGE_ARRAY_ADDRESS}" ]; then
    echo "PureStorage array address has to be set using PURESTORAGE_ARRAY_ADDRESS environment variable"
    exit 1
  fi

}

# pure_configure creates PureStorage storage pool and configures instance root disk
# device in default profile to use that storage pool.
pure_configure() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Configuring PureStorage backend in ${LXD_DIR}"

  pure_check_vars

  # Create pure storage storage pool.
  lxc storage create "lxdtest-$(basename "${LXD_DIR}")" pure \
    pure.gateway="${PURESTORAGE_GATEWAY}" \
    pure.gateway.verify="${PURESTORAGE_GATEWAY_VERIFY:-true}" \
    pure.api.token="${PURESTORAGE_API_TOKEN}" \
    pure.array.address="${PURESTORAGE_ARRAY_ADDRESS}" \
    pure.mode="${PURESTORAGE_MODE:-nvme}" \
    volume.size=25MiB

  # Add the storage pool to the default profile.
  lxc profile device add default root disk path="/" pool="lxdtest-$(basename "${LXD_DIR}")"
}

# configure_pure_pool creates new PureStorage storage pool with a given name.
# Additional arguments are appended to the lxc storage create command.
# If there is anything on the stdin, the content is passed to the lxc storage create command as stdin as well.
configure_pure_pool() {
  poolName=$1
  shift 1

  pure_check_vars

  if [ -p /dev/stdin ]; then
    # Use heredoc if there's input on stdin
    lxc storage create "${poolName}" pure \
      pure.gateway="${PURESTORAGE_GATEWAY}" \
      pure.gateway.verify="${PURESTORAGE_GATEWAY_VERIFY:-true}" \
      pure.api.token="${PURESTORAGE_API_TOKEN}" \
      pure.array.address="${PURESTORAGE_ARRAY_ADDRESS}" \
      pure.mode="${PURESTORAGE_MODE:-nvme}" \
      "$@" <<EOF
$(cat)
EOF
  else
    # Run without stdin if no heredoc is provided
    lxc storage create "${poolName}" pure \
      pure.gateway="${PURESTORAGE_GATEWAY}" \
      pure.gateway.verify="${PURESTORAGE_GATEWAY_VERIFY:-true}" \
      pure.api.token="${PURESTORAGE_API_TOKEN}" \
      pure.array.address="${PURESTORAGE_ARRAY_ADDRESS}" \
      pure.mode="${PURESTORAGE_MODE:-nvme}" \
      "$@"
  fi
}

pure_teardown() {
  local LXD_DIR

  LXD_DIR=$1

  echo "==> Tearing down PureStorage backend in ${LXD_DIR}"
}
