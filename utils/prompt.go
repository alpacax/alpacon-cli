package utils

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

func PromptForPassword(promptText string) string {
	_, _ = fmt.Fprint(TrackedStderr(), promptText)

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback to standard hidden input
		bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return ""
		}
		_, _ = fmt.Fprintln(TrackedStderr())
		return strings.TrimSpace(string(bytePassword))
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	var password []byte
	buf := make([]byte, 1)
	for {
		_, err := os.Stdin.Read(buf)
		if err != nil {
			break
		}
		switch buf[0] {
		case '\n', '\r':
			_, _ = fmt.Fprint(TrackedStderr(), "\r\n")
			return strings.TrimSpace(string(password))
		case 127, '\b': // backspace
			if len(password) > 0 {
				password = password[:len(password)-1]
				_, _ = fmt.Fprint(TrackedStderr(), "\b \b")
			}
		case 3: // Ctrl+C
			_, _ = fmt.Fprint(TrackedStderr(), "\r\n")
			_ = term.Restore(int(os.Stdin.Fd()), oldState)
			os.Exit(1)
		default:
			if buf[0] >= 32 { // printable characters only
				password = append(password, buf[0])
				_, _ = fmt.Fprint(TrackedStderr(), "*")
			}
		}
	}

	_, _ = fmt.Fprintln(TrackedStderr())
	return strings.TrimSpace(string(password))
}

func PromptForInput(promptText string) string {
	input, err := promptForInput(os.Stdin, promptText)
	if err != nil {
		CliErrorWithExit("No input received.")
	}
	return input
}

func promptForInput(r io.Reader, promptText string) (string, error) {
	reader := bufio.NewReader(r)
	_, _ = fmt.Fprint(TrackedStderr(), promptText)
	line, err := reader.ReadString('\n')
	noteEchoedNewline(r, err)
	input := strings.TrimSpace(line)
	if err != nil && (input == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return input, nil
}

// noteEchoedNewline records the Enter a terminal echoes, which lands on stderr only when stderr is that terminal.
func noteEchoedNewline(r io.Reader, readErr error) {
	if f, ok := r.(*os.File); ok && readErr == nil && term.IsTerminal(int(f.Fd())) && term.IsTerminal(int(os.Stderr.Fd())) {
		stderrMidLine.Store(false)
	}
}

func PromptForRequiredInput(promptText string) string {
	for {
		input := PromptForInput(promptText)
		if input != "" {
			return input
		}
		CliWarning("This field is required. Please enter a value.")
	}
}

// PromptForInputWithDefault prompts for input and returns the default if empty.
func PromptForInputWithDefault(promptText, defaultValue string) string {
	input := PromptForInput(promptText)
	if input == "" {
		return defaultValue
	}
	return input
}

func PromptForRequiredIntInput(promptText string) int {
	for {
		inputStr := PromptForInput(promptText)
		inputInt, err := strconv.Atoi(inputStr)
		if err != nil {
			CliWarning("Only integers are allowed. Please try again.")
			continue
		}
		return inputInt
	}
}

func PromptForIntInput(promptText string, defaultValue int) int {
	inputStr := PromptForInput(promptText)
	inputStr = strings.TrimSpace(inputStr)
	if inputStr == "" {
		return defaultValue
	}
	inputInt, err := strconv.Atoi(inputStr)
	if err != nil {
		CliWarning("Invalid input. Using default value: %d", defaultValue)
		return defaultValue
	}
	return inputInt
}

func PromptForListInput(promptText string) []string {
	return SplitAndTrim(PromptForInput(promptText), ",")
}

// ConfirmAction prompts the user for confirmation before a destructive action.
// In non-interactive mode (piped stdin, CI, etc.), it exits and asks for --yes flag.
// Returns on confirm; exits the program on decline.
func ConfirmAction(msg string, args ...any) {
	if !IsInteractiveShell() {
		CliErrorWithExit("This operation requires confirmation. Use --yes (-y) to skip the prompt in non-interactive mode.")
	}
	message := fmt.Sprintf(msg, args...)
	if !PromptForBool(message) {
		CliInfoWithExit("Operation cancelled.")
	}
}

func PromptForBool(prompt string) bool {
	return promptForBool(os.Stdin, prompt)
}

func promptForBool(r io.Reader, prompt string) bool {
	reader := bufio.NewReader(r)

	for {
		_, _ = fmt.Fprintf(TrackedStderr(), "%s [y/n]: ", prompt)
		line, err := reader.ReadString('\n')
		noteEchoedNewline(r, err)
		input := strings.TrimSpace(strings.ToLower(line))

		switch input {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		if err != nil {
			_, _ = fmt.Fprintln(TrackedStderr())
			return false
		}
		CliWarning("Invalid input. Please enter 'y' (yes) or 'n' (no).")
	}
}
