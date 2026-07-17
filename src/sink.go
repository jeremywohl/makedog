// Output sink: the seam through which child output and lifecycle reporting flow.
// Everything that belongs in a run's log goes through the active sink, which
// renders to the terminal and tees into the current run's log file when one is
// open. Interactive chrome (menus, key help) is terminal-only and does not
// pass through here.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
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

// out is the active sink.
var out = &teeSink{}

var _ sink = (*teeSink)(nil)

// runLog appends JSONL records to one run's log file, one flushed write per
// record so followers see lines as they land.
type runLog struct {
	mu sync.Mutex
	f  *os.File
}

func (l *runLog) write(rec record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.f.Write(append(b, '\n'))
}

func (l *runLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

// teeSink renders to the terminal and records to the attached run log.
// Attachment swaps per run; a nil log means terminal-only.
type teeSink struct {
	term terminalSink
	mu   sync.Mutex
	log  *runLog
}

func (s *teeSink) attach(l *runLog) {
	s.mu.Lock()
	s.log = l
	s.mu.Unlock()
}

func (s *teeSink) detach() { s.attach(nil) }

func (s *teeSink) record(rec record) {
	s.mu.Lock()
	l := s.log
	s.mu.Unlock()
	if l != nil {
		l.write(rec)
	}
}

func (s *teeSink) ChildLine(t time.Time, line string) {
	s.term.ChildLine(t, line)
	s.record(record{T: recLine, TS: t, S: line})
}

func (s *teeSink) Command(format string, args ...any) {
	s.term.Command(format, args...)
	s.record(record{T: recCommand, TS: time.Now(), S: fmt.Sprintf(format, args...)})
}

func (s *teeSink) Event(format string, args ...any) {
	s.term.Event(format, args...)
	s.record(record{T: recEvent, TS: time.Now(), S: fmt.Sprintf(format, args...)})
}

func (s *teeSink) EventAt(t time.Time, format string, args ...any) {
	s.term.EventAt(t, format, args...)
	s.record(record{T: recEvent, TS: t, S: fmt.Sprintf(format, args...)})
}

func (s *teeSink) Error(format string, args ...any) {
	s.term.Error(format, args...)
	s.record(record{T: recError, TS: time.Now(), S: fmt.Sprintf(format, args...)})
}

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
