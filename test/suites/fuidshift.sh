_common_fuidshift() {
  # test some bad arguments
  ! fuidshift /tmp -t b:0 > /dev/null 2>&1 || false
  ! fuidshift /tmp -t x:0:0:0 > /dev/null 2>&1 || false
}

_nonroot_fuidshift() {
  _common_fuidshift

  LXD_FUIDMAP_DIR=$(mktemp -d -p "${TEST_DIR}" XXX)

  u=$(id -u)
  g=$(id -g)
  u1=$((u+1))
  g1=$((g+1))

  fail=0
  touch "${LXD_FUIDMAP_DIR}/x1"
  fuidshift "${LXD_FUIDMAP_DIR}/x1" -t "u:${u}:100000:1" "g:${g}:100000:1" | tee /dev/stderr | grep "to 100000 100000" > /dev/null || fail=1
  if [ "${fail}" -eq 1 ]; then
    echo "==> Failed to shift own uid to container root"
    false
  fi
  fuidshift "${LXD_FUIDMAP_DIR}/x1" -t "u:${u1}:10000:1" "g:${g1}:100000:1" | tee /dev/stderr | grep "to -1 -1" > /dev/null || fail=1
  if [ "${fail}" -eq 1 ]; then
    echo "==> Wrongly shifted invalid uid to container root"
    false
  fi

  # unshift it
  chown 100000:100000 "${LXD_FUIDMAP_DIR}/x1"
  fuidshift "${LXD_FUIDMAP_DIR}/x1" -r -t "u:${u}:100000:1" "g:${g}:100000:1" | tee /dev/stderr | grep "to 0 0" > /dev/null || fail=1
  if [ "${fail}" -eq 1 ]; then
    echo "==> Failed to unshift container root back to own uid"
    false
  fi
}

_root_fuidshift() {
  _nonroot_fuidshift

  # Todo - test ranges
}

test_fuidshift() {
  if [ "$(id -u)" -ne 0 ]; then
    _nonroot_fuidshift
  else
    _root_fuidshift
  fi
}
