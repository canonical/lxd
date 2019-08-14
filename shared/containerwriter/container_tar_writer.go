package containerwriter

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
)

type ContainerTarWriter struct {
	tarWriter *tar.Writer
	idmapSet  *idmap.IdmapSet
	linkMap   map[uint64]string
}

func NewContainerTarWriter(writer io.Writer, idmapSet *idmap.IdmapSet) *ContainerTarWriter {
	ctw := new(ContainerTarWriter)
	ctw.tarWriter = tar.NewWriter(writer)
	ctw.idmapSet = idmapSet
	ctw.linkMap = map[uint64]string{}
	return ctw
}

func (ctw *ContainerTarWriter) WriteFile(offset int, path string, fi os.FileInfo) error {
	var err error
	var major, minor uint32
	var nlink int
	var ino uint64

	link := ""
	if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		link, err = os.Readlink(path)
		if err != nil {
			return fmt.Errorf("failed to resolve symlink: %s", err)
		}
	}

	// Sockets cannot be stored in tarballs, just skip them (consistent with tar)
	if fi.Mode()&os.ModeSocket == os.ModeSocket {
		return nil
	}

	hdr, err := tar.FileInfoHeader(fi, link)
	if err != nil {
		return fmt.Errorf("failed to create tar info header: %s", err)
	}

	hdr.Name = path[offset:]
	if fi.IsDir() || fi.Mode()&os.ModeSymlink == os.ModeSymlink {
		hdr.Size = 0
	} else {
		hdr.Size = fi.Size()
	}

	hdr.Uid, hdr.Gid, major, minor, ino, nlink, err = shared.GetFileStat(path)
	if err != nil {
		return fmt.Errorf("failed to get file stat: %s", err)
	}

	// Unshift the id under /rootfs/ for unpriv containers
	if strings.HasPrefix(hdr.Name, "/rootfs") {
		if ctw.idmapSet != nil {
			hUid, hGid := ctw.idmapSet.ShiftFromNs(int64(hdr.Uid), int64(hdr.Gid))
			hdr.Uid = int(hUid)
			hdr.Gid = int(hGid)
			if hdr.Uid == -1 || hdr.Gid == -1 {
				return nil
			}
		}
	}

	hdr.Devmajor = int64(major)
	hdr.Devminor = int64(minor)

	// If it's a hardlink we've already seen use the old name
	if fi.Mode().IsRegular() && nlink > 1 {
		if firstPath, found := ctw.linkMap[ino]; found {
			hdr.Typeflag = tar.TypeLink
			hdr.Linkname = firstPath
			hdr.Size = 0
		} else {
			ctw.linkMap[ino] = hdr.Name
		}
	}

	// Handle xattrs (for real files only)
	if link == "" {
		hdr.Xattrs, err = shared.GetAllXattr(path)
		if err != nil {
			return fmt.Errorf("failed to read xattr for '%s': %s", path, err)
		}
	}

	if err := ctw.tarWriter.WriteHeader(hdr); err != nil {
		return fmt.Errorf("failed to write tar header: %s", err)
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("failed to open the file: %s", err)
		}
		defer f.Close()

		if _, err := io.Copy(ctw.tarWriter, f); err != nil {
			return fmt.Errorf("failed to copy file content: %s", err)
		}
	}

	return nil
}

func (ctw *ContainerTarWriter) Close() error {
	err := ctw.tarWriter.Close()
	if err != nil {
		return fmt.Errorf("failed to close tar writer: %s", err)
	}
	return nil
}
