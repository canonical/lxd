# Copilot instructions for lxd-pkg-snap

## snapcraft.yaml: source-commit entries

Every `source-commit:` line has the form:

```
source-commit: <sha1> # <version-tag>
```

**Always update both fields together.** When bumping a dependency to a new
version, the SHA1 hash *and* the version comment must change at the same time.
Updating only the comment (leaving the old SHA1) silently pins the build to the
wrong upstream revision.
