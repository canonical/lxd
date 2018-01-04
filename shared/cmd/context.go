package cmd

import (
	"bufio"
	"fmt"
	"gopkg.in/yaml.v2"
	"io"
	"io/ioutil"
	"os"
	"strconv"
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

// DefaultContext returns a new Context connected the stdin, stdout and stderr
// streams.
func DefaultContext() *Context {
	return NewContext(os.Stdin, os.Stderr, os.Stdout)
}

// NewContext creates a new command context with the given parameters.
func NewContext(stdin io.Reader, stdout, stderr io.Writer) *Context {
	return &Context{
		stdin:  bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
	}
}

// Output prints a message on standard output.
func (c *Context) Output(format string, a ...interface{}) {
	fmt.Fprintf(c.stdout, format, a...)
}

// Error prints a message on standard error.
func (c *Context) Error(format string, a ...interface{}) {
	fmt.Fprintf(c.stderr, format, a...)
}

// AskBool asks a question an expect a yes/no answer.
func (c *Context) AskBool(question string, defaultAnswer string) bool {
	for {
		answer := c.askQuestion(question, defaultAnswer)

		if shared.StringInSlice(strings.ToLower(answer), []string{"yes", "y"}) {
			return true
		} else if shared.StringInSlice(strings.ToLower(answer), []string{"no", "n"}) {
			return false
		}

		c.invalidInput()
	}
}

// AskChoice asks the user to select between a set of choices
func (c *Context) AskChoice(question string, choices []string, defaultAnswer string) string {
	for {
		answer := c.askQuestion(question, defaultAnswer)

		if shared.StringInSlice(answer, choices) {
			return answer
		}

		c.invalidInput()
	}
}

// AskInt asks the user to enter an integer between a min and max value
func (c *Context) AskInt(question string, min int64, max int64, defaultAnswer string) int64 {
	for {
		answer := c.askQuestion(question, defaultAnswer)

		result, err := strconv.ParseInt(answer, 10, 64)

		if err == nil && (min == -1 || result >= min) && (max == -1 || result <= max) {
			return result
		}

		c.invalidInput()
	}
}

// AskString asks the user to enter a string, which optionally
// conforms to a validation function.
func (c *Context) AskString(question string, defaultAnswer string, validate func(string) error) string {
	for {
		answer := c.askQuestion(question, defaultAnswer)

		if validate != nil {
			error := validate(answer)
			if error != nil {
				fmt.Fprintf(c.stderr, "Invalid input: %s\n\n", error)
				continue
			}
		}
		if len(answer) != 0 {
			return answer
		}

		c.invalidInput()
	}
}

// AskPassword asks the user to enter a password. The reader function used to
// read the password without echoing characters must be passed (usually
// terminal.ReadPassword from golang.org/x/crypto/ssh/terminal).
func (c *Context) AskPassword(question string, reader func(int) ([]byte, error)) string {
	for {
		fmt.Fprintf(c.stdout, question)

		pwd, _ := reader(0)
		fmt.Fprintf(c.stdout, "\n")
		inFirst := string(pwd)
		inFirst = strings.TrimSuffix(inFirst, "\n")

		fmt.Fprintf(c.stdout, "Again: ")
		pwd, _ = reader(0)
		fmt.Fprintf(c.stdout, "\n")
		inSecond := string(pwd)
		inSecond = strings.TrimSuffix(inSecond, "\n")

		if inFirst == inSecond {
			return inFirst
		}

		c.invalidInput()
	}
}

// InputYAML treats stdin as YAML content and returns the unmarshalled
// structure
func (c *Context) InputYAML(out interface{}) error {
	bytes, err := ioutil.ReadAll(c.stdin)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(bytes, out)
}

// Ask a question on the output stream and read the answer from the input stream
func (c *Context) askQuestion(question, defaultAnswer string) string {
	fmt.Fprintf(c.stdout, question)
	return c.readAnswer(defaultAnswer)
}

// Print an invalid input message on the error stream
func (c *Context) invalidInput() {
	fmt.Fprintf(c.stderr, "Invalid input, try again.\n\n")
}

// Read the user's answer from the input stream, trimming newline and providing a default.
func (c *Context) readAnswer(defaultAnswer string) string {
	answer, _ := c.stdin.ReadString('\n')
	answer = strings.TrimSuffix(answer, "\n")
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = defaultAnswer
	}
	return answer
}
