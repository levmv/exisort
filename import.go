package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc64"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Run(ctx context.Context, metaSvc *MetadataService, srcRoot, dstRoot string) error {
	jobs := make(chan FileJob, 100)

	go func() {
		defer close(jobs)
		scanSource(ctx, metaSvc, srcRoot, jobs)
	}()

	c := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job, ok := <-jobs:
			if !ok {
				return nil
			}

			destPath := filepath.Join(dstRoot, formatPath(cfg.Format, job.Date, job.Path))
			c++
			if c%20 == 0 {
				log.Status("Scanned: %d | Processing: %s...", stats.FilesScanned.Load(), job.Path)
			}

			importOne(ctx, job, destPath)
		}
	}
}

func scanSource(ctx context.Context, metaSvc *MetadataService, root string, jobs chan<- FileJob) {
	// Decision: We use synchronous filepath.WalkDir instead of a parallel worker pool.
	// It much simpler. And often not that slower especially on slow disks.
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Warn("Skipping path %s: %v", path, err)
			return nil
		}

		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if !cfg.Extensions[ext] {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			log.Warn("Skipping file info for %s: %v", path, err)
			return nil
		}

		if info.Size() < cfg.MinSizeBytes {
			if cfg.Verbose {
				log.Warn("Skipping %s: too small (%d B)", path, info.Size())
			}
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			log.Warn("Skipping file info for %s: %v", path, err)
			return nil
		}
		defer f.Close()

		// We read up to 64KB to generate a "Short Hash" and validify file type.
		head := make([]byte, 64*1024)
		n, err := io.ReadFull(f, head)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Warn("Failed to read header %s: %v", path, err)
			return nil
		}
		validHead := head[:n]

		f.Seek(0, 0)

		// Extract Date (EXIF or Fallback)
		date := metaSvc.GetTime(f, info)

		hash := computeFingerprint(validHead, info.Size())

		stats.IncScanned()

		select {
		case <-ctx.Done():
			return filepath.SkipAll
		case jobs <- FileJob{
			Path:       path,
			Info:       info,
			Date:       date,
			SourceHead: validHead,
			Hash:       hash,
		}:
		}

		return nil
	})
}

func importOne(ctx context.Context, job FileJob, originalDest string) {
	finalDest := originalDest

	// 1. Resolve Conflicts & Detect Duplicates
	if _, err := os.Stat(finalDest); err == nil {

		// Case A: Exact Match at Target (No Rename needed)
		if isFileIdentical(job, finalDest) {
			handleDuplicate(job)
			return
		}

		// Conflict handling based on config
		if cfg.Conflict == "skip" {
			return
		} else if cfg.Conflict == "overwrite" {
			// Do nothing, let it fall through to copy logic
		} else {
			// Mode: "rename" (Default)

			// Case B: Try appending Short Hash
			// "Image.jpg" -> "Image_A1B2C3D4.jpg"
			ext := filepath.Ext(originalDest)
			base := strings.TrimSuffix(originalDest, ext)

			// TODO: 16-char hex hash probably is too much. Maybe just got half of it?
			hashedDest := fmt.Sprintf("%s_%08x%s", base, job.Hash, ext)

			if _, err := os.Stat(hashedDest); os.IsNotExist(err) {
				// Slot is free!
				finalDest = hashedDest
			} else {
				// File with Hash exists. Is it the same file?
				// (e.g. we ran import twice and previous run renamed it)
				if isFileIdentical(job, hashedDest) {
					handleDuplicate(job)
					return
				}

				// Case C: Hash Collision (Rare) or file content changed.
				// Start counting: "Image_A1B2C3D4_1.jpg"
				n := 1
				for {
					counterDest := fmt.Sprintf("%s_%08x_%d%s", base, job.Hash, n, ext)
					if _, err := os.Stat(counterDest); os.IsNotExist(err) {
						finalDest = counterDest
						break
					}
					if isFileIdentical(job, counterDest) {
						handleDuplicate(job)
						return
					}
					n++
				}
			}
		}
	}

	// 2. Perform Copy/Move to the resolved finalDest
	transferFile(job, finalDest)
}

func isFileIdentical(job FileJob, existingPath string) bool {
	info, err := os.Stat(existingPath)
	if err != nil {
		return false
	}

	if info.Size() != job.Info.Size() {
		return false
	}

	if !areHeadersIdentical(existingPath, job.SourceHead) {
		return false
	}

	if cfg.DeepCheck || cfg.Move {
		fullMatch, _ := areFilesDeepIdentical(job.Path, existingPath)
		return fullMatch
	}

	return true
}

func handleDuplicate(job FileJob) {
	stats.IncDuplicate()

	if cfg.DryRun {
		log.Duplicate(job.Path)
		// log.Action(tag.Dry(), "%s (Duplicate)", job.Path)
		return
	}

	if cfg.Move {
		if err := os.Remove(job.Path); err != nil {
			log.Error("Failed to delete duplicate source %s: %v", job.Path, err)
			return
		}
	}
	log.Duplicate(job.Path)
}

func transferFile(job FileJob, destPath string) {
	if cfg.DryRun {
		log.Transfer(job.Path, destPath)
		return
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		stats.IncError()
		log.Error("Mkdir failed for %s: %v", destPath, err)
		return
	}

	var err error
	if cfg.Move {
		if err = os.Rename(job.Path, destPath); err != nil {
			if err = copyFile(job.Path, destPath, job.Info); err == nil {
				os.Remove(job.Path)
			}
		}
	} else {
		err = copyFile(job.Path, destPath, job.Info)
	}

	if err != nil {
		stats.IncError()
		log.Error("IO Error %s: %v", job.Path, err)
	} else {
		stats.IncProcessed()
		stats.AddBytes(job.Info.Size())
		log.Transfer(job.Path, destPath)
	}
}

// areHeadersIdentical compares the in-memory source header against the destination file on disk.
func areHeadersIdentical(destPath string, sourceHead []byte) bool {
	f, err := os.Open(destPath)
	if err != nil {
		return false
	}
	defer f.Close()

	destHead := make([]byte, len(sourceHead))
	n, _ := io.ReadFull(f, destHead)

	return n == len(sourceHead) && string(destHead) == string(sourceHead)
}

func areFilesDeepIdentical(src, dst string) (bool, error) {
	h1, err := computeFullHash(src)
	if err != nil {
		return false, err
	}

	h2, err := computeFullHash(dst)
	if err != nil {
		return false, err
	}

	return h1 == h2, nil
}

var crcTable = crc64.MakeTable(crc64.ISO)

// computeFingerprint calculates a fast hash based on the file header and file size.
func computeFingerprint(header []byte, size int64) uint64 {
	h := crc64.New(crcTable)
	h.Write(header)

	var sizeBuf [8]byte
	binary.LittleEndian.PutUint64(sizeBuf[:], uint64(size))
	h.Write(sizeBuf[:])

	return h.Sum64()
}

// computeFullHash calculates the SHA256 of the entire file.
// Used for the --deep check to ensure absolute duplicate safety.
func computeFullHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func formatPath(fmtStr string, t time.Time, path string) string {
	_, file := filepath.Split(path)
	ext := filepath.Ext(file)
	name := strings.TrimSuffix(file, ext)
	if len(ext) > 0 {
		ext = ext[1:] // remove dot
	}

	// Use t.Format for everything. It's cleaner.
	r := strings.NewReplacer(
		"{year}", t.Format("2006"),
		"{month}", t.Format("01"),
		"{day}", t.Format("02"),
		"{hour}", t.Format("15"),
		"{min}", t.Format("04"),
		"{sec}", t.Format("05"),
		"{filename}", name,
		"{ext}", ext,
	)
	return r.Replace(fmtStr)
}

func copyFile(src, dst string, srcInfo fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}

	if err := os.Chtimes(dst, time.Now(), srcInfo.ModTime()); err != nil {
		// log.Warn("Fail to upgrade file time: %v", err)
	}

	return nil
}
