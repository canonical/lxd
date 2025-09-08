# How to run

To run all tests, including the Go tests, run from repository root:

    sudo -E make check

To run only the integration tests, run from the test directory:

    sudo -E ./main.sh

# Environment variables

Name                           | Default                   | Description
:--                            | :---                      | :----------
`LXD_BACKEND`                  | dir                       | What backend to test against (btrfs, ceph, dir, lvm, zfs, or random)
`LXD_CEPH_CLUSTER`             | ceph                      | The name of the ceph cluster to create osd pools in
`LXD_CEPH_CEPHFS`              | ""                        | Enables the CephFS tests using the specified cephfs filesystem for `cephfs` pools
`LXD_CEPH_CEPHOBJECT_RADOSGW`  | ""                        | Enables the Ceph Object tests using the specified radosgw HTTP endpoint for `cephobject` pools
`LXD_VERBOSE`                  | ""                        | Run lxd, lxc and the shell in verbose mode (used in CI; less verbose than `LXD_DEBUG`)
`LXD_DEBUG`                    | ""                        | Run lxd, lxc and the shell in debug mode (very verbose)
`LXD_INSPECT`                  | 0                         | Set to 1 to start an inspection shell in the test environment on failure
`LXD_LOGS`                     | ""                        | Path to a directory to copy all the LXD logs to
`LXD_OFFLINE`                  | 0                         | Skip anything that requires network access
`LXD_SKIP_TESTS`               | ""                        | Space-delimited list of test names to skip
`LXD_TEST_IMAGE`               | "" (busybox test image)   | Path to an image tarball to use instead of the default busybox image
`LXD_TMPFS`                    | 0                         | Sets up a tmpfs for the whole testsuite to run on (fast but needs memory)
`LXD_NIC_SRIOV_PARENT`         | ""                        | Enables SR-IOV NIC tests using the specified parent device
`LXD_IB_PHYSICAL_PARENT`       | ""                        | Enables Infiniband physical tests using the specified parent device
`LXD_IB_SRIOV_PARENT`          | ""                        | Enables Infiniband SR-IOV tests using the specified parent device
`LXD_NIC_BRIDGED_DRIVER`       | ""                        | Specifies bridged NIC driver for tests (either native or openvswitch, defaults to native)
`LXD_REQUIRED_TESTS`           | ""                        | Space-delimited list of test names that must not be skipped if their prerequisites are not met
`LXD_VM_TESTS`                 | 0                         | Enables tests using VMs and the on-demand installation of the needed tools

# Recommendations

## `echo` context

Add `echo` to provide context during the test execution instead of using code comments.

Bad:

```sh
test_vm() {
  # Tiny VMs
  lxc init --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc start v1
  lxc delete --force v1

  # Ephemeral cleanup
  lxc launch --vm --empty --ephemeral v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc stop -f v1
  [ "$(lxc list -f csv -c n)" = "" ]
}
```

Good:

```sh
  echo "==> Tiny VMs"
  lxc init --vm --empty v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc start v1
  lxc delete --force v1

  echo "==> Ephemeral cleanup"
  lxc launch --vm --empty --ephemeral v1 -c limits.memory=128MiB -d "${SMALL_ROOT_DISK}"
  lxc stop -f v1
  [ "$(lxc list -f csv -c n)" = "" ]
```

This way, debug logs will be easier to read.

## Expected failure

If a command is expected to fail, special care needs to be used in testing.

Bad:

```sh
set -e
...

! cmd_should_fail

some_other_command
```

Good:

```sh
set -e

! cmd_should_fail || false

some_other_command
```

Best:

```sh
set -e

if cmd_should_fail; then
  echo "ERROR: cmd_should_fail unexpectedly succeeded, aborting" >&2
  exit 1
fi

some_other_command
```

In the "bad" example, if the command unexpectedly succeeds, the script won't
abort because `bash` ignores `set -e` for compounded commands (`!
cmd_should_fail`).

The "good" example works around the problem of compound commands by falling
back to executing `false` in case of unexpected success of the command.

The "best" example also works around the problem of compound commands but in a
very intuitive and readable form, albeit longer.

````{note}
This odd behavior of `set -e` with compound commands does not apply inside `[]`.

```sh
set -e
# Does the right thing of failing if the file unexpectedly exist
[ ! -e "should/not/exist" ]
```

However, note that in the above example, if the `!` is moved outside of the `[]`, it would also warrant a ` || false` fallback.
````
