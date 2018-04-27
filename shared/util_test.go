package shared

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestURLEncode(t *testing.T) {
	url, _ := URLEncode(
		"/path/with spaces",
		map[string]string{"param": "with spaces", "other": "without"})
	expected := "/path/with%20spaces?other=without&param=with+spaces"
	if url != expected {
		t.Error(fmt.Errorf("'%s' != '%s'", url, expected))
	}
}

func TestFileCopy(t *testing.T) {
	helloWorld := []byte("hello world\n")
	source, err := ioutil.TempFile("", "")
	if err != nil {
		t.Error(err)
		return
	}
	defer os.Remove(source.Name())

	if err := WriteAll(source, helloWorld); err != nil {
		source.Close()
		t.Error(err)
		return
	}
	source.Close()

	dest, err := ioutil.TempFile("", "")
	defer os.Remove(dest.Name())
	if err != nil {
		t.Error(err)
		return
	}
	dest.Close()

	if err := FileCopy(source.Name(), dest.Name()); err != nil {
		t.Error(err)
		return
	}

	dest2, err := os.Open(dest.Name())
	if err != nil {
		t.Error(err)
		return
	}

	content, err := ioutil.ReadAll(dest2)
	if err != nil {
		t.Error(err)
		return
	}

	if string(content) != string(helloWorld) {
		t.Error("content mismatch: ", string(content), "!=", string(helloWorld))
		return
	}
}

func TestDirCopy(t *testing.T) {
	dir, err := ioutil.TempDir("", "lxd-shared-util-")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	source := filepath.Join(dir, "source")
	dest := filepath.Join(dir, "dest")

	dir1 := "dir1"
	dir2 := "dir2"

	file1 := "file1"
	file2 := "dir1/file1"

	content1 := []byte("file1")
	content2 := []byte("file2")

	require.NoError(t, os.Mkdir(source, 0755))
	require.NoError(t, os.Mkdir(filepath.Join(source, dir1), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(source, dir2), 0755))
	require.NoError(t, ioutil.WriteFile(filepath.Join(source, file1), content1, 0755))
	require.NoError(t, ioutil.WriteFile(filepath.Join(source, file2), content2, 0755))

	require.NoError(t, DirCopy(source, dest))

	for _, path := range []string{dir1, dir2, file1, file2} {
		assert.True(t, PathExists(filepath.Join(dest, path)))
	}

	bytes, err := ioutil.ReadFile(filepath.Join(dest, file1))
	require.NoError(t, err)
	assert.Equal(t, content1, bytes)

	bytes, err = ioutil.ReadFile(filepath.Join(dest, file2))
	require.NoError(t, err)
	assert.Equal(t, content2, bytes)
}

func TestReaderToChannel(t *testing.T) {
	buf := make([]byte, 1*1024*1024)
	rand.Read(buf)

	offset := 0
	finished := false

	ch := ReaderToChannel(bytes.NewBuffer(buf), -1)
	for {
		data, ok := <-ch
		if len(data) > 0 {
			for i := 0; i < len(data); i++ {
				if buf[offset+i] != data[i] {
					t.Error(fmt.Sprintf("byte %d didn't match", offset+i))
					return
				}
			}

			offset += len(data)
			if offset > len(buf) {
				t.Error("read too much data")
				return
			}

			if offset == len(buf) {
				finished = true
			}
		}

		if !ok {
			if !finished {
				t.Error("connection closed too early")
				return
			} else {
				break
			}
		}
	}
}
