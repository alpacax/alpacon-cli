package exec

// inlineCommandLimit matches alpacon-server Command.line max_length=2048.
const inlineCommandLimit = 2048

// windowsPlatform is the platform string the server reports for Windows hosts.
const windowsPlatform = "windows"

// exceedsInlineLimit reports whether the command must be sent with the oversized
// flag so the server stages it as a temp script instead of running it inline.
// Byte-based: may flag before the server's char limit, never after.
func exceedsInlineLimit(command string) bool {
	return len(command) > inlineCommandLimit
}
