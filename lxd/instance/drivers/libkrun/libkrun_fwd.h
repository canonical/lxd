/*
 * Minimal forward declarations for libkrun bundled with this package.
 *
 * This replaces the system <libkrun.h> so that the package compiles without
 * libkrun installed. The library itself is resolved at runtime via dlopen(3).
 */

#ifndef LIBKRUN_FWD_H
#define LIBKRUN_FWD_H

#include <stdint.h>
#include <stdbool.h>

/* Sentinel return code used by the local libkrun runtime loader wrapper. */
/* It is a deliberately unique 32-bit negative code reserved to mean loader failure. */
#define KRUN_LOADER_ERR INT32_C(-1879048192)

/* Kernel image formats */
#define KRUN_KERNEL_FORMAT_RAW        0
#define KRUN_KERNEL_FORMAT_ELF        1
#define KRUN_KERNEL_FORMAT_PE_GZ      2
#define KRUN_KERNEL_FORMAT_IMAGE_BZ2  3
#define KRUN_KERNEL_FORMAT_IMAGE_GZ   4
#define KRUN_KERNEL_FORMAT_IMAGE_ZSTD 5

/* virtio-net feature bits (from uapi/linux/virtio_net.h) */
#define NET_FEATURE_CSUM       (1 << 0)
#define NET_FEATURE_GUEST_CSUM (1 << 1)
#define NET_FEATURE_GUEST_TSO4 (1 << 7)
#define NET_FEATURE_GUEST_UFO  (1 << 10)
#define NET_FEATURE_HOST_TSO4  (1 << 11)
#define NET_FEATURE_HOST_UFO   (1 << 14)

#define COMPAT_NET_FEATURES (NET_FEATURE_CSUM | NET_FEATURE_GUEST_CSUM | \
                             NET_FEATURE_GUEST_TSO4 | NET_FEATURE_GUEST_UFO | \
                             NET_FEATURE_HOST_TSO4 | NET_FEATURE_HOST_UFO)

/* Context lifecycle */
int32_t krun_create_ctx();
int32_t krun_free_ctx(uint32_t ctx_id);
int32_t krun_start_enter(uint32_t ctx_id);

/* VM configuration */
int32_t krun_set_vm_config(uint32_t ctx_id, uint8_t num_vcpus, uint32_t ram_mib);

/* virtio-console */
int32_t krun_add_virtio_console_default(uint32_t ctx_id, int input_fd, int output_fd, int err_fd);
int32_t krun_add_virtio_console_multiport(uint32_t ctx_id);
int32_t krun_add_console_port_inout(uint32_t ctx_id, uint32_t console_id, const char *name, int input_fd, int output_fd);

/* Disk */
int32_t krun_add_disk(uint32_t ctx_id, const char *block_id, const char *disk_path, bool read_only);

/* virtio-fs */
int32_t krun_add_virtiofs3(uint32_t ctx_id, const char *c_tag, const char *c_path, uint64_t shm_size, bool read_only);

/* Kernel */
int32_t krun_set_kernel(uint32_t ctx_id, const char *kernel_path, uint32_t kernel_format, const char *initramfs, const char *cmdline);

/* vsock */
int32_t krun_add_vsock(uint32_t ctx_id, uint32_t tsi_features);
int32_t krun_add_vsock_port(uint32_t ctx_id, uint32_t port, const char *c_filepath);
int32_t krun_add_vsock_port2(uint32_t ctx_id, uint32_t port, const char *c_filepath, bool listen);

/* Network */
int32_t krun_add_net_tap(uint32_t ctx_id, char *c_tap_name, uint8_t *const c_mac, uint32_t features, uint32_t flags);

#endif /* LIBKRUN_FWD_H */
