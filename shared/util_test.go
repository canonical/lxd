package shared

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"testing"
)

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
	fmt.Println(lines)

	split = strings.Split(lines, "\n")
	for i := 0; i < 100; i++ {
		if fmt.Sprintf("%d", i+50) != split[i] {
			t.Error(fmt.Sprintf("got %s expected %d", split[i], i))
			return
		}
	}
}
