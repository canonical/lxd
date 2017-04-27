package cmd

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/lxc/lxd/shared"
)

// Context captures the environment the sub-command is being run in,
// such as in/out/err streams and command line arguments.
type Context struct {
	stdin  *bufio.Reader
	stdout io.Writer
	stderr io.Writer
}

// NewContext creates a new command context with the given parameters.
func NewContext(stdin io.Reader, stdout, stderr io.Writer) *Context {
	return &Context{
		stdin:  bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
	}
}

// AskBool asks a question an expect a yes/no answer.
func (c *Context) AskBool(question string, defaultAnswer string) bool {
	for {
		fmt.Fprintf(c.stdout, question)
		answer := c.readAnswer(defaultAnswer)

		if shared.StringInSlice(strings.ToLower(answer), []string{"yes", "y"}) {
			return true
		} else if shared.StringInSlice(strings.ToLower(answer), []string{"no", "n"}) {
			return false
		}

		fmt.Fprintf(c.stderr, "Invalid input, try again.\n\n")
	}
}

// Read the user's answer from the input stream, trimming newline and providing a default.
func (c *Context) readAnswer(defaultAnswer string) string {
	answer, _ := c.stdin.ReadString('\n')
	answer = strings.TrimSuffix(answer, "\n")
	if answer == "" {
		answer = defaultAnswer
	}
	return answer
}
