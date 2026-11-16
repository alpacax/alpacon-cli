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

// stdin is the one buffered reader every prompt on os.Stdin shares, since a pipe hands over many
// lines per read and a dropped reader loses the rest. A swapped os.Stdin gets a fresh one.
var stdin struct {
	file   *os.File
	reader *bufio.Reader
}

// PromptForPassword reads a password after promptText; Ctrl+C returns a silent exit-1 failure.
func PromptForPassword(promptText string) (string, error) {
	_, _ = fmt.Fprint(TrackedStderr(), promptText)

	if !term.IsTerminal(int(os.Stdin.Fd())) { // nothing to hide, and ReadPassword refuses a pipe
		line, _ := lineReader(os.Stdin).ReadString('\n')
		return strings.TrimSpace(line), nil
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return "", nil
		}
		_, _ = fmt.Fprintln(TrackedStderr())
		return strings.TrimSpace(string(bytePassword)), nil
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()
	return readRawPassword(os.Stdin, TrackedStderr())
}

// readRawPassword reads a password typed in raw mode from r, echoing a mask to w.
func readRawPassword(r io.Reader, w io.Writer) (string, error) {
	var password []byte
	buf := make([]byte, 1)
	for {
		_, err := r.Read(buf)
		if err != nil {
			break
		}
		switch buf[0] {
		case '\n', '\r':
			_, _ = fmt.Fprint(w, "\r\n")
			return strings.TrimSpace(string(password)), nil
		case 127, '\b': // backspace
			if len(password) > 0 {
				password = password[:len(password)-1]
				_, _ = fmt.Fprint(w, "\b \b")
			}
		case 3: // Ctrl+C
			_, _ = fmt.Fprint(w, "\r\n")
			return "", NewSilentExitError(ExitCodeGeneralError)
		default:
			if buf[0] >= 32 {
				password = append(password, buf[0])
				_, _ = fmt.Fprint(w, "*")
			}
		}
	}

	_, _ = fmt.Fprintln(w)
	return strings.TrimSpace(string(password)), nil
}

// ReadInput reads one line from stdin after promptText, or returns the exit-1 failure on no input.
func ReadInput(promptText string) (string, error) {
	input, err := promptForInput(os.Stdin, promptText)
	if err != nil {
		return "", noInputError()
	}
	return input, nil
}

func noInputError() *ExitError {
	return newExitError(ExitCodeGeneralError, true, "No input received.", nil)
}

func promptForInput(r io.Reader, promptText string) (string, error) {
	reader := lineReader(r)
	_, _ = fmt.Fprint(TrackedStderr(), promptText)
	line, err := reader.ReadString('\n')
	noteEchoedNewline(r, err)
	input := strings.TrimSpace(line)
	if err != nil && (input == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return input, nil
}

func lineReader(r io.Reader) *bufio.Reader {
	if r != os.Stdin {
		return bufio.NewReader(r)
	}
	if stdin.file != os.Stdin {
		stdin.file, stdin.reader = os.Stdin, bufio.NewReader(os.Stdin)
	}
	return stdin.reader
}

// noteEchoedNewline records the Enter a terminal echoes, which lands on stderr only when stderr is that terminal.
func noteEchoedNewline(r io.Reader, readErr error) {
	if f, ok := r.(*os.File); ok && readErr == nil && term.IsTerminal(int(f.Fd())) && term.IsTerminal(int(os.Stderr.Fd())) {
		stderrMidLine.Store(false)
	}
}

// ReadRequiredInput is ReadInput that asks again on an empty line.
func ReadRequiredInput(promptText string) (string, error) {
	return readRequiredInput(os.Stdin, promptText)
}

func readRequiredInput(r io.Reader, promptText string) (string, error) {
	for {
		input, err := promptForInput(r, promptText)
		if err != nil {
			return "", noInputError()
		}
		if input != "" {
			return input, nil
		}
		CliWarning("This field is required. Please enter a value.")
	}
}

func ReadInputWithDefault(promptText, defaultValue string) (string, error) {
	input, err := ReadInput(promptText)
	if err != nil {
		return "", err
	}
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}

func ReadRequiredIntInput(promptText string) (int, error) {
	for {
		inputStr, err := ReadInput(promptText)
		if err != nil {
			return 0, err
		}
		inputInt, err := strconv.Atoi(inputStr)
		if err != nil {
			CliWarning("Only integers are allowed. Please try again.")
			continue
		}
		return inputInt, nil
	}
}

func ReadIntInput(promptText string, defaultValue int) (int, error) {
	inputStr, err := ReadInputWithDefault(promptText, strconv.Itoa(defaultValue))
	if err != nil {
		return 0, err
	}
	inputInt, err := strconv.Atoi(inputStr)
	if err != nil {
		CliWarning("Invalid input. Using default value: %d", defaultValue)
		return defaultValue, nil
	}
	return inputInt, nil
}

// Confirm asks msg as a yes/no question before a destructive action. A decline prints the cancel
// notice and returns false, which the caller ends on with exit 0; a non-interactive run returns the exit-2 failure.
func Confirm(msg string, args ...any) (bool, error) {
	return confirm(IsInteractiveShell(), os.Stdin, msg, args...)
}

func confirm(interactive bool, r io.Reader, msg string, args ...any) (bool, error) {
	if !interactive {
		return false, confirmationRequiredError()
	}
	if !promptForBool(r, confirmPrompt(msg, args...)) {
		CliInfo("Operation cancelled.")
		return false, nil
	}
	return true, nil
}

func confirmationRequiredError() *ExitError {
	return &ExitError{Code: ExitCodeUsageError, errorCode: ErrorCodeConfirmationRequired,
		msg: "This operation requires confirmation. Use --yes (-y) to skip the prompt in non-interactive mode."}
}

// confirmPrompt formats a confirmation prompt without control characters, so a
// server's value such as an email cannot repaint what the answer acts on.
func confirmPrompt(msg string, args ...any) string {
	prompt, _ := SanitizeTerminalLine(fmt.Sprintf(msg, args...))
	return prompt
}

func PromptForBool(prompt string) bool {
	return promptForBool(os.Stdin, prompt)
}

func promptForBool(r io.Reader, prompt string) bool {
	reader := lineReader(r)

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

// PrintDeviceCodePrompt prints the device code prompt and returns the URL it printed, the one the
// browser must open, so the operator lands on the address they read. Both values come from the
// authorization server: each is sanitized as a single line, since a newline would let the server
// write its own line into the prompt. Sanitizing runs before Blue and Bold, since sanitizing
// afterward would strip their escape sequences.
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
