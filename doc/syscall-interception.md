# System call interception
LXD supports intercepting some specific system calls from unprivileged
containers and if they're considered to be safe, will executed with
elevated privileges on the host.

Doing so comes with a performance impact for the syscall in question and
will cause some work for LXD to evaluate the request and if allowed,
process it with elevated privileges.

# Available system calls
## mknod / mknodat
The `mknod` and `mknodat` system calls can be used to create a variety of special files.

Most commonly inside containers, they may be called to create block or character devices.
Creating such devices isn't allowed in unprivileged containers as this
is a very easy way to escalate privileges by allowing direct write
access to resources like disks or memory.

But there are files which are safe to create. For those, intercepting
this syscall may unblock some specific workloads and allow them to run
inside an unprivileged containers.

The devices which are currently allowed are:

 - overlayfs whiteout (char 0:0)
 - /dev/console (char 5:1)
 - /dev/full (char 1:7)
 - /dev/null (char 1:3)
 - /dev/random (char 1:8)
 - /dev/tty (char 5:0)
 - /dev/urandom (char 1:9)
 - /dev/zero (char 1:5)

All file types other than character devices are currently sent to the
kernel as usual, so enabling this feature doesn't change their behavior
at all.

This can be enabled by setting `security.syscalls.intercept.mknod` to `true`.

## setxattr
The `setxattr` system call is used to set extended attributes on files.

The attributes which are handled by this currently are:

 - trusted.overlay.opaque (overlayfs directory whiteout)

Note that because the mediation must happen on a number of character
strings, there is no easy way at present to only intercept the few
attributes we care about. As we only allow the attributes above, this
may result in breakage for other attributes that would have been
previously allowed by the kernel.

This can be enabled by setting `security.syscalls.intercept.setxattr` to `true`.
