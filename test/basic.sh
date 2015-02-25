test_basic_usage() {
  if ! lxc image alias list | grep -q ^ubuntu$; then
    scripts/lxd-images import lxc ubuntu trusty amd64 --alias ubuntu
  fi

  lxc launch ubuntu foo
  # should fail if foo isn't running
  lxc stop foo --force  # stop is hanging
  lxc delete foo

  lxc init ubuntu foo

  # did it get created?
  lxc list | grep foo

  # cycle it a few times
  lxc start foo
  lxc stop foo  --force # stop is hanging
  lxc start foo

  # Make sure it is the right version
  lxc exec foo /bin/cat /etc/issue | grep 14.04

  # cleanup
  lxc stop foo  --force # stop is hanging
  lxc delete foo
}
