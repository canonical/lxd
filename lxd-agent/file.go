package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/response"
	"github.com/lxc/lxd/shared"
)

var fileCmd = APIEndpoint{
	Name: "file",
	Path: "files",

	Get:    APIEndpointAction{Handler: fileHandler},
	Post:   APIEndpointAction{Handler: fileHandler},
	Delete: APIEndpointAction{Handler: fileHandler},
}

func fileHandler(r *http.Request) response.Response {
	path := r.FormValue("path")
	if path == "" {
		return response.BadRequest(fmt.Errorf("missing path argument"))
	}

	switch r.Method {
	case "GET":
		return containerFileGet(path, r)
	case "POST":
		return containerFilePost(path, r)
	case "DELETE":
		return containerFileDelete(path, r)
	default:
		return response.NotFound(fmt.Errorf("Method '%s' not found", r.Method))
	}
}

func containerFileGet(path string, r *http.Request) response.Response {
	uid, gid, mode, type_, dirEnts, err := getFileInfo(path)
	if err != nil {
		return response.SmartError(err)
	}

	headers := map[string]string{
		"X-LXD-uid":  fmt.Sprintf("%d", uid),
		"X-LXD-gid":  fmt.Sprintf("%d", gid),
		"X-LXD-mode": fmt.Sprintf("%04o", mode),
		"X-LXD-type": type_,
	}

	if type_ == "file" || type_ == "symlink" {
		// Make a file response struct
		files := make([]response.FileResponseEntry, 1)
		files[0].Identifier = filepath.Base(path)

		f, err := ioutil.TempFile(filepath.Dir(path), "lxd_getfile_")
		if err != nil {
			return response.SmartError(err)
		}
		defer f.Close()

		if type_ == "file" {
			src, err := os.Open(path)
			if err != nil {
				return response.SmartError(err)
			}
			defer src.Close()

			_, err = io.Copy(f, src)
			if err != nil {
				return response.SmartError(err)
			}
		} else {
			target, err := os.Readlink(path)
			if err != nil {
				return response.SmartError(err)
			}

			_, err = f.WriteString(target + "\n")
			if err != nil {
				return response.SmartError(err)
			}
		}

		files[0].Path = f.Name()
		files[0].Filename = filepath.Base(path)

		return response.FileResponse(r, files, headers, true)
	} else if type_ == "directory" {
		return response.SyncResponseHeaders(true, dirEnts, headers)
	}

	return response.InternalError(fmt.Errorf("bad file type %s", type_))
}

func containerFilePost(path string, r *http.Request) response.Response {
	// Extract file ownership and mode from headers
	uid, gid, mode, type_, write := shared.ParseLXDFileHeaders(r.Header)

	if !shared.StringInSlice(write, []string{"overwrite", "append"}) {
		return response.BadRequest(fmt.Errorf("Bad file write mode: %s", write))
	}

	if type_ == "file" {
		// Write file content to a tempfile
		temp, err := ioutil.TempFile("", "lxd_forkputfile_")
		if err != nil {
			return response.InternalError(err)
		}
		defer func() {
			temp.Close()
			os.Remove(temp.Name())
		}()

		_, err = io.Copy(temp, r.Body)
		if err != nil {
			return response.InternalError(err)
		}

		// Transfer the file into the container
		err = filePush("file", temp.Name(), path, uid, gid, mode, write)
		if err != nil {
			return response.InternalError(err)
		}

		return response.EmptySyncResponse
	} else if type_ == "symlink" {
		target, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return response.InternalError(err)
		}

		err = filePush("symlink", string(target), path, uid, gid, mode, write)
		if err != nil {
			return response.InternalError(err)
		}
		return response.EmptySyncResponse
	} else if type_ == "directory" {
		err := filePush("directory", "", path, uid, gid, mode, write)
		if err != nil {
			return response.InternalError(err)
		}
		return response.EmptySyncResponse
	}

	return response.BadRequest(fmt.Errorf("Bad file type: %s", type_))
}

func containerFileDelete(path string, r *http.Request) response.Response {
	err := os.Remove(path)
	if err != nil {
		return response.SmartError(err)
	}

	return response.EmptySyncResponse
}

func getFileInfo(path string) (int64, int64, os.FileMode, string, []string, error) {
	var stat unix.Stat_t

	err := os.Chdir("/")
	if err != nil {
		return -1, -1, 0, "", nil, err
	}

	fi, err := os.Lstat(path)
	if err != nil {
		return -1, -1, 0, "", nil, err
	}

	err = unix.Lstat(path, &stat)
	if err != nil {
		return -1, -1, 0, "", nil, err
	}

	var type_ string
	var dirEnts []string

	if fi.Mode().IsDir() {
		type_ = "directory"

		f, err := os.Open(path)
		if err != nil {
			return -1, -1, 0, "", nil, err
		}

		dirEnts, err = f.Readdirnames(0)
		if err != nil {
			return -1, -1, 0, "", nil, err
		}

	} else {
		if fi.Mode()&os.ModeSymlink != 0 {
			type_ = "symlink"
		} else {
			type_ = "file"
		}
	}

	// 0xFFF = 0b7777
	return int64(stat.Uid), int64(stat.Gid), fi.Mode() & 0xFFF, type_, dirEnts, nil
}

func filePush(type_ string, srcpath string, dstpath string, uid int64, gid int64, mode int, write string) error {
	switch type_ {
	case "file":
		if !shared.PathExists(dstpath) {
			if uid == -1 {
				uid = 0
			}

			if gid == -1 {
				gid = 0
			}

			if mode == -1 {
				mode = 0
			}
		}

		flags := os.O_CREATE | os.O_WRONLY

		if write == "overwrite" {
			flags |= os.O_TRUNC
		} else if write == "append" {
			flags |= os.O_APPEND
		}

		dst, err := os.OpenFile(dstpath, flags, os.FileMode(mode))
		if err != nil {
			return err
		}
		defer dst.Close()

		src, err := os.Open(srcpath)
		if err != nil {
			return err
		}
		defer src.Close()

		_, err = io.Copy(dst, src)
		if err != nil {
			return err
		}

		err = os.Chown(dst.Name(), int(uid), int(gid))
		if err != nil {
			return err
		}

		return nil
	case "symlink":
		if uid == -1 {
			uid = 0
		}

		if gid == -1 {
			gid = 0
		}

		err := os.Symlink(srcpath, dstpath)
		if err != nil {
			return err
		}

		err = os.Lchown(dstpath, int(uid), int(gid))
		if err != nil {
			return err
		}

		return nil
	case "directory":
		if uid == -1 {
			uid = 0
		}

		if gid == -1 {
			gid = 0
		}

		if mode == -1 {
			mode = 0
		}

		err := os.MkdirAll(dstpath, os.FileMode(mode))
		if err != nil {
			return err
		}

		err = os.Chown(dstpath, int(uid), int(gid))
		if err != nil {
			return err
		}

		return nil
	}

	return fmt.Errorf("Bad file type: %s", type_)
}
