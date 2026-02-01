
# Exisort

**The safe, high-performance photo organizer.**

Exisort is a command-line tool written in Go designed to tame chaotic photo collections. It imports photos and videos from SD cards or messy directories, organizes them into a clean folder structure based on EXIF metadata, and intelligently handles duplicates.

Unlike simple scripts, Exisort calculates content hashes to distinguish between a "duplicate file" and a "different photo taken at the same second," ensuring you never lose data.

## Features

*   **Smart Import:** organizing by Date or custom patterns.
*   **Collision Detection:** Automatically handles filename collisions. If `Img_01.jpg` exists, Exisort checks the content. If it's the same file, it skips it. If it's different, it renames the new one automatically.
*   **Metadata Fallback:** Intelligently looks for `DateTimeOriginal`, `CreateDate`, or `FileModifyDate` (in that order) to ensure files are dated correctly.
*   **Video Support:** Handles `.mov`, `.mp4`, and other formats natively or via ExifTool fallback.


---

## Usage

```bash
exisort [flags] <source_dir> <destination_dir>
```

### Examples

**1. Basic Import (Copy)**

Safely copy photos from an SD card to your library, organized by year and month.
```bash
exisort /Volumes/EOS_DIGITAL ~/Photos
```

**2. Move and Sort**

Move files (delete from source after successful transfer) and use a custom naming format.
```bash
exisort --move --format "{year}/{month}/{year}-{month}-{day}_{hour}{min}{sec}.{ext}" /Downloads/Unsorted ~/Photos
```

**3. Simulate (Dry Run)**

See exactly what would happen without moving any files.
```bash
exisort --dry-run --move /Volumes/SD ~/Photos
```

---

## Configuration

### Core Flags
*   `--move`: Move files instead of copying them. Verifies transfer before deleting source.
*   `--dry-run`: Print actions that would be performed without making changes.
*   `-v`: Enable verbose logging (shows skipped files and details).

### Naming & Organization
*   `--format <string>`
    *   Defines the destination folder structure and filename.
    *   **Default:** `{year}/{year}-{month}/{year}{month}{day}_{hour}{min}{sec}.{ext}`
    *   **Tokens:**
        *   `{year}`, `{month}`, `{day}`: Date components.
        *   `{hour}`, `{min}`, `{sec}`: Time components.
        *   `{filename}`: Original filename (excluding extension).
        *   `{ext}`: File extension.

### Conflict Handling
What happens if the destination file already exists?

*   `--conflict <mode>`
    *   `rename` (Default): Calculate hash. If content matches, treat as duplicate (skip/delete source). If content differs, append the short hash or a counter to the filename.
    *   `skip`: Do not process the file if a file with the same name exists (regardless of content).
    *   `overwrite`: Replace the destination file with the source file (Use with caution).

*   `--deep`: Perform a full SHA-256 hash comparison when checking for duplicates.
    *   By default, Exisort uses a fast "Header + Size" fingerprint (CRC64 of first 64KB) to detect duplicates. This is extremely fast and reliable for 99.9% of cases. Use `--deep` if you need cryptographic certainty.

### Filtering
*   `--extensions <list>`: Comma-separated list of extensions to process.
    *   **Default:** `jpg,jpeg,png,heic,heif,mov,mp4,m4v,avi,arw,cr2,cr3,dng,nef,orf,raf,rw2`

---

## Installation

```bash
go install github.com/levmv/exisort@latest
```
