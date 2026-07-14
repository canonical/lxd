# libkrun (reduced)

Minimal Go bindings for libkrun required by LXD.

## Runtime Loading

The package resolves libkrun at runtime with `dlopen(3)`/`dlsym(3)`.

- If `LIBKRUN_PATH` is set, it is used first.
- Otherwise it falls back to `libkrun.so` then `libkrun.so.0`.
