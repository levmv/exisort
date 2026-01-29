package main

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// ANSI Color Codes
const (
	Reset  = "\033[0m"
	Red    = "\033[31m"
	Green  = "\033[32m"
	Yellow = "\033[33m"
	Blue   = "\033[34m"
	Cyan   = "\033[36m"
	Gray   = "\033[90m"
)

type Logger struct {
	mu      sync.Mutex
	out     io.Writer
	Verbose bool
}

var log *Logger

func InitLogger(verbose bool) {
	log = &Logger{
		out:     os.Stderr,
		Verbose: verbose,
	}
}

// internal helper to print safely
func (l *Logger) print(color, tag, format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// format the user message
	msg := fmt.Sprintf(format, a...)

	// print the final line: [TAG] Message
	fmt.Fprintf(l.out, "%s[%s]%s %s\n", color, tag, Reset, msg)
}

func (l *Logger) Error(format string, a ...any) {
	l.print(Red, "ERR ", format, a...)
}

func (l *Logger) Warn(format string, a ...any) {
	l.print(Yellow, "WARN", format, a...)
}

func (l *Logger) Info(format string, a ...any) {
	if !l.Verbose {
		return
	}
	l.print(Blue, "INFO", format, a...)
}

// Action allows custom tags (COPY, MOVE, SKIP)
func (l *Logger) Action(tag, format string, a ...any) {
	if !l.Verbose {
		return
	}
	color := Green
	if tag == "SKIP" || tag == "TRASH" {
		color = Cyan
	} else if tag == "DRY" {
		color = Gray // Let's use Gray for dry-run to distinguish it
	}

	l.print(color, tag, format, a...)
}
