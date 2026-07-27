package repl

import (
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
)

// Simple ANSI theme. Disabled automatically when stdout is not a TTY
// (color.NoColor=true) so non-interactive scripts get clean output.
type Theme struct {
	Info *color.Color // cyan  — [*]
	OK   *color.Color // green — [+]
	Warn *color.Color // yellow— [!]
	Err  *color.Color // red   — [x]
	Mute *color.Color // faint — secondary text
}

var theme = newTheme()

func newTheme() *Theme {
	if !isTTY(os.Stdout) || os.Getenv("NO_COLOR") != "" {
		color.NoColor = true
	}
	return &Theme{
		Info: color.New(color.FgCyan),
		OK:   color.New(color.FgGreen),
		Warn: color.New(color.FgYellow),
		Err:  color.New(color.FgRed),
		Mute: color.New(color.Faint),
	}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ASCII art banner shown at startup.
const bannerArt = `
[0;37;40m▄▄▄▄▄▄▄ ▄▄▄▄▄▄▄ ▄▄▄▄▄▄▄ ▄▄▄▄▄▄▄ ▄▄▄▄▄▄▄ ▄▄▄▄▄▄  ▄▄▄▄▄▄▄ ▄▄▄▄▄▄▄       ▄▄▄▄▄▄▄ ▄▄▄▄▄▄ [0m
[0;37;40m█ ▄▄▄▄█ █ ▄▄▄ █ █ ▄▄▄▄█ █ ▄▄▄ █ █ █▀ ▄█ █ ▄▄ █▄ █ ▄▄▄ █ █ ▄▄▄▄█ ▄▄▄▄▄ █ ▄▄▄▄█ █▄▄▄▄▀█[0m
[0;37;40m█▄▄▄▄ █ █ ▄▄▄ █ █▄▄▄▄ █ █ ▄▄▄ █ █ ▄ ▀█▄ █ ▄▄▄ █ █ ▄▄▄▄█ █ ▄▄█   █▄▄▄█ █ █▄▄▄▄ █▀▄▄▄██[0m
[0;37;40m█▄▄▄▄▄█ █▄█ █▄█ █▄▄▄▄▄█ █▄█ █▄█ █▄██▄▄█ █▄▄▄▄▄█ █▄█     █▄█           █▄▄▄▄▄█ █▄▄▄▄▄█[0m
`

// Banner prints the startup header in the [*] line format.
func Banner(channelID, targetID string) {
	fmt.Print(bannerArt)
	fmt.Println()
	fmt.Println("[*] Authtlor @mywannn")
	fmt.Println("[*] Version 1.0") 
	fmt.Println("[*] Welcome to the SasakBPF Backdoor console, please type 'help' for options")
	fmt.Println()
	fmt.Printf("[*] Channel: %s\n", channelID)
	if targetID != "" && targetID != "*" {
		fmt.Printf("[*] Target: %s\n", targetID)
	} else {
		fmt.Println("[*] Target: (none — use 'target <id>' to set)")
	}
	fmt.Println("[*] Type 'help' for available commands")
	fmt.Println("[*] Use -target <id> flag to connect to a specific agent directly")
	fmt.Println()
}

// Prompt returns the prompt string: "[channelID] targetID > ".
func Prompt(channelID, targetID string) string {
	if targetID == "" || targetID == "*" {
		return fmt.Sprintf("[%s] > ", channelID)
	}
	return fmt.Sprintf("[%s] %s > ", channelID, targetID)
}

// Log prints a categorized line like "[*] foo", "[+] ok", "[!] warn", "[x] err".
func Log(level, msg string) {
	var c *color.Color
	switch level {
	case "*":
		c = theme.Info
	case "+":
		c = theme.OK
	case "!":
		c = theme.Warn
	case "x":
		c = theme.Err
	default:
		c = theme.Info
	}
	fmt.Printf("%s %s\n", c.Sprint("["+level+"]"), msg)
}

// Truncate respects visible width (ASCII-only safe).
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// VisibleWidth counts rune width (best-effort, ASCII-only safe).
func VisibleWidth(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x1100 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// runningInTTY reports whether both stdin and stdout are character devices.
func runningInTTY() bool {
	return isTTY(os.Stdin) && isTTY(os.Stdout)
}

func _unused() { _ = strings.TrimSpace }
