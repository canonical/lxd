test_resources() {
  RES=$(lxc storage show --resources "lxdtest-$(basename "${LXD_DIR}")")
  echo "${RES}" | grep -q "^space:"

  RES=$(lxc info --resources)
  echo "${RES}" | grep -q "^cpu:"
  echo "${RES}" | grep -q "sockets:"
  echo "${RES}" | grep -q "threads:"
  echo "${RES}" | grep -q "total:"
  echo "${RES}" | grep -q "memory:"
  echo "${RES}" | grep -q "used:"
  echo "${RES}" | grep -q "total:"
}
