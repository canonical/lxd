package shared

import (
	"io/ioutil"
	"os"
	"testing"
)

func TestCopyFile(t *testing.T) {
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
	if err != nil {
		t.Error(err)
		return
	}
	dest.Close()

	if err := CopyFile(dest.Name(), source.Name()); err != nil {
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
