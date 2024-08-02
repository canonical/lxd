package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/logger"
)

// Asker holds a reader for reading input into CLI questions.
type Asker struct {
	reader *bufio.Reader
	logger logger.Logger
}

// NewAsker creates a new Asker instance that reads from the given reader.
// It can also be configured with a logger to help during the debug process.
func NewAsker(reader *bufio.Reader, logger logger.Logger) Asker {
	return Asker{reader: reader, logger: logger}
}

// AskBool asks a question and expect a yes/no answer.
func (a *Asker) AskBool(question string, defaultAnswer string) (bool, error) {
	for {
		answer, err := a.askQuestion(question, defaultAnswer)
		if err != nil {
			if a.logger != nil {
				a.logger.Error("Failed to read answer for question", logger.Ctx{"answer": answer, "question": question, "err": err})
			}

			return false, err
		}

		if shared.ValueInSlice(strings.ToLower(answer), []string{"yes", "y"}) {
			return true, nil
		} else if shared.ValueInSlice(strings.ToLower(answer), []string{"no", "n"}) {
			return false, nil
		}

		a.invalidInput(question, answer)
	}
}

// AskChoice asks the user to select one of multiple options.
func (a *Asker) AskChoice(question string, choices []string, defaultAnswer string) (string, error) {
	for {
		answer, err := a.askQuestion(question, defaultAnswer)
		if err != nil {
			if a.logger != nil {
				a.logger.Error("Failed to read answer for question", logger.Ctx{"answer": answer, "question": question, "err": err})
			}

			return "", err
		}

		if shared.ValueInSlice(answer, choices) {
			return answer, nil
		} else if a.logger != nil {
			a.logger.Error("Answer not among the available choices", logger.Ctx{"answer": answer, "choices": choices})
		}

		a.invalidInput(question, answer)
	}
}

// AskInt asks the user to enter an integer between a min and max value.
func (a *Asker) AskInt(question string, min int64, max int64, defaultAnswer string, validate func(int64) error) (int64, error) {
	for {
		answer, err := a.askQuestion(question, defaultAnswer)
		if err != nil {
			if a.logger != nil {
				a.logger.Error("Failed to read answer for question", logger.Ctx{"answer": answer, "question": question, "err": err})
			}

			return -1, err
		}

		result, err := strconv.ParseInt(answer, 10, 64)
		if err != nil {
			if a.logger != nil {
				a.logger.Error("Invalid input for the question", logger.Ctx{"answer": answer, "question": question, "err": err})
			}

			fmt.Fprintf(os.Stderr, "Invalid input: %v\n\n", err)
			continue
		}

		if !((min == -1 || result >= min) && (max == -1 || result <= max)) {
			if a.logger != nil {
				a.logger.Error("Invalid input (out of range) for the question", logger.Ctx{"answer": answer, "question": question})
			}

			fmt.Fprintf(os.Stderr, "Invalid input: out of range\n\n")
			continue
		}

		if validate != nil {
			err = validate(result)
			if err != nil {
				if a.logger != nil {
					a.logger.Error("Invalid input for the question", logger.Ctx{"answer": answer, "question": question, "err": err})
				}

				fmt.Fprintf(os.Stderr, "Invalid input: %v\n\n", err)
				continue
			}
		}

		return result, err
	}
}

// AskString asks the user to enter a string, which optionally
// conforms to a validation function.
func (a *Asker) AskString(question string, defaultAnswer string, validate func(string) error) (string, error) {
	for {
		answer, err := a.askQuestion(question, defaultAnswer)
		if err != nil {
			if a.logger != nil {
				a.logger.Error("Failed to read answer for question", logger.Ctx{"answer": answer, "question": question, "err": err})
			}

			return "", err
		}

		if validate != nil {
			err = validate(answer)
			if err != nil {
				if a.logger != nil {
					a.logger.Error("Invalid input for the question", logger.Ctx{"answer": answer, "question": question, "err": err})
				}

				fmt.Fprintf(os.Stderr, "Invalid input: %v\n\n", err)
				continue
			}

			return answer, err
		}

		if len(answer) != 0 {
			return answer, err
		}

		a.invalidInput(question, answer)
	}
}

// AskPassword asks the user to enter a password.
func (a *Asker) AskPassword(question string) string {
	for {
		fmt.Print(question)

		pwd, _ := term.ReadPassword(0)
		fmt.Println("")
		inFirst := string(pwd)
		inFirst = strings.TrimSuffix(inFirst, "\n")

		fmt.Print("Again: ")
		pwd, _ = term.ReadPassword(0)
		fmt.Println("")
		inSecond := string(pwd)
		inSecond = strings.TrimSuffix(inSecond, "\n")

		// refuse empty password or if password inputs do not match
		if len(inFirst) > 0 && inFirst == inSecond {
			return inFirst
		}

		a.invalidInput(question, "*****")
	}
}

// AskPasswordOnce asks the user to enter a password.
//
// It's the same as AskPassword, but it won't ask to enter it again.
func (a *Asker) AskPasswordOnce(question string) string {
	for {
		fmt.Print(question)
		pwd, _ := term.ReadPassword(0)
		fmt.Println("")

		// refuse empty password
		spwd := string(pwd)
		if len(spwd) > 0 {
			return spwd
		}

		a.invalidInput(question, "*****")
	}
}

// Ask a question on the output stream and read the answer from the input stream.
func (a *Asker) askQuestion(question, defaultAnswer string) (string, error) {
	fmt.Print(question)

	return a.readAnswer(defaultAnswer)
}

// Read the user's answer from the input stream, trimming newline and providing a default.
func (a *Asker) readAnswer(defaultAnswer string) (string, error) {
	answer, err := a.reader.ReadString('\n')
	answer = strings.TrimSpace(strings.TrimSuffix(answer, "\n"))
	if answer == "" {
		answer = defaultAnswer
	}

	return answer, err
}

// Print an invalid input message on the error stream.
func (a *Asker) invalidInput(question string, answer string) {
	if a.logger != nil {
		a.logger.Error("Invalid input for the question", logger.Ctx{"answer": answer, "question": question})
	}

	fmt.Fprintf(os.Stderr, "Invalid input, try again.\n\n")
}
