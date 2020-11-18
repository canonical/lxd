package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh/terminal"

	"github.com/grant-he/lxd/shared"
)

var stdin = bufio.NewReader(os.Stdin)

// AskBool asks a question and expect a yes/no answer.
func AskBool(question string, defaultAnswer string) bool {
	for {
		answer := askQuestion(question, defaultAnswer)

		if shared.StringInSlice(strings.ToLower(answer), []string{"yes", "y"}) {
			return true
		} else if shared.StringInSlice(strings.ToLower(answer), []string{"no", "n"}) {
			return false
		}

		invalidInput()
	}
}

// AskChoice asks the user to select one of multiple options
func AskChoice(question string, choices []string, defaultAnswer string) string {
	for {
		answer := askQuestion(question, defaultAnswer)

		if shared.StringInSlice(answer, choices) {
			return answer
		}

		invalidInput()
	}
}

// AskInt asks the user to enter an integer between a min and max value
func AskInt(question string, min int64, max int64, defaultAnswer string, validate func(int64) error) int64 {
	for {
		answer := askQuestion(question, defaultAnswer)

		result, err := strconv.ParseInt(answer, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid input: %v\n\n", err)
			continue
		}

		if !((min == -1 || result >= min) && (max == -1 || result <= max)) {
			fmt.Fprintf(os.Stderr, "Invalid input: out of range\n\n")
			continue
		}

		if validate != nil {
			err = validate(result)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid input: %v\n\n", err)
				continue
			}
		}

		return result
	}
}

// AskString asks the user to enter a string, which optionally
// conforms to a validation function.
func AskString(question string, defaultAnswer string, validate func(string) error) string {
	for {
		answer := askQuestion(question, defaultAnswer)

		if validate != nil {
			error := validate(answer)
			if error != nil {
				fmt.Fprintf(os.Stderr, "Invalid input: %s\n\n", error)
				continue
			}

			return answer
		}

		if len(answer) != 0 {
			return answer
		}

		invalidInput()
	}
}

// AskPassword asks the user to enter a password.
func AskPassword(question string) string {
	for {
		fmt.Printf(question)

		pwd, _ := terminal.ReadPassword(0)
		fmt.Println("")
		inFirst := string(pwd)
		inFirst = strings.TrimSuffix(inFirst, "\n")

		fmt.Printf("Again: ")
		pwd, _ = terminal.ReadPassword(0)
		fmt.Println("")
		inSecond := string(pwd)
		inSecond = strings.TrimSuffix(inSecond, "\n")

		// refuse empty password or if password inputs do not match
		if len(inFirst) > 0 && inFirst == inSecond {
			return inFirst
		}

		invalidInput()
	}
}

// AskPasswordOnce asks the user to enter a password.
//
// It's the same as AskPassword, but it won't ask to enter it again.
func AskPasswordOnce(question string) string {
	for {
		fmt.Printf(question)
		pwd, _ := terminal.ReadPassword(0)
		fmt.Println("")

		// refuse empty password
		spwd := string(pwd)
		if len(spwd) > 0 {
			return spwd
		}

		invalidInput()
	}
}

// Ask a question on the output stream and read the answer from the input stream
func askQuestion(question, defaultAnswer string) string {
	fmt.Printf(question)

	return readAnswer(defaultAnswer)
}

// Read the user's answer from the input stream, trimming newline and providing a default.
func readAnswer(defaultAnswer string) string {
	answer, _ := stdin.ReadString('\n')
	answer = strings.TrimSuffix(answer, "\n")
	answer = strings.TrimSpace(answer)
	if answer == "" {
		answer = defaultAnswer
	}

	return answer
}

// Print an invalid input message on the error stream
func invalidInput() {
	fmt.Fprintf(os.Stderr, "Invalid input, try again.\n\n")
}
