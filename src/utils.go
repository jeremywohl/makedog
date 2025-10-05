// Utilities
package main

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

var oldTermState *term.State

// Set stdin to raw mode.
func setRawTerm() error {
	var err error
	oldTermState, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}

	return nil
}

// restoreTerm restores the terminal to its original state.
func restoreTerm() {
	if oldTermState != nil {
		term.Restore(int(os.Stdin.Fd()), oldTermState)
	}
}

// banner prints text centered with a fill character.
func banner(text string, fill rune, tight bool) {
	cols := 80
	if width, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		cols = width
	}

	padded := text
	if !tight {
		padded = " " + text + " "
	}

	padding := cols - len(padded)
	if padding < 0 {
		padding = 0
	}

	leftPad := padding / 2
	rightPad := padding - leftPad

	printf("%s%s%s\n", strings.Repeat(string(fill), leftPad), padded, strings.Repeat(string(fill), rightPad))
}

// printf prints formatted output with proper line endings for raw terminal mode.
// In raw mode, the terminal doesn't automatically convert \n to \r\n, so we must
// use \r\n explicitly for carriage return + line feed.
func printf(format string, args ...interface{}) {
	format = strings.ReplaceAll(format, "\n", "\r\n")
	output := fmt.Sprintf(format, args...)
	fmt.Print(output)
}

// println prints a blank line for raw terminal mode.
func println() {
	fmt.Print("\r\n")
}
