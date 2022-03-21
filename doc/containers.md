---
discourse: 8767
---

# Containers
## Introduction
Containers are the default type for LXD and currently the most
featureful and complete implementation of LXD instances.

They are implemented through the use of `liblxc` (LXC).

## Configuration
See [instance configuration](instances.md) for valid configuration options.

## Live migration
LXD supports live migration of containers using [CRIU](http://criu.org). In
order to optimize the memory transfer for a container LXD can be instructed to
make use of CRIU's pre-copy features by setting the
`migration.incremental.memory` property to `true`. This means LXD will request
CRIU to perform a series of memory dumps for the container. After each dump LXD
will send the memory dump to the specified remote. In an ideal scenario each
memory dump will decrease the delta to the previous memory dump thereby
increasing the percentage of memory that is already synced. When the percentage
of synced memory is equal to or greater than the threshold specified via
`migration.incremental.memory.goal` LXD will request CRIU to perform a final
memory dump and transfer it. If the threshold is not reached after the maximum
number of allowed iterations specified via
`migration.incremental.memory.iterations` LXD will request a final memory dump
from CRIU and migrate the container.
