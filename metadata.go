package main

import (
	"errors"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/barasher/go-exiftool"
	"github.com/levmv/exisort/exifdate"
)

type MetadataService struct {
	et *exiftool.Exiftool
	mu sync.Mutex
}

// Close cleans up the ExifTool process if it was started.
func (s *MetadataService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.et != nil {
		s.et.Close()
		s.et = nil
	}
}

// ensureExifTool lazily initializes the ExifTool instance.
func (s *MetadataService) ensureExifTool() (*exiftool.Exiftool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.et != nil {
		return s.et, nil
	}

	et, err := exiftool.NewExiftool()
	if err != nil {
		return nil, err
	}
	s.et = et
	return s.et, nil
}

func (s *MetadataService) GetTime(f *os.File, info fs.FileInfo) time.Time {
	// 1. Try native Go parser (fast, zero-alloc)
	t, err := exifdate.Get(f)
	if err == nil {
		return t
	}

	// 2. Fallback to ExifTool if format is unsupported (e.g., complex Video)
	if errors.Is(err, exifdate.ErrUnsupported) {
		if tFallback, found := s.fallbackExifTool(f.Name()); found {
			return tFallback
		}
	}
	return info.ModTime()
}

func (s *MetadataService) fallbackExifTool(path string) (time.Time, bool) {
	et, err := s.ensureExifTool()
	if err != nil {
		// ExifTool likely not installed or failed to start
		// TODO: probably log something in verbose mode?
		return time.Time{}, false
	}

	fileInfos := et.ExtractMetadata(path)

	for _, fileInfo := range fileInfos {
		if fileInfo.Err != nil {
			continue
		}

		keys := []string{"DateTimeOriginal", "CreateDate", "MediaCreateDate"}

		for _, key := range keys {
			if dateStr, ok := fileInfo.Fields[key].(string); ok {
				parsedTime, err := time.Parse("2006:01:02 15:04:05", dateStr)
				if err == nil {
					return parsedTime, true
				}
			}
		}
	}

	return time.Time{}, false
}
