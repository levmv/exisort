package exifdate

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"time"
)

var (
	ErrUnsupported = errors.New("unsupported format")
	exifHeader     = []byte{'E', 'x', 'i', 'f', 0x00, 0x00}
)

// Get attempts to find and parse the EXIF date from a file.
func Get(f *os.File) (time.Time, error) {
	blob, err := ExtractEXIF(f)
	if err != nil {
		return time.Time{}, err
	}
	if blob == nil {
		return time.Time{}, errors.New("no exif data found")
	}
	return ParseDate(blob)
}

func ExtractEXIF(r io.ReadSeeker) ([]byte, error) {
	sniff := make([]byte, 12)
	if _, err := io.ReadFull(r, sniff); err != nil {
		return nil, err
	}

	if _, err := r.Seek(0, 0); err != nil {
		return nil, err
	}

	switch {
	case bytes.HasPrefix(sniff, []byte{0xFF, 0xD8}):
		return extractJPEG(r)
	case isHEIC(sniff):
		return ExtractExifFromHEIC(r)
	case bytes.HasPrefix(sniff, []byte{0x89, 0x50, 0x4E, 0x47}):
		return extractPNG(r)
	default:
		return nil, ErrUnsupported
	}
}

func isHEIC(sig []byte) bool {
	if !bytes.Equal(sig[4:8], []byte("ftyp")) {
		return false
	}
	brand := string(sig[8:12])
	return brand == "heic" || brand == "heix" || brand == "mif1" || brand == "msf1"
}

func extractJPEG(r io.Reader) ([]byte, error) {
	br := bufio.NewReader(r)
	var sizeBuf [2]byte

	maxScan := 1 << 20 // 1MB scan limit for performance
	scanned := 0

	for scanned < maxScan {
		// 1. Find Start of Marker (0xFF)
		b, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		scanned++

		if b != 0xFF {
			continue
		}

		// 2. Consume Padding
		var marker byte
		for {
			marker, err = br.ReadByte()
			if err != nil {
				return nil, err
			}
			scanned++
			if marker != 0xFF {
				break
			}
		}

		// 3. Handle Markers
		if marker == 0xD8 { // SOI
			continue
		}
		if marker == 0xD9 || marker == 0xDA { // EOI or SOS (Scan data starts, stop looking)
			return nil, nil
		}
		// Skip standalone markers
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			continue
		}

		// 4. Read Length
		if _, err := io.ReadFull(br, sizeBuf[:]); err != nil {
			return nil, err
		}
		length := int(binary.BigEndian.Uint16(sizeBuf[:])) - 2
		scanned += 2

		// 5. Check for APP1 Exif
		if marker == 0xE1 && length >= 6 {
			sig, err := br.Peek(6)
			if err == nil && bytes.Equal(sig, exifHeader) {
				data := make([]byte, length)
				if _, err := io.ReadFull(br, data); err != nil {
					return nil, err
				}
				return data[6:], nil
			}
		}

		// 6. Enforce Limit
		if length > (maxScan - scanned) {
			return nil, nil
		}

		// 7. Skip Payload
		if length > 0 {
			skipped, err := br.Discard(length)
			if err != nil {
				return nil, err
			}
			scanned += skipped
		}
	}

	return nil, nil
}

// extractPNG walks through PNG chunks looking for the "eXIf" chunk.
func extractPNG(r io.Reader) ([]byte, error) {
	if _, err := io.CopyN(io.Discard, r, 8); err != nil {
		return nil, err
	}

	// Buffer for Length (4 bytes) and Type (4 bytes)
	header := make([]byte, 8)

	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if err == io.EOF {
				return nil, nil // End of file, no EXIF found
			}
			return nil, err
		}

		length := binary.BigEndian.Uint32(header[0:4])
		chunkType := string(header[4:8])

		if chunkType == "eXIf" {
			// Sanity check: EXIF shouldn't be massive (usually < 64KB)
			// Limit to 10MB to prevent OOM attacks
			if length > 10*1024*1024 {
				return nil, errors.New("exif data too large")
			}

			data := make([]byte, length)
			if _, err := io.ReadFull(r, data); err != nil {
				return nil, err
			}
			// Note: PNG eXIf chunks contain the raw TIFF structure (II/MM...)
			// They usually do NOT have the "Exif\0\0" header that JPEG has,
			// so we return the data as-is.
			return data, nil
		}

		if chunkType == "IEND" {
			return nil, nil
		}

		skipAmount := int64(length) + 4 // Skip Payload + CRC
		if _, err := io.CopyN(io.Discard, r, skipAmount); err != nil {
			return nil, err
		}
	}
}
