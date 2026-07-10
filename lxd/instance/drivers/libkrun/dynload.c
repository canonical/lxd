#include <dlfcn.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

#include "libkrun_fwd.h"

struct krun_api {
    __typeof__(krun_create_ctx) *create_ctx;
    __typeof__(krun_free_ctx) *free_ctx;
    __typeof__(krun_set_vm_config) *set_vm_config;
    __typeof__(krun_add_virtio_console_default) *add_virtio_console_default;
    __typeof__(krun_add_virtio_console_multiport) *add_virtio_console_multiport;
    __typeof__(krun_add_console_port_inout) *add_console_port_inout;
    __typeof__(krun_set_kernel) *set_kernel;
    __typeof__(krun_add_disk) *add_disk;
    __typeof__(krun_add_virtiofs3) *add_virtiofs3;
    __typeof__(krun_add_vsock) *add_vsock;
    __typeof__(krun_add_vsock_port) *add_vsock_port;
    __typeof__(krun_add_vsock_port2) *add_vsock_port2;
    __typeof__(krun_add_net_tap) *add_net_tap;
    __typeof__(krun_start_enter) *start_enter;
};

static struct {
    pthread_once_t once;
    void *handle;
    int init_ok;
    char err[1024];
    struct krun_api api;
} loader = {
    .once = PTHREAD_ONCE_INIT,
    .init_ok = 0,
    .err = "Failed loading libkrun: uninitialized",
};

static void loader_set_err(const char *fmt, ...) {
    va_list ap;

    va_start(ap, fmt);
    vsnprintf(loader.err, sizeof(loader.err), fmt, ap);
    va_end(ap);
}

static void *loader_open(const char *path) {
    void *handle;

    dlerror();
    handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
    if (handle == NULL) {
        const char *derr = dlerror();
        loader_set_err("libkrun loader: dlopen(%s) failed: %s", path, derr ? derr : "unknown error");
    }

    return handle;
}

static bool loader_resolve(void **target, const char *name) {
    void *sym;
    const char *derr;

    dlerror();
    sym = dlsym(loader.handle, name);
    derr = dlerror();
    if (derr != NULL || sym == NULL) {
        loader_set_err("libkrun loader: required symbol %s missing: %s", name, derr ? derr : "unknown error");
        return false;
    }

    *target = sym;
    return true;
}

#define RESOLVE_REQUIRED(field, symbol) \
    do { \
        if (!loader_resolve((void **)&loader.api.field, #symbol)) { \
            return; \
        } \
    } while (0)

static void loader_init_once(void) {
    const char *override;
    const char *candidates[] = {
        "libkrun.so",
        "libkrun.so.0",
        NULL,
    };
    size_t i;

    override = getenv("LIBKRUN_PATH");
    if (override != NULL && override[0] != '\0') {
        loader.handle = loader_open(override);
        if (loader.handle == NULL) {
            return;
        }
    } else {
        for (i = 0; candidates[i] != NULL; i++) {
            loader.handle = loader_open(candidates[i]);
            if (loader.handle != NULL) {
                break;
            }
        }

        if (loader.handle == NULL) {
            loader_set_err("libkrun loader: cannot open %s or %s (set LIBKRUN_PATH to an explicit .so path)",
                           candidates[0], candidates[1]);
            return;
        }
    }

    RESOLVE_REQUIRED(create_ctx, krun_create_ctx);
    RESOLVE_REQUIRED(free_ctx, krun_free_ctx);
    RESOLVE_REQUIRED(set_vm_config, krun_set_vm_config);
    RESOLVE_REQUIRED(add_virtio_console_default, krun_add_virtio_console_default);
    RESOLVE_REQUIRED(add_virtio_console_multiport, krun_add_virtio_console_multiport);
    RESOLVE_REQUIRED(add_console_port_inout, krun_add_console_port_inout);
    RESOLVE_REQUIRED(set_kernel, krun_set_kernel);
    RESOLVE_REQUIRED(add_disk, krun_add_disk);
    RESOLVE_REQUIRED(add_virtiofs3, krun_add_virtiofs3);
    RESOLVE_REQUIRED(add_vsock, krun_add_vsock);
    RESOLVE_REQUIRED(add_vsock_port, krun_add_vsock_port);
    RESOLVE_REQUIRED(add_vsock_port2, krun_add_vsock_port2);
    RESOLVE_REQUIRED(add_net_tap, krun_add_net_tap);
    RESOLVE_REQUIRED(start_enter, krun_start_enter);

    loader.init_ok = 1;
}

static bool loader_ready(void) {
    pthread_once(&loader.once, loader_init_once);
    return loader.init_ok == 1;
}

const char *goKrunLoaderLastError(void) {
    pthread_once(&loader.once, loader_init_once);
    return loader.err;
}

int32_t krun_create_ctx(void) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.create_ctx();
}

int32_t krun_free_ctx(uint32_t ctx_id) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.free_ctx(ctx_id);
}

int32_t krun_set_vm_config(uint32_t ctx_id, uint8_t num_vcpus, uint32_t ram_mib) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.set_vm_config(ctx_id, num_vcpus, ram_mib);
}

int32_t krun_add_virtio_console_default(uint32_t ctx_id, int input_fd, int output_fd, int err_fd) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_virtio_console_default(ctx_id, input_fd, output_fd, err_fd);
}

int32_t krun_add_virtio_console_multiport(uint32_t ctx_id) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_virtio_console_multiport(ctx_id);
}

int32_t krun_add_console_port_inout(uint32_t ctx_id, uint32_t console_id, const char *name, int input_fd, int output_fd) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_console_port_inout(ctx_id, console_id, name, input_fd, output_fd);
}

int32_t krun_set_kernel(uint32_t ctx_id,
                        const char *kernel_path,
                        uint32_t kernel_format,
                        const char *initramfs,
                        const char *cmdline) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.set_kernel(ctx_id, kernel_path, kernel_format, initramfs, cmdline);
}

int32_t krun_add_disk(uint32_t ctx_id, const char *block_id, const char *disk_path, bool read_only) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_disk(ctx_id, block_id, disk_path, read_only);
}

int32_t krun_add_virtiofs3(uint32_t ctx_id, const char *c_tag, const char *c_path, uint64_t shm_size, bool read_only) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_virtiofs3(ctx_id, c_tag, c_path, shm_size, read_only);
}

int32_t krun_add_vsock(uint32_t ctx_id, uint32_t tsi_features) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_vsock(ctx_id, tsi_features);
}

int32_t krun_add_vsock_port(uint32_t ctx_id, uint32_t port, const char *c_filepath) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_vsock_port(ctx_id, port, c_filepath);
}

int32_t krun_add_vsock_port2(uint32_t ctx_id, uint32_t port, const char *c_filepath, bool listen) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_vsock_port2(ctx_id, port, c_filepath, listen);
}

int32_t krun_add_net_tap(uint32_t ctx_id,
                         char *c_tap_name,
                         uint8_t *const c_mac,
                         uint32_t features,
                         uint32_t flags) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.add_net_tap(ctx_id, c_tap_name, c_mac, features, flags);
}

int32_t krun_start_enter(uint32_t ctx_id) {
    if (!loader_ready()) {
        return KRUN_LOADER_ERR;
    }

    return loader.api.start_enter(ctx_id);
}
