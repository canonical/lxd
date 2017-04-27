package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lxc/lxd/shared/cmd"
)

// AskBool returns a boolean result depending on the user input.
func TestAskBool(t *testing.T) {
	cases := []struct {
		question      string
		defaultAnswer string
		output        string
		error         string
		input         string
		result        bool
	}{
		{"Do you code?", "yes", "Do you code?", "", "\n", true},
		{"Do you code?", "yes", "Do you code?", "", "yes\n", true},
		{"Do you code?", "yes", "Do you code?", "", "y\n", true},
		{"Do you code?", "yes", "Do you code?", "", "no\n", false},
		{"Do you code?", "yes", "Do you code?", "", "n\n", false},
		{"Do you code?", "yes", "Do you code?Do you code?", "Invalid input, try again.\n\n", "foo\nyes\n", true},
	}
	for _, c := range cases {
		stdin := strings.NewReader(c.input)
		stdout := new(bytes.Buffer)
		stderr := new(bytes.Buffer)
		context := cmd.NewContext(stdin, stdout, stderr)
		result := context.AskBool(c.question, c.defaultAnswer)

		if result != c.result {
			t.Errorf("Expected '%v' result got '%v'", c.result, result)
		}

		if output := stdout.String(); output != c.output {
			t.Errorf("Expected '%s' output got '%s'", c.output, output)
		}

		if error := stderr.String(); error != c.error {
			t.Errorf("Expected '%s' error got '%s'", c.error, error)
		}
	}
}
