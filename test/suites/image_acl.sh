test_image_acl() {
  ensure_import_testimage

  # Launch a new container with an ACL applied file
  lxc launch testimage c1
  C1_PID="$(lxc list -f csv -c p c1)"
  lxc exec c1 -- touch foo
  setfacl -m user:1000001:rwx "/proc/${C1_PID}/root/root/foo"
  setfacl -m group:1000001:rwx "/proc/${C1_PID}/root/root/foo"

  # Publish the container to a new image
  lxc stop c1
  lxc publish c1 --alias c1-with-acl

  # Launch a new container from the existing image
  lxc launch c1-with-acl c2

  # Check if the ACLs are still present
  C2_PID="$(lxc list -f csv -c p c2)"
  getfacl "/proc/${C2_PID}/root/root/foo" | grep -xF "user:1000001:rwx"
  getfacl "/proc/${C2_PID}/root/root/foo" | grep -xF "group:1000001:rwx"

  lxc delete -f c1 c2
  lxc image delete c1-with-acl
}
