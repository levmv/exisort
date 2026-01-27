package exifdate

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ExtractExifFromHEIC reads from r and returns raw EXIF bytes (TIFF header + data).
func ExtractExifFromHEIC(r io.ReadSeeker) ([]byte, error) {
	// 1. Locate the 'meta' box
	metaBox, err := findBox(r, 0, ^uint64(0), "meta")
	if err != nil {
		return nil, fmt.Errorf("meta box not found: %w", err)
	}

	// 'meta' is a FullBox. It has 4 bytes (Version + Flags) at the start of its data.
	metaChildrenOffset := metaBox.dataOffset + 4
	metaChildrenEnd := metaBox.dataOffset + metaBox.dataSize

	// 2. Locate 'iinf' (Item Info) inside 'meta'
	iinf, err := findBox(r, metaChildrenOffset, metaChildrenEnd, "iinf")
	if err != nil {
		return nil, fmt.Errorf("iinf box not found: %w", err)
	}

	// 3. Parse 'iinf' children ('infe') to find the item ID for Exif
	exifItemIDs, err := parseInfeForExif(r, iinf)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse infe: %v", ErrUnsupported, err)
	}

	if len(exifItemIDs) == 0 {
		return nil, fmt.Errorf("%w: no supported Exif item info found (possible version mismatch)", ErrUnsupported)
	}

	// 4. Locate 'iloc' (Item Location) inside 'meta'
	iloc, err := findBox(r, metaChildrenOffset, metaChildrenEnd, "iloc")
	if err != nil {
		return nil, fmt.Errorf("iloc box not found: %w", err)
	}

	// We only need the location for the first Exif ID found
	targetID := exifItemIDs[0]
	locs, err := parseIloc(r, iloc.dataOffset, iloc.dataSize, targetID)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse iloc: %v", ErrUnsupported, err)
	}

	if len(locs) == 0 {
		return nil, fmt.Errorf("%w: exif item location definition not found", ErrUnsupported)
	}

	// 5. Determine if 'idat' is required and scan for it only if necessary.
	var idatOffset uint64
	needsIdat := false

	// Check if any extent uses construction_method 1 (idat-relative)
	for _, loc := range locs {
		if loc.constructionMethod == 1 {
			needsIdat = true
			break
		}
	}

	if needsIdat {
		_ = scanBoxes(r, 0, ^uint64(0), func(b boxHeader) (bool, error) {
			if b.typ == "idat" {
				idatOffset = b.dataOffset
				return true, nil
			}
			return false, nil
		})

		// Note: If idat is needed but not found, idatOffset remains 0.
		// readItemData handles the error if it sees constructionMethod 1 and idatOffset 0.
	}

	// 6. Read the data
	itemData, err := readItemData(r, locs, idatOffset)
	if err != nil {
		return nil, err
	}

	// 7. Clean up the Exif wrapper (4 byte offset + "Exif\0\0") to get raw TIFF
	return stripExifWrapper(itemData), nil
}

// -------------------------------------------------------------------------
// Low Level Parsing
// -------------------------------------------------------------------------

type boxHeader struct {
	offset     uint64
	size       uint64
	typ        string
	dataOffset uint64
	dataSize   uint64
}

func readBoxHeader(r io.ReadSeeker, offset uint64) (boxHeader, error) {
	_, err := r.Seek(int64(offset), io.SeekStart)
	if err != nil {
		return boxHeader{}, err
	}
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return boxHeader{}, err
	}
	size := uint64(binary.BigEndian.Uint32(buf[0:4]))
	typ := string(buf[4:8])
	headerSize := uint64(8)

	if size == 1 {
		// Large size (64-bit)
		var large [8]byte
		if _, err := io.ReadFull(r, large[:]); err != nil {
			return boxHeader{}, err
		}
		size = binary.BigEndian.Uint64(large[:])
		headerSize = 16
	} else if size == 0 {
		// Extends to EOF
		cur, _ := r.Seek(0, io.SeekCurrent)
		end, _ := r.Seek(0, io.SeekEnd)
		size = uint64(end) - offset
		_, _ = r.Seek(cur, io.SeekStart)
	}

	if size < headerSize {
		return boxHeader{}, fmt.Errorf("box '%s' size (%d) is smaller than header size (%d)", typ, size, headerSize)
	}

	return boxHeader{
		offset:     offset,
		size:       size,
		typ:        typ,
		dataOffset: offset + headerSize,
		dataSize:   size - headerSize,
	}, nil
}

func scanBoxes(r io.ReadSeeker, start, end uint64, cb func(boxHeader) (bool, error)) error {
	pos := start
	for pos < end {
		// Sanity check: ensure we have at least 8 bytes left
		if end-pos < 8 {
			break
		}
		bh, err := readBoxHeader(r, pos)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if bh.size == 0 {
			// Prevent infinite loop if size is 0 (excluding the EOF-marker case which is handled in readBoxHeader)
			break
		}

		done, err := cb(bh)
		if err != nil {
			return err
		}
		if done {
			return nil
		}

		pos += bh.size
	}
	return nil
}

func findBox(r io.ReadSeeker, start, end uint64, targetType string) (boxHeader, error) {
	var result boxHeader
	found := false
	err := scanBoxes(r, start, end, func(b boxHeader) (bool, error) {
		if b.typ == targetType {
			result = b
			found = true
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return boxHeader{}, err
	}
	if !found {
		return boxHeader{}, fmt.Errorf("box %s not found", targetType)
	}
	return result, nil
}

// parseInfeForExif scans the 'iinf' box.
// iinf is a FullBox: [4 bytes Ver+Flags] + [2 or 4 bytes EntryCount] + [infe boxes...]
func parseInfeForExif(r io.ReadSeeker, iinf boxHeader) ([]uint32, error) {
	// 1. Read iinf Version to know how large EntryCount is
	if iinf.dataSize < 4 {
		return nil, errors.New("iinf too small")
	}
	_, err := r.Seek(int64(iinf.dataOffset), io.SeekStart)
	if err != nil {
		return nil, err
	}

	// Read Version (1) + Flags (3)
	var vf [4]byte
	if _, err := io.ReadFull(r, vf[:]); err != nil {
		return nil, err
	}
	version := vf[0]

	// Calculate where the child boxes start.
	offsetWithin := uint64(4)
	if version == 0 {
		offsetWithin += 2
	} else {
		offsetWithin += 4
	}

	startScan := iinf.dataOffset + offsetWithin
	endScan := iinf.dataOffset + iinf.dataSize

	var ids []uint32

	scratch := make([]byte, 16)

	err = scanBoxes(r, startScan, endScan, func(b boxHeader) (bool, error) {
		if b.typ != "infe" {
			return false, nil
		}

		// infe is also a FullBox.
		// We need at least 16 bytes to safely parse up to item_type in V3.
		if b.dataSize < 12 {
			return false, nil
		}

		buf := scratch[:min(int(b.dataSize), 16)]
		_, _ = r.Seek(int64(b.dataOffset), io.SeekStart)
		if _, err := io.ReadFull(r, buf); err != nil {
			return false, nil
		}

		infeVersion := buf[0]
		pos := 4 // skip version(1) + flags(3)

		var itemID uint32
		var itemType string

		switch infeVersion {
		case 2: // ID is 2 bytes, Protection is 2 bytes
			itemID = uint32(binary.BigEndian.Uint16(buf[pos : pos+2]))
			itemType = string(buf[pos+4 : pos+8]) // Skip 2(ID) + 2(Prot) = 4
		case 3: // ID is 4 bytes, Protection is 2 bytes
			itemID = binary.BigEndian.Uint32(buf[pos : pos+4])
			itemType = string(buf[pos+6 : pos+10]) // Skip 4(ID) + 2(Prot) = 6
		default:
			// Version 0/1 (no item_type field) or Future versions (unknown structure)
			return false, nil
		}

		if itemType == "Exif" {
			ids = append(ids, itemID)
		}
		return false, nil
	})

	return ids, err
}

type itemLocation struct {
	constructionMethod int
	baseOffset         uint64
	extents            []extent
}
type extent struct {
	offset uint64
	length uint64
}

// parseIloc parses the Item Location Box to find extents for a specific ID.
func parseIloc(r io.ReadSeeker, offset, size uint64, targetID uint32) ([]itemLocation, error) {
	if _, err := r.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, err
	}

	// Scratch buffer for reading small fields (max 8 bytes for uint64)
	var scratch [8]byte

	// Helper to read exactly n bytes
	readBytes := func(n int) ([]byte, error) {
		if n == 0 {
			return nil, nil
		}
		if n > len(scratch) {
			return nil, errors.New("field size too large")
		}
		b := scratch[:n]
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		return b, nil
	}

	// Helper to read variable-sized integers (n bytes)
	readUint := func(n int) (uint64, error) {
		if n == 0 {
			return 0, nil
		}
		b, err := readBytes(n)
		if err != nil {
			return 0, err
		}
		var x uint64
		for _, v := range b {
			x = (x << 8) | uint64(v)
		}
		return x, nil
	}

	// 1. Read FullBox Header: Version (1) + Flags (3) = 4 bytes
	// We only need the version.
	b, err := readBytes(4)
	if err != nil {
		return nil, err
	}
	version := b[0]

	// 2. Read Size Configuration (2 bytes)
	// Byte 1: offset_size (4 bits) + length_size (4 bits)
	// Byte 2: base_offset_size (4 bits) + index_size (4 bits, if ver >= 1)
	b, err = readBytes(2)
	if err != nil {
		return nil, err
	}
	offsetSize := int(b[0] >> 4)
	lengthSize := int(b[0] & 0x0F)
	baseOffsetSize := int(b[1] >> 4)
	indexSize := 0
	if version >= 1 {
		indexSize = int(b[1] & 0x0F)
	}

	// 3. Read Item Count
	var itemCount uint32
	if version < 2 {
		b, err := readBytes(2)
		if err != nil {
			return nil, err
		}
		itemCount = uint32(binary.BigEndian.Uint16(b))
	} else {
		b, err := readBytes(4)
		if err != nil {
			return nil, err
		}
		itemCount = binary.BigEndian.Uint32(b)
	}

	var locs []itemLocation

	// 4. Iterate over items
	for i := uint32(0); i < itemCount; i++ {
		// Read Item ID
		var itemID uint32
		if version < 2 {
			b, err := readBytes(2)
			if err != nil {
				return nil, err
			}
			itemID = uint32(binary.BigEndian.Uint16(b))
		} else {
			b, err := readBytes(4)
			if err != nil {
				return nil, err
			}
			itemID = binary.BigEndian.Uint32(b)
		}

		// Read Construction Method (if ver 1 or 2)
		constructionMethod := 0
		if version == 1 || version == 2 {
			b, err := readBytes(2)
			if err != nil {
				return nil, err
			}
			val := binary.BigEndian.Uint16(b)
			constructionMethod = int(val & 0x000F)
		}

		// Read Data Reference Index (2 bytes) - skip it, but consume bytes
		if _, err := readBytes(2); err != nil {
			return nil, err
		}

		// Read Base Offset
		baseOffset, err := readUint(baseOffsetSize)
		if err != nil {
			return nil, err
		}

		// Read Extent Count
		b, err = readBytes(2)
		if err != nil {
			return nil, err
		}
		extentCount := binary.BigEndian.Uint16(b)

		var currentExtents []extent
		isTarget := (itemID == targetID)

		// 5. Iterate over extents
		for e := 0; e < int(extentCount); e++ {
			// Read (and ignore) Index if present
			if version >= 1 && indexSize > 0 {
				if _, err := readUint(indexSize); err != nil {
					return nil, err
				}
			}

			off, err := readUint(offsetSize)
			if err != nil {
				return nil, err
			}

			lenVal, err := readUint(lengthSize)
			if err != nil {
				return nil, err
			}

			// Only store the data if this is the item we are looking for
			if isTarget {
				currentExtents = append(currentExtents, extent{offset: off, length: lenVal})
			}
		}

		if isTarget {
			locs = append(locs, itemLocation{
				constructionMethod: constructionMethod,
				baseOffset:         baseOffset,
				extents:            currentExtents,
			})
		}
	}

	return locs, nil
}

func readItemData(r io.ReadSeeker, locs []itemLocation, idatOffset uint64) ([]byte, error) {
	var out bytes.Buffer

	for _, loc := range locs {
		for _, ext := range loc.extents {
			var finalOffset int64

			// 0: Absolute, 1: Relative to idat
			switch loc.constructionMethod {
			case 0:
				finalOffset = int64(loc.baseOffset + ext.offset)
			case 1:
				if idatOffset == 0 {
					return nil, fmt.Errorf("%w: item uses idat-relative offset but idat box not found", ErrUnsupported)
				}
				finalOffset = int64(idatOffset + loc.baseOffset + ext.offset)
			default:
				finalOffset = int64(loc.baseOffset + ext.offset)
			}

			if ext.length == 0 {
				continue
			}

			_, err := r.Seek(finalOffset, io.SeekStart)
			if err != nil {
				return nil, err
			}
			if _, err := io.CopyN(&out, r, int64(ext.length)); err != nil {
				return nil, err
			}
		}
	}
	return out.Bytes(), nil
}

func stripExifWrapper(data []byte) []byte {
	// The standard HEIC Exif wrapper is: [4-byte offset] + [padding] + "Exif\0\0" + [TIFF Header]
	if len(data) >= 4 {
		offsetToExif := int(binary.BigEndian.Uint32(data[0:4]))
		startOfExifSig := 4 + offsetToExif

		// Ensure we don't go out of bounds checking for the signature
		if startOfExifSig+6 <= len(data) {
			signature := data[startOfExifSig : startOfExifSig+6]
			if string(signature) == "Exif\x00\x00" {
				// Found the standard header. The TIFF data starts immediately after.
				return data[startOfExifSig+6:]
			}
		}
	}

	//  Fallback
	// If the strict parse failed (invalid offset, missing wrapper, raw TIFF),
	// we simply scan the first 512 bytes for the TIFF alignment ('II' or 'MM').
	if idx := findTIFF(data); idx >= 0 {
		return data[idx:]
	}

	return nil
}

func isTIFF(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return (data[0] == 'I' && data[1] == 'I' && data[2] == 0x2A && data[3] == 0x00) ||
		(data[0] == 'M' && data[1] == 'M' && data[2] == 0x00 && data[3] == 0x2A)
}

func findTIFF(data []byte) int {
	limit := min(len(data), 512)
	for i := 0; i < limit-4; i++ {
		if (data[i] == 'I' && data[i+1] == 'I' && data[i+2] == 0x2A && data[i+3] == 0x00) ||
			(data[i] == 'M' && data[i+1] == 'M' && data[i+2] == 0x00 && data[i+3] == 0x2A) {
			return i
		}
	}
	return -1
}
