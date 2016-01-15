# How to run

To run all tests, including the Go tests, run from repository root:

    sudo -E make check

To run only the integration tests, run from the test directory:

    sudo -E ./main.sh

# Environment variables

Name                            | Default                   | Description
:--                             | :---                      | :----------
LXD\_BACKEND                    | dir                       | What backend to test against (btrfs, dir, lvm or zfs)
LXD\_CONCURRENT                 | 0                         | Run concurency tests, very CPU intensive
LXD\_DEBUG                      | 0                         | Run lxd, lxc and the shell in debug mode (very verbose)
LXD\_INSPECT                    | 0                         | Don't teardown the test environment on failure
LXD\_LOGS                       | ""                        | Path to a directory to copy all the LXD logs to
LXD\_OFFLINE                    | 0                         | Skip anything that requires network access
LXD\_TEST\_IMAGE                | "" (busybox test image)   | Path to an image tarball to use instead of the default busybox image
LXD\_TMPFS                      | 0                         | Sets up a tmpfs for the whole testsuite to run on (fast but needs memory)
