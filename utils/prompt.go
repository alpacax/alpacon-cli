package utils

import (
	"bufio"
	"fmt"
	"golang.org/x/term"
	"os"
	"strconv"
	"strings"
)

func PromptForPassword(promptText string) string {
	fmt.Print(promptText)
	bytePassword, err := term.ReadPassword(0)
	if err != nil {
		return ""
	}
	fmt.Println()
	return strings.TrimSpace(string(bytePassword))
}

func PromptForInput(promptText string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(promptText)
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
		fmt.Println("This field is required. Please enter a value.")
	}
}

func PromptForRequiredIntInput(promptText string) int {
	inputStr := PromptForInput(promptText)
	inputInt, err := strconv.Atoi(inputStr)
	if err != nil {
		fmt.Println("Only integers are allowed. Please try again.")
		return PromptForRequiredIntInput(promptText)
	}
	return inputInt
}

func PromptForIntInput(promptText string, defaultValue int) int {
	inputStr := PromptForInput(promptText)
	inputStr = strings.TrimSpace(inputStr)
	if inputStr == "" {
		return defaultValue
	}
	inputInt, err := strconv.Atoi(inputStr)
	if err != nil {
		fmt.Printf("Invalid input. Using default value: %d\n", defaultValue)
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
		fmt.Println("This field is required. Please enter a value.")
	}
}

func PromptForBool(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("%s [y/n]: ", prompt)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(strings.ToLower(input))

		switch input {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		default:
			fmt.Println("Invalid input. Please enter 'y' (yes) or 'n' (no).")
		}
	}
}
