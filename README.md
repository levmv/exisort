
# Exisort

**The safe, high-performance photo organizer and deduplicator.**

Exisort is a command-line tool written in Go designed to tame chaotic photo collections. It imports photos and videos from SD cards or messy directories, organizes them into a clean folder structure based on EXIF metadata, and intelligently handles duplicates.

Unlike simple scripts, Exisort calculates content hashes to distinguish between a "duplicate file" and a "different photo taken at the same second," ensuring you never lose data.

## Features

*   **Smart Import:** organizing by Date, Camera Model, or custom patterns.
*   **Collision Detection:** Automatically handles filename collisions. If `Img_01.jpg` exists, Exisort checks the content. If it's the same file, it skips it. If it's different, it renames the new one automatically.
*   **Metadata Fallback:** Intelligently looks for `DateTimeOriginal`, `CreateDate`, or `FileModifyDate` (in that order) to ensure files are dated correctly.
*   **Video Support:** Handles `.mov`, `.mp4`, and other formats natively or via ExifTool fallback.
*   **Duplicate Cleaner:** A dedicated mode to scan existing libraries, find duplicates by content (hash), and safely move them to a Trash folder.

---

## Usage

Exisort has two main modes: `import` (ingesting new photos) and `clean` (fixing existing folders).

### 1. Import
Copy or move files from a source to a structured destination.

```bash
exisort import [flags] <source_dir> <destination_dir>
```

**Example:**
```bash
# Import from SD card, organize by Year/Month, and move files (delete from source)
exisort import --move --format "{year}/{month}/{year}{month}{day}_{hour}{min}{sec}.{ext}" /Volumes/EOS_DIGITAL ~/Photos
```

### 2. Clean
Scan a directory for duplicates and remove them.

```bash
exisort clean [flags] <target_dir>
```

**Example:**
```bash
# Find duplicates, keep the one with the shortest path, move duplicates to a trash folder
exisort clean --keep shortest-path --action trash ~/Photos
```

---

## Configuration & CLI Options

### Global Flags
These work with any command.
*   `-v, --verbose`: Enable detailed logging.
*   `--dry-run`: Print what would happen without making changes.

### Import Options
Control how files are named and where they go.

#### Naming Scheme
*   `-f, --format <string>`
    *   Defines the folder structure and filename.
    *   **Default:** `{year}/{year}-{month}/{year}{month}{day}_{hour}{min}{sec}.{ext}`
    *   **Tokens:**
        *   `{year}`, `{month}`, `{day}`: Date components.
        *   `{hour}`, `{min}`, `{sec}`: Time components.
        *   `{hash}`: Short hash of file
        *   `{filename}`: Original filename (excluding extension).
        *   `{ext}`: File extension.

#### Conflict Handling
What happens if the destination file already exists?
*   `--conflict <mode>`
    *   `rename` (Default): Calculate hash. If content matches, skip. If content differs, append `_1`, `_2`, etc.
    *   `skip`: Do not copy/move if the filename exists.
    *   `overwrite`: Replace the destination file (Use with caution).

#### File Filters & Modifiers
*   `-e, --extensions <list>`: Comma-separated list of extensions to process (e.g., `jpg,heic,mov`). Default includes common photo/video formats.
*   `--min-size <size>`: Ignore files smaller than this (e.g., `10kb`). Useful to skip thumbnails.
*   `--time-offset <duration>`: Shift EXIF timestamps. Useful if camera time was wrong. Example: `+1h`, `-30m`.
*   `--case <mode>`: Rename extensions. `lower` (default), `upper`, or `preserve`.
    `--mtime <mode>`: Update mtime of destination file. `yes` (default), `no`

#### Operation
*   `--move`: Delete source files after successful verification at destination.
*   `--verify`: Re-read the copied file from disk and compare hash with source before deleting original.

---

### Clean Options (Deduplication)

#### Detection
*   `--method <type>`
    *   `hash` (Default): strict content matching (SHA-256/XXHash).
    *   ... other methods not implemented yet

#### Resolution Strategy
When duplicates are found, which one should be kept?
*   `--keep <strategy>`
    *   `oldest`: Keep the file with the oldest file-system creation date.
    *   `shortest-path`: Keep the file closest to the root directory (e.g., keep `./Photos/img.jpg`, delete `./Photos/Backup/img.jpg`).
    *   `newest`: Keep the most recently modified file.

#### Actions
*   `--action <mode>`
    *   `report` (Default): Just list duplicates found.
    *   `trash`: Move duplicates to a specific folder (safest).
    *   `delete`: Permanently remove files.
    *   `hardlink`: Replace duplicates with filesystem hardlinks (saves space, keeps file visible).
*   `--trash-dir <path>`: Directory to move duplicates to when action is `trash`. Default: `./_Exisort_Trash`.

---

## Examples of Logic

### The "Smart Rename"
You are importing `IMG_001.JPG` (taken at 12:00:00).
Your format is `{year}{month}{day}_{hour}{min}{sec}.{ext}`.
Target: `20240101_120000.jpg`.

1.  **Scenario A:** You already imported this exact photo.
    *   Exisort sees target exists. Hashes match.
    *   Result: **Skipped** (Source deleted if `--move` is on).
2.  **Scenario B:** You took a burst shot. This is a *different* photo taken at the same second.
    *   Exisort sees target exists. Hashes differ.
    *   Result: Renames to `20240101_120000_1.jpg`.

### The "Safe Clean"
You have a messy backup folder inside your main library.
`~/Photos/2024/img.jpg` and `~/Photos/Backup/2024/img.jpg`.

Command:
`exisort clean --keep shortest-path --action trash ~/Photos`

1.  Exisort finds both files have identical hashes.
2.  "Shortest Path" logic selects `~/Photos/2024/img.jpg` as the **Keeper**.
3.  `~/Photos/Backup/2024/img.jpg` is moved to `~/Photos/_Exisort_Trash/Backup/2024/img.jpg`.
4.  You review the trash folder, then delete it when confident.

---

## Installation

```bash
go install github.com/levmv/exisort@latest
```
