# Go coverage-related test helpers.

# setup_gocoverdir: create GOCOVERDIR and make it usable by non-root users.
# Coverage-instrumented binaries may run as non-root users (e.g. testuser in
# the lxd-user tests), so besides making GOCOVERDIR itself world-writable,
# all its parent directories must be traversable (execute bit) for the Go
# coverage runtime to be able to reach it.
setup_gocoverdir() {
    [ -n "${GOCOVERDIR:-}" ] || return 0

    mkdir -p "${GOCOVERDIR}"

    # Normalize to an absolute path so binaries running with a different
    # working directory can still reach it, and so the parent directory
    # iteration below reliably terminates at the filesystem root.
    GOCOVERDIR="$(cd "${GOCOVERDIR}" && pwd)"

    chmod 0777 "${GOCOVERDIR}"

    local parent
    parent="$(dirname "${GOCOVERDIR}")"
    while [ "${parent}" != "/" ]; do
        chmod o+x "${parent}"
        parent="$(dirname "${parent}")"
    done
}
