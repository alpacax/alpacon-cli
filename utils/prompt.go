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
	fmt.Fprint(os.Stderr, promptText)

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		// Fallback to standard hidden input
		bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return ""
		}
		fmt.Fprintln(os.Stderr)
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
			fmt.Fprint(os.Stderr, "\r\n")
			return strings.TrimSpace(string(password))
		case 127, '\b': // backspace
			if len(password) > 0 {
				password = password[:len(password)-1]
				fmt.Fprint(os.Stderr, "\b \b")
			}
		case 3: // Ctrl+C
			fmt.Fprint(os.Stderr, "\r\n")
			_ = term.Restore(int(os.Stdin.Fd()), oldState)
			os.Exit(1)
		default:
			if buf[0] >= 32 { // printable characters only
				password = append(password, buf[0])
				fmt.Fprint(os.Stderr, "*")
			}
		}
	}

	fmt.Fprintln(os.Stderr)
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
	fmt.Fprint(os.Stderr, promptText)
	line, err := reader.ReadString('\n')
	input := strings.TrimSpace(line)
	if err != nil && (input == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return input, nil
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
	if !PromptForBool(confirmPrompt(msg, args...)) {
		CliInfoWithExit("Operation cancelled.")
	}
}

// confirmPrompt formats a confirmation prompt with control characters removed,
// as the Cli* helpers do: an argument can be a server's value, such as an
// email, whose escape sequences or bidi marks could make the prompt name a
// different target from the one the answer acts on.
func confirmPrompt(msg string, args ...any) string {
	prompt, _ := SanitizeTerminalLine(fmt.Sprintf(msg, args...))
	return prompt
}

func PromptForBool(prompt string) bool {
	return promptForBool(os.Stdin, prompt)
}

func promptForBool(r io.Reader, prompt string) bool {
	reader := bufio.NewReader(r)

	for {
		fmt.Fprintf(os.Stderr, "%s [y/n]: ", prompt)
		line, err := reader.ReadString('\n')
		input := strings.TrimSpace(strings.ToLower(line))

		switch input {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		if err != nil {
			fmt.Fprintln(os.Stderr)
			return false
		}
		CliWarning("Invalid input. Please enter 'y' (yes) or 'n' (no).")
	}
}

// PrintDeviceCodePrompt sanitizes the URL and the code before Blue and Bold go
// on—the reverse order would strip the highlight along with the escapes. Both
// come from the authorization server, and a person reads them off the screen.
//
// The line strip and not the block one: a URL and a device code are single
// values, so a newline in either is the server writing its own line into a
// prompt the CLI is supposed to own.
//
// It hands back the URL it printed, because that is the one the browser has to
// be opened on. Opening the raw value would take the operator somewhere other
// than the address they just read off the screen, which is the whole thing this
// is here to prevent.
func PrintDeviceCodePrompt(w io.Writer, verificationURI, userCode string) (printedURI string) {
	uri, uriAltered := SanitizeTerminalLine(verificationURI)
	code, codeAltered := SanitizeTerminalLine(userCode)
	if uriAltered || codeAltered {
		WarnTerminalTextAltered(w, "")
	}
	_, _ = fmt.Fprintf(w, "\nPlease authenticate by visiting:\n%s\n\n", Blue(uri))
	_, _ = fmt.Fprintf(w, "Verification code: %s\n\n", Bold(code))
	return uri
}
