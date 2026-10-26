package utils

import "strings"

// PreScanOutputFormat returns OutputFormatJSON when the last --output before a bare "--" is json, else "".
func PreScanOutputFormat(args []string) string {
	value := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			i = len(args)
		case arg == "--output":
			if i+1 < len(args) {
				value = args[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--output="):
			value = strings.TrimPrefix(arg, "--output=")
		}
	}
	if value == OutputFormatJSON {
		return OutputFormatJSON
	}
	return ""
}
