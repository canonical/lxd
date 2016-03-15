# Introduction
LXD just like LXC can run on just about any architecture that's
supported by the Linux kernel and by Go.

Some objects in LXD are tied to an architecture, like the container,
container snapshots and images.

This document lists all the supported architectures, their unique
identifier (used in the database), how they should be named and some
notes.


Please note that what LXD cares about is the kernel architecture, not
the particular userspace flavor as determined by the toolchain.

That means that LXD considers armv7 hard-float to be the same as armv7
soft-float and refers to both as "armv7". If useful to the user, the
exact userspace ABI may be set as an image and container property,
allowing easy query.

# Architectures

ID    | Name          | Notes                           | Personalities
:---  | :---          | :----                           | :------------
1     | i686          | 32bit Intel x86                 |
2     | x86\_64       | 64bit Intel x86                 | x86
3     | armv7l        | 32bit ARMv7 little-endian       |
4     | aarch64       | 64bit ARMv8 little-endian       | armv7 (optional)
5     | ppc           | 32bit PowerPC big-endian        |
6     | ppc64         | 64bit PowerPC big-endian        | powerpc
7     | ppc64le       | 64bit PowerPC little-endian     |
8     | s390x         | 64bit ESA/390 big-endian        |

The architecture names above are typically aligned with the Linux kernel
architecture names.
