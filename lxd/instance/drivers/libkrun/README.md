# libkrun (reduced)

Minimal Go bindings for libkrun required by LXD.

## Runtime Loading

The package resolves libkrun at runtime with `dlopen(3)`/`dlsym(3)`.

- `SetLibkrunPath(path)` sets the path of the library. It must be called before any other function.
- The library is opened on the first libkrun call, usually `CreateContext()`.
- LXD looks for the library, and for the MicroVM kernel, only in `$LXD_DIR/libkrun/`.
