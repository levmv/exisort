package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// --- Clean Pipeline ---

func runClean(targetDir string) {
	start := time.Now()

	// 1. Collect Phase (Map by Size)
	bySize := make(map[int64][]FileJob)
	count := 0

	filepath.WalkDir(targetDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if config.Extensions[ext] {
			info, _ := d.Info()
			// Minimal info for cleaning
			job := FileJob{Path: path, Info: info, Date: info.ModTime()}
			bySize[info.Size()] = append(bySize[info.Size()], job)
			count++
		}
		return nil
	})

	if config.Verbose {
		log.Info("Scan complete %d files, %d", count, time.Since(start))
	}

	// 2. Hash Phase (Only potential duplicates)
	for _, files := range bySize {
		if len(files) < 2 {
			continue
		}

		byHash := make(map[string][]FileJob)
		for _, f := range files {
			h, err := computeFullHash(f.Path)
			if err == nil {
				byHash[h] = append(byHash[h], f)
			}
		}

		// 3. Act Phase
		for _, duplicates := range byHash {
			if len(duplicates) > 1 {
				cleanDuplicates(duplicates)
			}
		}
	}
}

func cleanDuplicates(files []FileJob) {
	// Sort to pick keeper
	sort.Slice(files, func(i, j int) bool {
		switch config.Keep {
		case "newest":
			return files[i].Date.After(files[j].Date)
		case "shortest-path":
			if len(files[i].Path) != len(files[j].Path) {
				return len(files[i].Path) < len(files[j].Path)
			}
			return files[i].Path < files[j].Path
		default: // oldest
			return files[i].Date.Before(files[j].Date)
		}
	})

	toRemove := files[1:]

	for _, f := range toRemove {
		if config.DryRun {
			log.Action(config.Action, f.Path)
			continue
		}

		if config.Action == "trash" {
			dest := filepath.Join(config.TrashDir, filepath.Base(f.Path))
			if _, err := os.Stat(dest); err == nil {
				dest += fmt.Sprintf(".%d", time.Now().UnixNano())
			}
			os.MkdirAll(filepath.Dir(dest), 0755)
			os.Rename(f.Path, dest)
		} else if config.Action == "delete" {
			os.Remove(f.Path)
		} else {
			fmt.Println("Duplicate:", f.Path)
		}
	}
}
