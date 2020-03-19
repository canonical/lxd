package instancewriter

import (
	"archive/tar"
	"io"
	"os"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
)

// InstanceTarWriter provides a TarWriter implementation that handles ID shifting and hardlink tracking.
type InstanceTarWriter struct {
	tarWriter *tar.Writer
	idmapSet  *idmap.IdmapSet
	linkMap   map[uint64]string
}

// NewInstanceTarWriter returns a ContainerTarWriter for the provided target Writer and id map.
func NewInstanceTarWriter(writer io.Writer, idmapSet *idmap.IdmapSet) *InstanceTarWriter {
	ctw := new(InstanceTarWriter)
	ctw.tarWriter = tar.NewWriter(writer)
	ctw.idmapSet = idmapSet
	ctw.linkMap = map[uint64]string{}
	return ctw
}

// WriteFile adds a file to the tarball with the specified name using the srcPath file as the contents of the file.
func (ctw *InstanceTarWriter) WriteFile(name string, srcPath string, fi os.FileInfo) error {
	var err error
	var major, minor uint32
	var nlink int
	var ino uint64

	link := ""
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		link, err = os.Readlink(srcPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to resolve symlink for %q", srcPath)
		}
	}

	// Sockets cannot be stored in tarballs, just skip them (consistent with tar).
	if fi.Mode()&os.ModeSocket == os.ModeSocket {
		return nil
	}

	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return errors.Wrap(err, "Failed to create tar info header")
	}

	hdr.Name = name
	if fi.IsDir() || fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		hdr.Size = 0
	} else {
		hdr.Size = fi.Size()
	}

	hdr.Uid, hdr.Gid, major, minor, ino, nlink, err = shared.GetFileStat(srcPath)
	if err != nil {
		return errors.Wrapf(err, "Failed to get file stat %q", srcPath)
	}

	// Unshift the id under rootfs/ for unpriv containers.
	if strings.HasPrefix(hdr.Name, "rootfs") && ctw.idmapSet != nil {
		hUID, hGID := ctw.idmapSet.ShiftFromNs(int64(hdr.Uid), int64(hdr.Gid))
		hdr.Uid = int(hUID)
		hdr.Gid = int(hGID)
		if hdr.Uid == -1 || hdr.Gid == -1 {
			return nil
		}
	}

	hdr.Devmajor = int64(major)
	hdr.Devminor = int64(minor)

	// If it's a hardlink we've already seen use the old name.
	if fi.Mode().IsRegular() && nlink > 1 {
		if firstPath, found := ctw.linkMap[ino]; found {
			hdr.Typeflag = tar.TypeLink
			hdr.Linkname = firstPath
			hdr.Size = 0
		} else {
			ctw.linkMap[ino] = hdr.Name
		}
	}

	// Handle xattrs (for real files only).
	if link == "" {
		hdr.Xattrs, err = shared.GetAllXattr(srcPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to read xattr for %q", srcPath)
		}
	}

	if err := ctw.tarWriter.WriteHeader(hdr); err != nil {
		return errors.Wrap(err, "Failed to write tar header")
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(srcPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to open file %q", srcPath)
		}
		defer f.Close()

		if _, err := io.Copy(ctw.tarWriter, f); err != nil {
			return errors.Wrapf(err, "Failed to copy file content %q", srcPath)
		}
	}

	return nil
}

// Close finishes writing the tarball.
func (ctw *InstanceTarWriter) Close() error {
	err := ctw.tarWriter.Close()
	if err != nil {
		return errors.Wrap(err, "Failed to close tar writer")
	}
	return nil
}
