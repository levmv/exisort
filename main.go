package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	// Flags
	Verbose   bool
	DryRun    bool
	Move      bool
	DeepCheck bool
	Conflict  string
	Format    string

	Extensions   map[string]bool
	MinSizeBytes int64
}

var cfg Config

var Version = "dev"

// FileJob contains the "Fingerprint" of the source file
type FileJob struct {
	Path       string
	Info       fs.FileInfo
	Date       time.Time
	SourceHead []byte // First 64KB
	Hash       uint64
}

const defaultExtensions = "jpg,jpeg,png,heic,heif,mov,mp4,m4v,avi,arw,cr2,cr3,dng,nef"

func main() {
	var rawExts string
	var rawSizeKB int64

	flag.BoolVar(&cfg.Verbose, "v", false, "Verbose logging")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Simulate operations without changes")
	flag.BoolVar(&cfg.Move, "move", false, "Move files instead of copying")
	flag.BoolVar(&cfg.DeepCheck, "deep", false, "Verify content hash before skipping duplicates")

	flag.StringVar(&cfg.Conflict, "conflict", "rename", "Collision resolution: rename, skip, overwrite")
	flag.StringVar(&cfg.Format, "format", "{year}/{year}-{month}/{year}{month}{day}_{hour}{min}{sec}.{ext}", "Naming format")

	flag.StringVar(&rawExts, "extensions", defaultExtensions, "Comma-separated list of extensions to process")
	flag.Int64Var(&rawSizeKB, "min-size", 32, "Minimum file size in KB to process")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Exisort: The safe photo organizer.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: exisort [flags] <source_dir> <destination_dir>\n\nFlags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() >= 1 && flag.Arg(0) == "version" {
		fmt.Println("exisort", Version)
		os.Exit(0)
	}

	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}
	cfg.MinSizeBytes = rawSizeKB * 1024

	cfg.Extensions = make(map[string]bool)
	for e := range strings.SplitSeq(rawExts, ",") {
		cfg.Extensions[strings.ToLower(strings.TrimSpace(e))] = true
	}

	InitLogger()
	InitStats()

	metaSvc := &MetadataService{}
	defer metaSvc.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer func() {
		log.ClearStatus()
		stats.PrintSummary()
	}()

	if err := Run(ctx, metaSvc, flag.Arg(0), flag.Arg(1)); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Warn("Interrupted by user")
		} else {
			log.Error("Failed: %v", err)
			os.Exit(1)
		}
	}
}
