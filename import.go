package main

import (
	"crypto"
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

func runImport(metaSvc *MetadataService, srcRoot, dstRoot string) {
	start := time.Now()
	jobs := make(chan FileJob, 100)

	go func() {
		defer close(jobs)

		// Decision: We use synchronous filepath.WalkDir instead of a parallel worker pool.
		// Rationale:
		// 1. Simplicity: The code is much easier to maintain.
		// 2. IO Limits: Most sources (SD cards, HDDs) perform poorly with random concurrent reads.
		//    Linear scanning is often faster or equivalent for these devices.
		// 3. Performance: Current throughput is sufficient.
		filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				logger.Warn("Skipping unreadable path", "path", path, "err", err)
				return nil
			}

			if d.IsDir() {
				return nil
			}

			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
			if !config.Extensions[ext] {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				logger.Warn("Skipping file info", "path", path, "err", err)
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				logger.Warn("Skipping file open", "path", path, "err", err)
				return nil
			}
			defer f.Close()

			// 1. Read Header (Fingerprint)
			// We read up to 64KB to generate a "Short Hash" and validify file type.
			head := make([]byte, 64*1024)
			n, _ := io.ReadFull(f, head)
			validHead := head[:n]

			f.Seek(0, 0)

			// 2. Extract Date (EXIF or Fallback)
			date := metaSvc.GetTime(f, info)

			hash := computeFingerprint(validHead, info.Size())

			jobs <- FileJob{
				Path:       path,
				Info:       info,
				Date:       date,
				SourceHead: validHead,
				Hash:       hash,
			}
			return nil
		})
	}()

	for job := range jobs {
		destPath := filepath.Join(dstRoot, formatPath(config.Format, job.Date, job.Path))
		importOne(job, destPath)
	}

	if config.Verbose {
		logger.Info("Import finished", "duration", time.Since(start))
	}
}

func importOne(job FileJob, originalDest string) {
	finalDest := originalDest

	// 1. Resolve Conflicts & Detect Duplicates
	if _, err := os.Stat(finalDest); err == nil {

		// Case A: Exact Match at Target (No Rename needed)
		if isFileIdentical(job, finalDest) {
			handleDuplicate(job)
			return
		}

		// Conflict handling based on config
		if config.Conflict == "skip" {
			return
		} else if config.Conflict == "overwrite" {
			// Do nothing, let it fall through to copy logic
		} else {
			// Mode: "rename" (Default)

			// Case B: Try appending Short Hash
			// "Image.jpg" -> "Image_A1B2C3D4.jpg"
			ext := filepath.Ext(originalDest)
			base := strings.TrimSuffix(originalDest, ext)

			// TODO: 16-char hex hash probably is too much. Maybe just got half of it?
			hashedDest := fmt.Sprintf("%s_%016x%s", base, job.Hash, ext)

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
					counterDest := fmt.Sprintf("%s_%s_%d%s", base, job.Hash, n, ext)
					if _, err := os.Stat(counterDest); os.IsNotExist(err) {
						finalDest = counterDest
						break
					}
					n++
				}
			}
		}
	}

	// 2. Perform Copy/Move to the resolved finalDest
	performTransfer(job, finalDest)
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

	if config.DeepCheck || config.Action == "move" {
		fullMatch, _ := areFilesDeepIdentical(job.Path, existingPath)
		return fullMatch
	}

	return true
}

func handleDuplicate(job FileJob) {
	if config.Verbose {
		logger.Info("Duplicate skipped", "file", filepath.Base(job.Path))
	}
	// If Action is "Move", we must delete the source because the file
	// is already safe at the destination.
	if config.Action == "move" && !config.DryRun {
		os.Remove(job.Path)
	}
}

func performTransfer(job FileJob, destPath string) {
	if config.DryRun {
		logger.Info("Would "+config.Action, "src", job.Path, "dest", destPath)
		return
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		logger.Error("Mkdir failed", "err", err)
		return
	}

	var err error
	if config.Action == "move" {
		if err = os.Rename(job.Path, destPath); err != nil {
			if err = copyFile(job.Path, destPath); err == nil {
				os.Remove(job.Path)
			}
		}
	} else {
		err = copyFile(job.Path, destPath)
	}

	if err != nil {
		logger.Error("IO Error", "src", job.Path, "err", err)
	} else if config.Verbose {
		logger.Info("Processed", "file", filepath.Base(job.Path))
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

	if n != len(sourceHead) {
		return false
	}

	for i := 0; i < n; i++ {
		if sourceHead[i] != destHead[i] {
			return false
		}
	}
	return true
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
// This is NOT a cryptographic hash; it is used for file differentiation in naming.
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

	h := crypto.SHA256.New()

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func formatPath(fmtStr string, t time.Time, path string) string {
	r := strings.NewReplacer(
		"{year}", fmt.Sprintf("%04d", t.Year()),
		"{month}", fmt.Sprintf("%02d", t.Month()),
		"{day}", fmt.Sprintf("%02d", t.Day()),
		"{hour}", fmt.Sprintf("%02d", t.Hour()),
		"{min}", fmt.Sprintf("%02d", t.Minute()),
		"{sec}", fmt.Sprintf("%02d", t.Second()),
		"{filename}", strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		"{ext}", strings.TrimPrefix(filepath.Ext(path), "."),
	)
	return r.Replace(fmtStr)
}

func copyFile(src, dst string) error {
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

	_, err = io.Copy(out, in)
	return err
}
