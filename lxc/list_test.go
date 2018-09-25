package main

import (
	"bytes"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
)

func TestDotPrefixMatch(t *testing.T) {
	list := cmdList{}

	pass := true
	pass = pass && list.dotPrefixMatch("s.privileged", "security.privileged")
	pass = pass && list.dotPrefixMatch("u.blah", "user.blah")

	if !pass {
		t.Error("failed prefix matching")
	}
}

func TestShouldShow(t *testing.T) {
	list := cmdList{}

	state := &api.Container{
		Name: "foo",
		ExpandedConfig: map[string]string{
			"security.privileged": "1",
			"user.blah":           "abc",
		},
	}

	if !list.shouldShow([]string{"u.blah=abc"}, state) {
		t.Error("u.blah=abc didn't match")
	}

	if !list.shouldShow([]string{"user.blah=abc"}, state) {
		t.Error("user.blah=abc didn't match")
	}

	if list.shouldShow([]string{"bar", "u.blah=abc"}, state) {
		t.Errorf("name filter didn't work")
	}

	if list.shouldShow([]string{"bar", "u.blah=other"}, state) {
		t.Errorf("value filter didn't work")
	}
}

// Used by TestColumns and TestInvalidColumns
const shorthand = "46abcdfFlnNpPsStL"
const alphanum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func TestColumns(t *testing.T) {
	keys := make([]string, 0, len(shared.KnownContainerConfigKeys))
	for k := range shared.KnownContainerConfigKeys {
		keys = append(keys, k)
	}

	randShorthand := func(buffer *bytes.Buffer) {
		buffer.WriteByte(shorthand[rand.Intn(len(shorthand))])
	}

	randString := func(buffer *bytes.Buffer) {
		l := rand.Intn(20)
		if l == 0 {
			l = rand.Intn(20) + 20
		}
		for i := 0; i < l; i++ {
			buffer.WriteByte(alphanum[rand.Intn(len(alphanum))])
		}
	}

	randConfigKey := func(buffer *bytes.Buffer) {
		// Unconditionally prepend a comma so that we don't create an invalid
		// column string, redundant commas will be handled immediately prior
		// to parsing the string.
		buffer.WriteRune(',')

		switch rand.Intn(4) {
		case 0:
			buffer.WriteString(keys[rand.Intn(len(keys))])
		case 1:
			buffer.WriteString("user.")
			randString(buffer)
		case 2:
			buffer.WriteString("environment.")
			randString(buffer)
		case 3:
			if rand.Intn(2) == 0 {
				buffer.WriteString("volatile.")
				randString(buffer)
				buffer.WriteString(".hwaddr")
			} else {
				buffer.WriteString("volatile.")
				randString(buffer)
				buffer.WriteString(".name")
			}
		}

		// Randomize the optional fields in a single shot.  Empty names are legal
		// when specifying the max width, append an extra colon in this case.
		opt := rand.Intn(8)
		if opt&1 != 0 {
			buffer.WriteString(":")
			randString(buffer)
		} else if opt != 0 {
			buffer.WriteString(":")
		}

		switch opt {
		case 2, 3:
			buffer.WriteString(":-1")
		case 4, 5:
			buffer.WriteString(":0")
		case 6, 7:
			buffer.WriteRune(':')
			buffer.WriteString(strconv.FormatUint(uint64(rand.Uint32()), 10))
		}

		// Unconditionally append a comma so that we don't create an invalid
		// column string, redundant commas will be handled immediately prior
		// to parsing the string.
		buffer.WriteRune(',')
	}

	for i := 0; i < 1000; i++ {
		go func() {
			var buffer bytes.Buffer

			l := rand.Intn(10)
			if l == 0 {
				l = rand.Intn(10) + 10
			}

			num := l
			for j := 0; j < l; j++ {
				switch rand.Intn(5) {
				case 0:
					if buffer.Len() > 0 {
						buffer.WriteRune(',')
						num--
					} else {
						randShorthand(&buffer)
					}

				case 1, 2:
					randShorthand(&buffer)
				case 3, 4:
					randConfigKey(&buffer)
				}
			}

			// Generate the column string, removing any leading, trailing or duplicate commas.
			raw := shared.RemoveDuplicatesFromString(strings.Trim(buffer.String(), ","), ",")

			list := cmdList{flagColumns: raw}

			clustered := strings.Contains(raw, "L")
			columns, _, err := list.parseColumns(clustered)
			if err != nil {
				t.Errorf("Failed to parse columns string.  Input: %s, Error: %s", raw, err)
			}
			if len(columns) != num {
				t.Errorf("Did not generate correct number of columns.  Expected: %d, Actual: %d, Input: %s", num, len(columns), raw)
			}
		}()
	}
}

func TestInvalidColumns(t *testing.T) {
	run := func(raw string) {
		list := cmdList{flagColumns: raw}
		_, _, err := list.parseColumns(true)
		if err == nil {
			t.Errorf("Expected error from parseColumns, received nil.  Input: %s", raw)
		}
	}

	for _, v := range alphanum {
		if !strings.ContainsRune(shorthand, v) {
			run(string(v))
		}
	}

	run(",")
	run(",a")
	run("a,")
	run("4,,6")
	run(".")
	run(":")
	run("::")
	run(".key:")
	run("user.key:")
	run("user.key::")
	run(":user.key")
	run(":user.key:0")
	run("user.key::-2")
	run("user.key:name:-2")
	run("volatile")
	run("base_image")
	run("volatile.image")
}
