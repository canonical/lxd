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

# Multiple allocations
There are a few reasons why you wouldn't want a single allocation for
all of your containers:
 * ulimits (a container couldn't exhaust a ulimit and affect all others)
 * in the unlikely event where someone breaks out of a container, they
   can then enter any of the others running with the same map. 

And there's at least one reason why you'd want a shared allocation:
 * shared filesystems

As a result, the plan is for the default profile to come with a 65536
uid/gid allocation which will be used by all container running with the
default profile.

If you need to completely separate users, you can then create one
profile per user and assign it a different allocation.

# Changing the allocation of a used profile
When changing the allocation of a profile which is in use, LXD will
check whether the new allocation is smaller or larger than the previous
one.

If it's smaller and containers currently use the profile, the change
will be simply be rejected.
You'll have to move all the existing containers to different profiles
before you can do the change.

If the new allocation is of the same size or larger than the current one
and some of the containers are currently running, the change will be
rejected and you'll have to first stop all the affected containers
before re-trying.

Then if no containers are affected, the change will simply be committed,
otherwise, a uid/gid remap of all affected containers will occur, then
the change will be saved.

# Varying ranges between hosts
When migrating a container between host, the size of its current uid/gid
allocation with its current profile will be checked against that it'll
get on the remote server.

If the allocation is smaller, the transfer will be aborted and the user
will need to update the remote profile or switch the container to a
different profile before attempting the transfer again.

In the other cases, the source host uid and gid range will be compared
to that of the destination host. If it happens to be identical, then the
filesystem will be transferred as-is.

Otherwise, the filesystem will be transferred and a uid/gid remap
operation will then happen to convert all the uids and gids to the right
range.
