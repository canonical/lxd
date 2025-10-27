# download_minio: downloads minio server and mc client binaries to GOPATH/bin or /usr/local/bin.
download_minio() {
    local arch dir
    dir="${GOPATH:-${HOME}/go}/bin"
    mkdir -p "${dir}"

    arch="${ARCH:-$(dpkg --print-architecture || echo "amd64")}"

    # Download minio and mc binaries
    curl --show-error --silent --retry 3 --retry-delay 5 \
        --continue-at - "https://dl.min.io/server/minio/release/linux-${arch}/minio" --output "${dir}/minio" \
        --continue-at - "https://dl.min.io/client/mc/release/linux-${arch}/mc"       --output "${dir}/mc"
    chmod +x "${dir}/minio" "${dir}/mc"
}
