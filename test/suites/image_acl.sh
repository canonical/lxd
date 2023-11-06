test_image_acl() {
  ensure_import_testimage

  # Launch a new container with an ACL applied file
  lxc launch testimage c1
  CONTAINER_PID="$(lxc query /1.0/instances/c1?recursion=1 | jq '.state.pid')"
  lxc exec c1 -- touch foo
  setfacl -m user:1000001:rwx "/proc/$CONTAINER_PID/root/root/foo"
  setfacl -m group:1000001:rwx "/proc/$CONTAINER_PID/root/root/foo"

  # Publish the container to a new image
  lxc stop c1
  lxc publish c1 --alias c1-with-acl

  # Launch a new container from the existing image
  lxc launch c1-with-acl c2

  # Check if the ACLs are still present
  CONTAINER_PID="$(lxc query /1.0/instances/c2?recursion=1 | jq '.state.pid')"
  getfacl "/proc/$CONTAINER_PID/root/root/foo" | grep -q "user:1000001:rwx"
  getfacl "/proc/$CONTAINER_PID/root/root/foo" | grep -q "group:1000001:rwx"

  lxc delete -f c1 c2
  lxc image delete c1-with-acl
}
