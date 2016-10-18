package shared

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestGetAllXattr(t *testing.T) {
	var (
		err       error
		testxattr = map[string]string{
			"user.checksum": "asdfsf13434qwf1324",
			"user.random":   "This is a test",
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

func TestReadLastNLines(t *testing.T) {
	source, err := ioutil.TempFile("", "")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.Remove(source.Name())

	for i := 0; i < 50; i++ {
		fmt.Fprintf(source, "%d\n", i)
	}

	lines, err := ReadLastNLines(source, 100)
	if err != nil {
		t.Error(err)
		return
	}

	split := strings.Split(lines, "\n")
	for i := 0; i < 50; i++ {
		if fmt.Sprintf("%d", i) != split[i] {
			t.Error(fmt.Sprintf("got %s expected %d", split[i], i))
			return
		}
	}

	source.Seek(0, 0)
	for i := 0; i < 150; i++ {
		fmt.Fprintf(source, "%d\n", i)
	}

	lines, err = ReadLastNLines(source, 100)
	if err != nil {
		t.Error(err)
		return
	}

	split = strings.Split(lines, "\n")
	for i := 0; i < 100; i++ {
		if fmt.Sprintf("%d", i+50) != split[i] {
			t.Error(fmt.Sprintf("got %s expected %d", split[i], i))
			return
		}
	}
}
