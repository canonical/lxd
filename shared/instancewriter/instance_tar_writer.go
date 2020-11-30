package instancewriter

import (
	"archive/tar"
	"io"
	"os"
	"strings"

	"github.com/pkg/errors"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/idmap"
	"github.com/lxc/lxd/shared/logger"
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

// ResetHardLinkMap resets the hard link map. Use when copying multiple instances (or snapshots) into a tarball.
// So that the hard link map doesn't work across different instances/snapshots.
func (ctw *InstanceTarWriter) ResetHardLinkMap() {
	ctw.linkMap = map[uint64]string{}
}

// WriteFile adds a file to the tarball with the specified name using the srcPath file as the contents of the file.
// The ignoreGrowth argument indicates whether to error if the srcPath file increases in size beyond the size in fi
// during the write. If false the write will return an error. If true, no error is returned, instead only the size
// specified in fi is written to the tarball. This can be used when you don't need a consistent copy of the file.
func (ctw *InstanceTarWriter) WriteFile(name string, srcPath string, fi os.FileInfo, ignoreGrowth bool) error {
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
		xattrs, err := shared.GetAllXattr(srcPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to read xattr for %q", srcPath)
		}

		hdr.PAXRecords = make(map[string]string, len(xattrs))
		for key, val := range xattrs {
			if key == "system.posix_acl_access" && ctw.idmapSet != nil {
				aclAccess, err := idmap.UnshiftACL(val, ctw.idmapSet)
				if err != nil {
					logger.Debugf("Failed to unshift ACL access permissions of %q: %v", srcPath, err)
					continue
				}
				hdr.PAXRecords["SCHILY.acl.access"] = aclAccess
			} else if key == "system.posix_acl_default" && ctw.idmapSet != nil {
				aclDefault, err := idmap.UnshiftACL(val, ctw.idmapSet)
				if err != nil {
					logger.Debugf("Failed to unshift ACL default permissions of %q: %v", srcPath, err)
					continue
				}
				hdr.PAXRecords["SCHILY.acl.default"] = aclDefault
			} else if key == "security.capability" && ctw.idmapSet != nil {
				vfsCaps, err := idmap.UnshiftCaps(val, ctw.idmapSet)
				if err != nil {
					logger.Debugf("Failed to unshift VFS capabilities of %q: %v", srcPath, err)
					continue
				}
				hdr.PAXRecords["SCHILY.xattr."+key] = vfsCaps
			} else {
				hdr.PAXRecords["SCHILY.xattr."+key] = val
			}
		}
	}

	err = ctw.tarWriter.WriteHeader(hdr)
	if err != nil {
		return errors.Wrap(err, "Failed to write tar header")
	}

	if hdr.Typeflag == tar.TypeReg {
		f, err := os.Open(srcPath)
		if err != nil {
			return errors.Wrapf(err, "Failed to open file %q", srcPath)
		}
		defer f.Close()

		r := io.Reader(f)
		if ignoreGrowth {
			r = io.LimitReader(r, fi.Size())
		}

		_, err = io.Copy(ctw.tarWriter, r)
		if err != nil {
			return errors.Wrapf(err, "Failed to copy file content %q", srcPath)
		}
	}

	return nil
}

// WriteFileFromReader streams a file into the tarball using the src reader.
// A manually generated os.FileInfo should be supplied so that the tar header can be added before streaming starts.
func (ctw *InstanceTarWriter) WriteFileFromReader(src io.Reader, fi os.FileInfo) error {
	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return errors.Wrap(err, "Failed to create tar info header")
	}

	err = ctw.tarWriter.WriteHeader(hdr)
	if err != nil {
		return errors.Wrap(err, "Failed to write tar header")
	}

	_, err = io.Copy(ctw.tarWriter, src)
	return err
}

// Close finishes writing the tarball.
func (ctw *InstanceTarWriter) Close() error {
	err := ctw.tarWriter.Close()
	if err != nil {
		return errors.Wrap(err, "Failed to close tar writer")
	}
	return nil
}
