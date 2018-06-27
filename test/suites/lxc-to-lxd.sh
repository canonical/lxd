test_lxc_to_lxd() {
  ensure_has_localhost_remote "${LXD_ADDR}"

  LXC_DIR="${TEST_DIR}/lxc"

  mkdir -p "${LXC_DIR}"

  # Create LXC containers
  lxc-create -P "${LXC_DIR}" -n c1 -B dir -t busybox
  lxc-create -P "${LXC_DIR}" -n c2 -B dir -t busybox
  lxc-create -P "${LXC_DIR}" -n c3 -B dir -t busybox

  # Convert single LXC container (dry run)
  lxc-to-lxd --lxcpath "${LXC_DIR}" --dry-run --delete --containers c1

  # Ensure the LXC containers have not been deleted
  [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq "3" ]

  # Ensure no containers have been converted
  ! lxc info c1
  ! lxc info c2
  ! lxc info c3

  # Convert single LXC container
  lxc-to-lxd --lxcpath "${LXC_DIR}" --containers c1

  # Ensure the LXC containers have not been deleted
  [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq 3 ]

  # Ensure only c1 has been converted
  lxc info c1
  ! lxc info c2
  ! lxc info c3

  # Ensure the converted container is startable
  lxc start c1
  lxc delete -f c1

  # Convert some LXC containers
  lxc-to-lxd --lxcpath "${LXC_DIR}" --delete --containers c1,c2

  # Ensure the LXC containers c1 and c2 have been deleted
  [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq 1 ]

  # Ensure all containers have been converted
  lxc info c1
  lxc info c2
  ! lxc info c3

  # Convert all LXC containers
  lxc-to-lxd --lxcpath "${LXC_DIR}" --delete --all

  # Ensure the remaining LXC containers have been deleted
  [ "$(lxc-ls -P "${LXC_DIR}" -1 | wc -l)" -eq 0 ]

  # Ensure all containers have been converted
  lxc info c1
  lxc info c2
  lxc info c3
}
