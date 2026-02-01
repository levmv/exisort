package main

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// ANSI Colors
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorCyan   = "\033[36m"
	ColorGray   = "\033[90m"
)

type Logger struct {
	mu           sync.Mutex
	out          io.Writer
	lastIsStatus bool
}

var log *Logger

func InitLogger() {
	log = &Logger{out: os.Stderr}
}

// Transfer logs a file copy/move operation.
// It automatically detects Move vs Copy and Dry-Run from global config.
func (l *Logger) Transfer(src, dst string) {
	label := "COPY"
	color := ColorGreen

	if cfg.Move {
		label = "MOVE"
	}

	if cfg.DryRun {
		label = "DRY-" + label
		color = ColorGray
	}

	l.print(color, label, "%s -> %s", src, dst)
}

// Duplicate logs a duplicate file encounter.
// It automatically detects if we are Deleting (Move mode) or Skipping (Copy mode).
func (l *Logger) Duplicate(path string) {
	// If copying, it's just a Skip.
	// Skips are usually noisy, so check Verbose.
	if !cfg.Move {
		if !cfg.Verbose {
			return
		}
		label := "SKIP"
		color := ColorCyan
		if cfg.DryRun {
			label = "DRY-SKIP"
			color = ColorGray
		}
		l.print(color, label, "%s (Duplicate)", path)
		return
	}

	// If moving, it's a Delete (of the source).
	label := "DEL "
	color := ColorRed
	msg := "Duplicate source"

	if cfg.DryRun {
		label = "DRY-DEL"
		color = ColorGray
	}

	l.print(color, label, "%s (%s)", path, msg)
}

// Info logs general information (Verbose only)
func (l *Logger) Info(format string, a ...any) {
	if !cfg.Verbose {
		return
	}
	l.print(ColorBlue, "INFO", format, a...)
}

// Error logs critical errors
func (l *Logger) Error(format string, a ...any) {
	l.print(ColorRed, "ERR", format, a...)
}

func (l *Logger) Warn(format string, a ...any) {
	l.print(ColorYellow, "WARN", format, a...)
}

// Status prints a temporary line
func (l *Logger) Status(format string, a ...any) {
	if cfg.Verbose {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.clearStatus()

	fmt.Fprintf(l.out, format, a...)
	l.lastIsStatus = true
}

func (l *Logger) ClearStatus() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.clearStatus()
}

func (l *Logger) print(color, label, format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.clearStatus()

	msg := fmt.Sprintf(format, a...)
	fmt.Fprintf(l.out, "%s[%s]%s %s\n", color, label, ColorReset, msg)
}

func (l *Logger) clearStatus() {
	if l.lastIsStatus {
		fmt.Fprint(l.out, "\r\033[K")
		l.lastIsStatus = false
	}
}
