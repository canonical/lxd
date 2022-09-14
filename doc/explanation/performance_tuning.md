(performance-tuning)=
# Performance tuning

## Run benchmarks

## Monitor instance metrics

## Tune server settings

The vast majority of Linux distributions do not come with optimized
kernel settings suitable for the operation of a large number of
containers. The instructions in this document cover the most common
limits that you're likely to hit when running containers and suggested
updated values.

### Common errors that may be encountered

`Failed to allocate directory watch: Too many open files`

`<Error> <Error>: Too many open files`

`failed to open stream: Too many open files in...`

`neighbour: ndisc_cache: neighbor table overflow!`

## Tune the network bandwidth

```{toctree}
:maxdepth: 1
:hidden:

Increase bandwidth <../howto/network_increase_bandwidth>
Server settings <../production-setup>
```
