package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"text/tabwriter"
	"time"
)

type Statistics struct {
	FilesScanned   atomic.Int64
	FilesProcessed atomic.Int64 // Copied or Moved
	Duplicates     atomic.Int64 // Skipped/Trashed
	Errors         atomic.Int64
	BytesMoved     atomic.Int64
	StartTime      time.Time
}

var stats *Statistics

func InitStats() {
	stats = &Statistics{
		StartTime: time.Now(),
	}
}

func (s *Statistics) IncScanned() {
	s.FilesScanned.Add(1)
}

func (s *Statistics) IncProcessed() {
	s.FilesProcessed.Add(1)
}

func (s *Statistics) IncDuplicate() {
	s.Duplicates.Add(1)
}

func (s *Statistics) IncError() {
	s.Errors.Add(1)
}

func (s *Statistics) AddBytes(n int64) {
	s.BytesMoved.Add(n)
}

// PrintSummary outputs the final table
func (s *Statistics) PrintSummary() {
	//if s.FilesScanned.Load() == 0 {
	//	return
	//}

	duration := time.Since(s.StartTime)

	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)

	fmt.Fprintln(os.Stderr, "----------------------------------------")

	fmt.Fprintf(w, "Total Scanned:\t%d\n", s.FilesScanned.Load())

	if s.FilesProcessed.Load() > 0 {
		fmt.Fprintf(w, "Imported/Moved:\t%d\n", s.FilesProcessed.Load())
		fmt.Fprintf(w, "Data Volume:\t%s\n", formatBytes(s.BytesMoved.Load()))
	}

	if s.Duplicates.Load() > 0 {
		fmt.Fprintf(w, "Duplicates:\t%d\n", s.Duplicates.Load())
	}

	if s.Errors.Load() > 0 {
		fmt.Fprintf(w, "Errors:\t%d\n", s.Errors.Load())
	}

	fmt.Fprintf(w, "Duration:\t%s\n", duration.Round(time.Millisecond))

	w.Flush()
	fmt.Fprintln(os.Stderr, "----------------------------------------")
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
