# Introduction
LXD runs safe containers. This is achieved mostly through the use of
user namespaces which make it possible to run containers unprivileged,
greatly limiting the attack surface.

User namespaces work by mapping a set of uids and gids on the host to a
set of uids and gids in the container.


For example, we can define that the host uids and gids from 100000 to
165535 may be used by LXD and should be mapped to uid/gid 0 through
65535 in the container.

As a result a process running as uid 0 in the container will actually be
running as uid 100000.

Allocations should always be of at least 65536 uids and gids to cover
the POSIX range including root (0) and nobody (65534).


To simplify things, at this point, we will only deal with identical
allocations for uids and gids and only support a single contiguous range
per container.

# Kernel support
User namespaces require a kernel >= 3.12, LXD will start even on older
kernels but will refuse to start containers.

# Allowed ranges
On most hosts, LXD will check /etc/subuid and /etc/subgid for
allocations for the "lxd" user and on first start, set the default
profile to use the first 65536 uids and gids from that range.

If the range is shorter than 65536 (which includes no range at all),
then LXD will fail to create or start any container until this is corrected.

If some but not all of /etc/subuid, /etc/subgid, newuidmap (path lookup)
and newgidmap (path lookup) can't be found on the system, LXD will fail
the startup of any container until this is corrected as this shows a
broken shadow setup.

If none of those 4 files can be found, then LXD will assume it's running
on a host using an old version of shadow. In this mode, LXD will assume
it can use any uids and gids above 65535 and will take the first 65536
as its default map.

# Varying ranges between hosts
The source map is sent when moving containers between hosts so that they
can be remapped on the receiving host.
