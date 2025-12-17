(bpf-delegation-token)=
# Privilege delegation using BPF Token

## Overview

The {config:option}`instance-security:security.delegate_bpf` option enables the {abbr}`BPF (Berkeley Packet Filter)` functionality delegation mechanism, using a [BPF Token](https://docs.ebpf.io/linux/concepts/token). When enabled, LXD mounts a BPF File System (BPFFS) inside a container instance. This file system is configured with the `security.delegate_bpf.*` settings.
For example:

```
none on /sys/fs/bpf type bpf (rw,relatime,uid=1000000,gid=1000000,
                            delegate_cmds=map_create:prog_load,
                            delegate_maps=ringbuf,
                            delegate_progs=socket_filter,
                            delegate_attachs=cgroup_inet_ingress)
```

Then, applications inside the container can create a BPF Token file descriptor using that BPFFS mount and the `bpf(BPF_TOKEN_CREATE)` syscall. Later, this File Descriptor can be passed to `bpf(BPF_PROG_LOAD)`, `bpf(BPF_MAP_CREATE)`, or another `bpf()`-command syscall, and the kernel will perform a permission check against the token instead of the current user credentials. To be more precise, current user caps are also checked for `CAP_BPF` but in a current user namespace when `bpf(BPF_TOKEN_CREATE)` is called.

It follows that user space applications inside the container must be aware of  the BPF Token kernel feature (which appeared in Linux kernel v6.9) and make use of it. In contrast to `security.syscalls.intercept.*` features, this one is not fully transparent and might require updates or modifications to the software inside the container. Fortunately, [the libbpf library](https://docs.kernel.org/bpf/libbpf/libbpf_overview.html) supports BPF tokens. Thus if an application uses libbpf, then to make use of this feature, you might only need to update libbpf.

```{note}
Configure the following instance options for the container, depending on its BPF workload:

- {config:option}`instance-security:security.delegate_bpf.cmd_types`
- {config:option}`instance-security:security.delegate_bpf.map_types`
- {config:option}`instance-security:security.delegate_bpf.prog_types`
- {config:option}`instance-security:security.delegate_bpf.attach_types`
```

See the [BPF Token documentation page](https://docs.ebpf.io/linux/concepts/token/) on `docs.ebpf.io` for details.

## Example (socket filter)

Let's consider an example with a socket filter program from [libbpf-bootstrap](https://github.com/libbpf/libbpf-bootstrap).

The following creates an unprivileged container instance and sets all the necessary configuration options to enable BPF delegation:

```bash
lxc launch ubuntu:noble bpf-experiments
lxc config set bpf-experiments limits.kernel.memlock=unlimited
lxc config set bpf-experiments security.delegate_bpf=true
lxc config set bpf-experiments security.delegate_bpf.prog_types=socket_filter
lxc config set bpf-experiments security.delegate_bpf.attach_types=cgroup_inet_ingress
lxc config set bpf-experiments security.delegate_bpf.cmd_types=prog_load:map_create
lxc config set bpf-experiments security.delegate_bpf.map_types=ringbuf
```

The following set of commands clones and builds the libbpf-bootstrap.git repository within the example `bpf-experiments` container:

```bash
lxc shell bpf-experiments
apt install clang build-essential
git clone https://github.com/libbpf/libbpf-bootstrap.git
git submodule update --init --recursive
cd libbpf-bootstrap/examples/c
make
```

This experiment completes by running commands from two different shells into the `bpf-experiments` container.

From one terminal:

```{terminal}
lxc shell bpf-experiments
./sockfilter
```

From another terminal:

```{terminal}
lxc shell bpf-experiments
ping -c 4 localhost
```

Sample output:

```
ibbpf: loading object 'sockfilter_bpf' from buffer
libbpf: elf: section(2) .symtab, size 192, link 1, flags 0, type=2
libbpf: elf: section(3) socket, size 576, link 0, flags 6, type=1
libbpf: sec 'socket': found program 'socket_handler' at insn offset 0 (0 bytes), code size 72 insns (576 bytes)
...
libbpf: Kernel doesn't support BTF, skipping uploading it.
libbpf: map 'rb': created successfully, fd=3
interface: lo        protocol: ICMP        127.0.0.1:2048(src) -> 127.0.0.1:32429(dst)
interface: lo        protocol: ICMP        127.0.0.1:0(src) -> 127.0.0.1:34477(dst)
interface: lo        protocol: ICMP        127.0.0.1:2048(src) -> 127.0.0.1:46163(dst)
interface: lo        protocol: ICMP        127.0.0.1:0(src) -> 127.0.0.1:48211(dst)
```

We can see from this sample output that the ICMP packets were captured by the {abbr}`eBPF (extended Berkeley Capture Filter)` program and logged.

## Finding the right configuration

To figure out the right values for the `security.delegate_bpf.cmd_types`, `security.delegate_bpf.map_types`, `security.delegate_bpf.prog_types`, `security.delegate_bpf.attach_types` options, you must know how your application inside the container uses eBPF, such as its program types and map types. You can consult the application's source code, or use the [`strace`](https://github.com/strace/strace) tool to trace `bpf` syscall and see how it is being used.

Example using `strace`:

```{terminal}
strace -e bpf ./sockfilter
```

Sample output:

```
bpf(0x24 /* BPF_??? */, 0x7fffafdf5a40, 8) = 5
bpf(BPF_PROG_LOAD, {prog_type=BPF_PROG_TYPE_SOCKET_FILTER, insn_cnt=2, insns=0x7fffafdf59e0, license="GPL", log_level=0, log_size=0, log_buf=NULL, kern_version=KERNEL_VERSION(0, 0, 0), prog_flags=0, prog_name="", prog_ifindex=0, expected_attach_type=BPF_CGROUP_INET_INGRESS, prog_btf_fd=0, func_info_rec_size=0, func_info=NULL, func_info_cnt=0, line_info_rec_size=0, line_info=NULL, line_info_cnt=0, attach_btf_id=0, attach_prog_fd=0, fd_array=NULL}, 148) = -1 EPERM (Operation not permitted)
bpf(BPF_PROG_LOAD, {prog_type=BPF_PROG_TYPE_SOCKET_FILTER, insn_cnt=2, insns=0x7fffafdf5c10, license="GPL", log_level=0, log_size=0, log_buf=NULL, kern_version=KERNEL_VERSION(0, 0, 0), prog_flags=0x10000 /* BPF_F_??? */, prog_name="", prog_ifindex=0, expected_attach_type=BPF_CGROUP_INET_INGRESS, prog_btf_fd=0, func_info_rec_size=0, func_info=NULL, func_info_cnt=0, line_info_rec_size=0, line_info=NULL, line_info_cnt=0, attach_btf_id=0, attach_prog_fd=0, fd_array=NULL, ...}, 152) = 4
bpf(BPF_BTF_LOAD, {btf="\237\353\1\0\30\0\0\0\0\0\0\0000\0\0\0000\0\0\0\t\0\0\0\1\0\0\0\0\0\0\1"..., btf_log_buf=NULL, btf_size=81, btf_log_size=0, btf_log_level=0, ...}, 40) = -1 EPERM (Operation not permitted)
bpf(BPF_BTF_LOAD, {btf="\237\353\1\0\30\0\0\0\0\0\0\0000\0\0\0000\0\0\0\5\0\0\0\0\0\0\0\0\0\0\1"..., btf_log_buf=NULL, btf_size=77, btf_log_size=0, btf_log_level=0, ...}, 40) = -1 EPERM (Operation not permitted)
bpf(BPF_BTF_LOAD, {btf="\237\353\1\0\30\0\0\0\0\0\0\0\20\0\0\0\20\0\0\0\5\0\0\0\1\0\0\0\0\0\0\1"..., btf_log_buf=NULL, btf_size=45, btf_log_size=0, btf_log_level=0, ...}, 40) = -1 EPERM (Operation not permitted)
libbpf: Kernel doesn't support BTF, skipping uploading it.
bpf(BPF_PROG_LOAD, {prog_type=BPF_PROG_TYPE_SOCKET_FILTER, insn_cnt=2, insns=0x7fffafdf59c0, license="GPL", log_level=0, log_size=0, log_buf=NULL, kern_version=KERNEL_VERSION(0, 0, 0), prog_flags=0x10000 /* BPF_F_??? */, prog_name="libbpf_nametest", prog_ifindex=0, expected_attach_type=BPF_CGROUP_INET_INGRESS, prog_btf_fd=0, func_info_rec_size=0, func_info=NULL, func_info_cnt=0, line_info_rec_size=0, line_info=NULL, line_info_cnt=0, attach_btf_id=0, attach_prog_fd=0, fd_array=NULL, ...}, 148) = 4
bpf(BPF_PROG_LOAD, {prog_type=BPF_PROG_TYPE_SOCKET_FILTER, insn_cnt=2, insns=0x7fffafdf58e0, license="GPL", log_level=0, log_size=0, log_buf=NULL, kern_version=KERNEL_VERSION(0, 0, 0), prog_flags=0, prog_name="libbpf_nametest", prog_ifindex=0, expected_attach_type=BPF_CGROUP_INET_INGRESS, prog_btf_fd=0, func_info_rec_size=0, func_info=NULL, func_info_cnt=0, line_info_rec_size=0, line_info=NULL, line_info_cnt=0, attach_btf_id=0, attach_prog_fd=0, fd_array=NULL}, 148) = -1 EPERM (Operation not permitted)
bpf(BPF_MAP_CREATE, {map_type=BPF_MAP_TYPE_RINGBUF, key_size=0, value_size=0, max_entries=262144, map_flags=0x10000 /* BPF_F_??? */, inner_map_fd=0, map_name="", map_ifindex=0, btf_fd=0, btf_key_type_id=0, btf_value_type_id=0, btf_vmlinux_value_type_id=0, map_extra=0, ...}, 80) = 4
libbpf: map 'rb': created successfully, fd=3
bpf(BPF_PROG_LOAD, {prog_type=BPF_PROG_TYPE_SOCKET_FILTER, insn_cnt=72, insns=0x56198b83c180, license="Dual BSD/GPL", log_level=0, log_size=0, log_buf=NULL, kern_version=KERNEL_VERSION(6, 12, 14), prog_flags=0x10000 /* BPF_F_??? */, prog_name="", prog_ifindex=0, expected_attach_type=BPF_CGROUP_INET_INGRESS, prog_btf_fd=0, func_info_rec_size=0, func_info=NULL, func_info_cnt=0, line_info_rec_size=0, line_info=NULL, line_info_cnt=0, attach_btf_id=0, attach_prog_fd=0, fd_array=NULL, ...}, 152) = 4
bpf(BPF_OBJ_GET_INFO_BY_FD, {info={bpf_fd=3, info_len=88, info=0x7fffafdf5de0}}, 16) = 0
```

This log shows that `sockfilter` is using:

1. Program types: `BPF_PROG_TYPE_SOCKET_FILTER`
1. Map types: `BPF_MAP_TYPE_RINGBUF`
1. Attachment types: `BPF_CGROUP_INET_INGRESS`
1. BPF commands: `BPF_BTF_LOAD`, `BPF_PROG_LOAD`, `BPF_MAP_CREATE`
