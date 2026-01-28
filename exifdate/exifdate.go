package exifdate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	TagExifOffset       = 0x8769
	TagDateTime         = 0x0132
	TagDateTimeOriginal = 0x9003
)

func ParseDate(data []byte) (time.Time, error) {
	if len(data) < 8 {
		// Too short to be any known EXIF/TIFF structure
		return time.Time{}, fmt.Errorf("%w: data too short", ErrUnsupported)
	}

	// 1. Determine Endianness (Zero Alloc)
	// Direct byte comparison is faster than string conversion
	var order binary.ByteOrder
	if data[0] == 'I' && data[1] == 'I' {
		order = binary.LittleEndian
	} else if data[0] == 'M' && data[1] == 'M' {
		order = binary.BigEndian
	} else {
		return time.Time{}, fmt.Errorf("%w: invalid tiff header", ErrUnsupported)
	}

	// 2. Check Magic Number
	if order.Uint16(data[2:4]) != 42 {
		return time.Time{}, fmt.Errorf("%w: invalid magic number", ErrUnsupported)
	}

	// 3. Get offset to first IFD
	ifdOffset := int(order.Uint32(data[4:8]))

	// --- Pass 1: Scan IFD0 ---
	// We look for:
	// 1. TagExifOffset (to go deeper)
	// 2. TagDateTime (as a fallback)

	var exifOffset int
	var fallbackDateStr string

	err := iterateTags(data, ifdOffset, order, func(tag uint16, offset int, count uint32) {
		if tag == TagExifOffset {
			// Found pointer to Sub-IFD. It's a Long (4 bytes).
			// It fits inside the value field (bytes 8-12 relative to tag start).
			// Tag structure: [ID:2][Type:2][Count:4][Value/Offset:4]
			// The value starts at offset + 8
			if offset+12 <= len(data) {
				exifOffset = int(order.Uint32(data[offset+8 : offset+12]))
			}
		} else if tag == TagDateTime {
			// Found Modify Date. Read it just in case we don't find Original.
			fallbackDateStr = extractString(data, offset, count, order)
		}
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: tiff structure corruption: %v", ErrUnsupported, err)
	}

	// --- Pass 2: Scan Exif Sub-IFD (if found) ---
	if exifOffset > 0 {
		var originalDateStr string
		_ = iterateTags(data, exifOffset, order, func(tag uint16, offset int, count uint32) {
			if tag == TagDateTimeOriginal {
				originalDateStr = extractString(data, offset, count, order)
			}
		})

		// If we found the original date, parse and return immediately
		if originalDateStr != "" {
			return parseExifTime(originalDateStr)
		}
	}

	// Fallback
	if fallbackDateStr != "" {
		return parseExifTime(fallbackDateStr)
	}

	return time.Time{}, errors.New("no date tag found")
}

// iterateTags walks a directory and calls 'fn' for every tag.
// It performs NO allocations.
// fn arguments: tagID, absoluteOffsetToStartOfTag, valueCount
func iterateTags(data []byte, dirOffset int, order binary.ByteOrder, fn func(uint16, int, uint32)) error {
	if dirOffset+2 > len(data) {
		return errors.New("offset out of bounds")
	}

	count := int(order.Uint16(data[dirOffset : dirOffset+2]))
	current := dirOffset + 2

	for i := 0; i < count; i++ {
		if current+12 > len(data) {
			return errors.New("tag out of bounds")
		}

		tag := order.Uint16(data[current : current+2])
		// Optimization: We don't read Type here because we know what types we expect for specific tags.
		countVal := order.Uint32(data[current+4 : current+8])

		fn(tag, current, countVal)

		current += 12
	}
	return nil
}

// extractString reads the ASCII string from the tag.
func extractString(data []byte, tagStartOffset int, count uint32, order binary.ByteOrder) string {
	// Value/Offset field is at tagStartOffset + 8
	valueOffset := tagStartOffset + 8

	var strStart int
	// If data fits in 4 bytes (unlikely for dates, but possible for other strings)
	if count <= 4 {
		strStart = valueOffset
	} else {
		// It's an offset
		strStart = int(order.Uint32(data[valueOffset : valueOffset+4]))
	}
	if strStart < 0 {
		return ""
	}

	if strStart+int(count) > len(data) {
		return ""
	}

	raw := data[strStart : strStart+int(count)]

	if idx := bytes.IndexByte(raw, 0); idx != -1 {
		raw = raw[:idx]
	}

	raw = bytes.TrimSpace(raw)

	return string(raw)
}

var nativeLayouts = []string{
	"2006:01:02 15:04:05",
	"2006:01:02 15:04:05-07:00",
	"2006:01:02 15:04:05+07:00",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04:05-07:00",
	"2006-01-02T15:04:05",
}

func parseExifTime(s string) (time.Time, error) {
	if len(s) < 10 || strings.HasPrefix(s, "0000:00:00") || strings.HasPrefix(s, "    :  :  ") {
		// This is NOT "Unsupported". It is just "No Date".
		// We do NOT want to trigger ExifTool for this.
		return time.Time{}, errors.New("date not set")
	}

	for _, layout := range nativeLayouts {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("%w: unknown date format '%s'", ErrUnsupported, s)
}
