package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	Verbose    bool
	DryRun     bool
	DeepCheck  bool // If true, force full hash check on collisions
	Extensions map[string]bool
	Format     string
	Conflict   string // rename, skip, overwrite
	Action     string // move, copy (import); report, trash, delete (clean)
	Keep       string // oldest, newest, shortest-path
	TrashDir   string
}

// FileJob contains the "Fingerprint" of the source file
type FileJob struct {
	Path       string
	Info       fs.FileInfo
	Date       time.Time
	SourceHead []byte // First 64KB
	Hash       uint64
}

var (
	logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	config Config
)

func main() {
	importCmd := flag.NewFlagSet("import", flag.ExitOnError)
	cleanCmd := flag.NewFlagSet("clean", flag.ExitOnError)

	setupCommon := func(f *flag.FlagSet) *Config {
		c := &Config{Extensions: make(map[string]bool)}
		f.BoolVar(&c.Verbose, "v", false, "Verbose logging")
		f.BoolVar(&c.DryRun, "dry-run", false, "Dry run (no disk changes)")
		return c
	}

	cfgImport := setupCommon(importCmd)
	importCmd.StringVar(&cfgImport.Format, "format", "{year}/{year}-{month}/{year}{month}{day}_{hour}{min}{sec}.{ext}", "Naming format")
	importCmd.StringVar(&cfgImport.Conflict, "conflict", "rename", "Collision: rename, skip, overwrite")
	importCmd.BoolVar(&cfgImport.DeepCheck, "deep", false, "Force full hashing on collision")
	move := importCmd.Bool("move", false, "Move files instead of copying")
	extsImp := importCmd.String("extensions", "jpg,jpeg,heic,png,mov,mp4,arw,cr2,dng,nef", "Extensions")

	cfgClean := setupCommon(cleanCmd)
	cleanCmd.StringVar(&cfgClean.Action, "action", "report", "Action: report, trash, delete")
	cleanCmd.StringVar(&cfgClean.Keep, "keep", "oldest", "Keep strategy")
	cleanCmd.StringVar(&cfgClean.TrashDir, "trash-dir", "./_Exisort_Trash", "Trash directory")
	extsClean := cleanCmd.String("extensions", "jpg,jpeg,png,mov,mp4,heic", "Extensions")

	if len(os.Args) < 2 {
		fmt.Println("Usage: exisort <import|clean> [flags]")
		os.Exit(1)
	}

	metaSvc := &MetadataService{}
	defer metaSvc.Close()

	switch os.Args[1] {
	case "import":
		importCmd.Parse(os.Args[2:])
		args := importCmd.Args()
		if len(args) < 2 {
			logger.Error("Import requires <source> <dest>")
			os.Exit(1)
		}
		config = *cfgImport
		config.Action = "copy"
		if *move {
			config.Action = "move"
		}
		parseExts(config.Extensions, *extsImp)
		runImport(metaSvc, args[0], args[1])

	case "clean":
		cleanCmd.Parse(os.Args[2:])
		args := cleanCmd.Args()
		if len(args) < 1 {
			logger.Error("Clean requires <target>")
			os.Exit(1)
		}
		config = *cfgClean
		parseExts(config.Extensions, *extsClean)
		runClean(args[0])
	}
}

func parseExts(m map[string]bool, s string) {
	for _, e := range strings.Split(s, ",") {
		m[strings.ToLower(strings.TrimSpace(e))] = true
	}
}
