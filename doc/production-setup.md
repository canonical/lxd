# Introduction
So you've made it past trying out [LXD live online](https://linuxcontainers.org/lxd/try-it/), 
or on a server scavanged from random parts. You like what you see, 
and now you want to try doing some serious work with LXD.

With the vanilla installation of Ubuntu Server 16.04, there will 
need to be some modifications to the server configuration to avoid 
common pitfalls when using containers that require tens of thousands 
of file operations.


## Common errors that may be encountered

`Failed to allocate directory watch: Too many open files`

`<Error> <Error>: Too many open files`

`failed to open stream: Too many open files in...`


# Server Changes
## /etc/security/limits.conf

Domain  | Type  | Item    | Value     | Default   | Description
:-----  | :---  | :----   | :-------- | :-------- | :----------
*       | soft  | nofile  | 1048576   | unset     | maximum number of open files
*       | hard  | nofile  | 1048576   | unset     | maximum number of open files
root    | soft  | nofile  | 1048576   | unset     | maximum number of open files
root    | hard  | nofile  | 1048576   | unset     | maximum number of open files
*       | soft  | memlock | unlimited | unset     | maximum locked-in-memory address space (KB)
*       | hard  | memlock | unlimited | unset     | maximum locked-in-memory address space (KB)


## /etc/sysctl.conf

Parameter                       | Value     | Default | Description
:-----                          | :---      | :---    | :---
fs.inotify.max\_queued\_events  | 1048576   | 16384   | This specifies an upper limit on the number of events that can be queued to the corresponding inotify instance. [1]
fs.inotify.max\_user\_instances | 1048576   | 128     | This specifies an upper limit on the number of inotify instances that can be created per real user ID. [1]
fs.inotify.max\_user\_watches   | 1048576   | 8192    | This specifies an upper limit on the number of watches that can be created per real user ID. [1]
vm.max\_map\_count              | 262144    | 65530   | This file contains the maximum number of memory map areas a process may have. Memory map areas are used as a side-effect of calling malloc, directly by mmap and mprotect, and also when loading shared libraries.


Then, reboot the server.


[1]: http://man7.org/linux/man-pages/man7/inotify.7.html
