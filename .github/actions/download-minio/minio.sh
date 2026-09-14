# download_minio: downloads minio server and mc client binaries to GOPATH/bin or /usr/local/bin.
download_minio() {
    local arch dir
    dir="${GOPATH:-${HOME}/go}/bin"
    mkdir -p "${dir}"

    arch="${ARCH:-$(dpkg --print-architecture || echo "amd64")}"

    # Download minio and mc binaries
    curl --show-error --silent --retry 3 --retry-delay 5 --location --fail \
        --continue-at - "https://github.com/minio/minio/releases/download/RELEASE.2025-09-07T16-13-09Z/minio.linux-${arch}.RELEASE.2025-09-07T16-13-09Z" --output "${dir}/minio" \
        --continue-at - "https://github.com/minio/mc/releases/download/RELEASE.2025-08-13T08-35-41Z/mc.linux-${arch}.RELEASE.2025-08-13T08-35-41Z"       --output "${dir}/mc"
    chmod +x "${dir}/minio" "${dir}/mc"
}
