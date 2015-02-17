#!/bin/bash

set -e

usage() {
	echo "Usage: $0 [distro] [release] [arch]"
}

if [ "$1" = "help" -o "$1" = "--help" ]; then
	usage
	exit 0
fi

arch=amd64
rel=vivid
distro=ubuntu
if [ $# -ge 3 ]; then
	arch=$3
fi
if [ $# -ge 2 ]; then
	rel=$2
fi
if [ $# -ge 1 ]; then
	distro=$1
fi

d=${distro}-${rel}-${arch}

dest=`mktemp -d ${d}-XXXX`
echo "Placing results in $dest"

# functions borrowed from the download template
DOWNLOAD_KEYSERVER="hkp://pool.sks-keyservers.net"
DOWNLOAD_KEYID="0xBAEFF88C22F6E216"

gpg_setup() {
    echo "Setting up the GPG keyring"

    mkdir -p gpg
    chmod 700 "gpg"
    export GNUPGHOME="`pwd`/gpg"

    success=
    for i in $(seq 3); do
        if gpg --keyserver $DOWNLOAD_KEYSERVER \
            --recv-keys ${DOWNLOAD_KEYID} >/dev/null 2>&1; then
            success=1
            break
        fi
    done

    if [ -z "$success" ]; then
        echo "ERROR: Unable to fetch GPG key from keyserver."
        exit 1
    fi
}

gpg_validate() {
    if ! gpg --verify $1 >/dev/zero 2>&1; then
        echo "ERROR: Invalid signature for $1" 1>&2
        exit 1
    fi
}

cd $dest

gpg_setup
wget http://images.linuxcontainers.org/meta/1.0/index-system
urldate=`grep "$distro;$rel;$arch" index-system | cut -d \; -f 5`
f=${distro}-${rel}-${arch}-$urldate.tar.xz
urldir=`grep "$distro;$rel;$arch" index-system | cut -d \; -f 6`
url="http://images.linuxcontainers.org/$urldir"
wget ${url}/rootfs.tar.xz
wget ${url}/rootfs.tar.xz.asc

gpg_validate rootfs.tar.xz.asc

# The properties aren't quite right - worry about that later
# when something actually parses it
cat > metadata.yaml << EOF
    {
        'architecture': "$arch",
        'creation_date': $urldir,
        'properties': {
            'os': "$distro",
            'release': ["$rel", "14.04"],
            'description': "$distro $rel $arch"},
            'name': "ubuntu-14.04-amd64-20150218"
            'name': "$distro-$arch-$urldate"
    }
EOF
tar Jcf ../$f metadata.yaml rootfs.tar.xz
echo "result is in $f, workdir was $dest"
