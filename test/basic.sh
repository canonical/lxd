test_basic_usage() {
  lxc launch ubuntu foo
  # should fail if foo isn't running
  lxc stop foo
  lxc delete foo

  lxc init ubuntu foo

  # did it get created?
  lxc list | grep foo

  # cycle it a few times
  lxc start foo
  lxc stop foo
  lxc start foo

  # Make sure it is the right version
  lxc exec foo /bin/cat /etc/issue | grep 14.04

  # cleanup
  lxc stop foo
  lxc delete foo
}
