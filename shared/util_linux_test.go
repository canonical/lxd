package shared

import (
	"io/ioutil"
	"os"
	"syscall"
	"testing"
)

func TestGetAllXattr(t *testing.T) {
	var (
		err       error
		testxattr = map[string]string{
			"user.checksum": "asdfsf13434qwf1324",
			"user.random":   "This is a test",
			"user.empty":    "",
		}
	)
	xattrFile, err := ioutil.TempFile("", "")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.Remove(xattrFile.Name())
	xattrFile.Close()

	xattrDir, err := ioutil.TempDir("", "")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.Remove(xattrDir)

	for k, v := range testxattr {
		err = syscall.Setxattr(xattrFile.Name(), k, []byte(v), 0)
		if err == syscall.ENOTSUP {
			t.Log(err)
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
		err = syscall.Setxattr(xattrDir, k, []byte(v), 0)
		if err == syscall.ENOTSUP {
			t.Log(err)
			return
		}
		if err != nil {
			t.Error(err)
			return
		}
	}

	// Test retrieval of extended attributes for regular files.
	h, err := GetAllXattr(xattrFile.Name())
	if err != nil {
		t.Error(err)
		return
	}

	if h == nil {
		t.Errorf("Expected to find extended attributes but did not find any.")
		return
	}

	for k, v := range h {
		found, ok := h[k]
		if !ok || found != testxattr[k] {
			t.Errorf("Expected to find extended attribute %s with a value of %s on regular file but did not find it.", k, v)
			return
		}
	}

	// Test retrieval of extended attributes for directories.
	h, err = GetAllXattr(xattrDir)
	if err != nil {
		t.Error(err)
		return
	}

	if h == nil {
		t.Errorf("Expected to find extended attributes but did not find any.")
		return
	}

	for k, v := range h {
		found, ok := h[k]
		if !ok || found != testxattr[k] {
			t.Errorf("Expected to find extended attribute %s with a value of %s on directory but did not find it.", k, v)
			return
		}
	}
}
