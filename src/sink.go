// Output sink: the seam through which child output and lifecycle reporting flow.
// Everything that belongs in a run's log goes through the active sink, so it can
// render to the terminal today and also persist to run logs later. Interactive
// chrome (menus, key help) is terminal-only and does not pass through here.
package main

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// sink receives a run's loggable output and events.
type sink interface {
	ChildLine(t time.Time, line string) // one line of child output
	Command(format string, args ...any) // lifecycle commands: start, stop, make
	Event(format string, args ...any)   // notable events and state changes
	EventAt(t time.Time, format string, args ...any)
	Error(format string, args ...any)
}

// out is the active sink. A future log store wraps or fans out from here.
var out sink = terminalSink{}

// terminalSink renders to the raw-mode terminal, with styling.
type terminalSink struct{}

var (
	commandStyle = lipgloss.NewStyle().Underline(true)
	eventStyle   = lipgloss.NewStyle().Bold(true)
	errorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("red"))
)

func (terminalSink) ChildLine(t time.Time, line string) {
	printf("%s  %s\n", t.Format("[2006-01-02 15:04:05.000]"), line)
}

func (terminalSink) Command(format string, args ...any) {
	printf("--> %s\n", commandStyle.Render(fmt.Sprintf(format, args...)))
}

func (terminalSink) Event(format string, args ...any) {
	printf("* %s\n", eventStyle.Render(fmt.Sprintf(format, args...)))
}

func (terminalSink) EventAt(t time.Time, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	printf("%s at %s\n", eventStyle.Render("* "+text), t.Format("15:04:05.000"))
}

func (terminalSink) Error(format string, args ...any) {
	printf("! %s\n", errorStyle.Render(fmt.Sprintf(format, args...)))
}
