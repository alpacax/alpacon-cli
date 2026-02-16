package utils

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

func PromptForPassword(promptText string) string {
	fmt.Fprint(os.Stderr, promptText)
	bytePassword, err := term.ReadPassword(0)
	if err != nil {
		return ""
	}
	fmt.Fprintln(os.Stderr)
	return strings.TrimSpace(string(bytePassword))
}

func PromptForInput(promptText string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprint(os.Stderr, promptText)
	input, err := reader.ReadString('\n')
	if err != nil {
		CliErrorWithExit("Invalid input. Please try again.")
	}
	return strings.TrimSpace(input)
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
	inputStr := PromptForInput(promptText)
	inputList := strings.Split(inputStr, ",")
	for i, item := range inputList {
		inputList[i] = strings.TrimSpace(item)
	}
	if len(inputList) == 1 && inputList[0] == "" {
		return []string{}
	}

	return inputList
}

func PromptForRequiredListInput(promptText string) []string {
	for {
		inputList := PromptForListInput(promptText)
		if len(inputList) > 0 && inputList[0] != "" {
			return inputList
		}
		CliWarning("This field is required. Please enter a value.")
	}
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
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Fprintf(os.Stderr, "%s [y/n]: ", prompt)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))

		switch input {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			CliWarning("Invalid input. Please enter 'y' (yes) or 'n' (no).")
		}
	}
}
