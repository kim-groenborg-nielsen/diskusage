diskusage - concurrent disk usage reporter

A small multithreaded disk-usage tool written in Go. It scans a directory tree concurrently and prints aggregated directory sizes, optional file counts and ownership summaries. It can also emit a structured JSON summary (optionally gzipped) and read such JSON back to re-render the tree without scanning.

Build

```bash
cd /Users/kgn/coding/novo/diskusage
go build -o diskusage
```

Quick usage notes

- Flags (options) must come before any positional `root` argument.
- You can provide the root directory either with `-root <path>` or as the single positional argument after flags: `./diskusage [flags] <root>`.

Examples

```bash
# Scan /Users/me/projects and show 2 levels (default)
./diskusage /Users/me/projects

# Specify levels and show file counts + owners
./diskusage -levels 1 -files -user -group /Users/me/projects

# Write JSON summary to a file (plain)
./diskusage -levels 3 -files -user -group -json out.json /Users/me/projects

# Write gzipped JSON (auto-append .gz if missing)
./diskusage -json out.json -gzip /Users/me/projects   # writes out.json.gz

# Write gzipped JSON to stdout
./diskusage -json - -gzip /Users/me/projects > out.json.gz

# Read a JSON summary from file and render tree (no scanning)
./diskusage -read-json out.json
# Read gzipped JSON automatically detected and rendered
./diskusage -read-json out.json.gz
# Read JSON from stdin
cat out.json | ./diskusage -read-json -

# Show progress while scanning and building JSON
./diskusage -progress -json out.json /Users/me/projects
```

Flags

- `-levels` int
  - Number of directory levels to display (0 means only the root). Default: 2.
- `-files` bool
  - Show number of files per directory in the tree output.
- `-user` bool
  - Show directory owner user (resolved to username when possible).
- `-group` bool
  - Show directory owner group (resolved to group name when possible).
- `-root` string
  - Root path to analyze (can also be specified as the single positional argument). Default: `.`
- `-concurrency` int
  - Number of concurrent directory readers (default: 2 * CPU cores).
- `-bytes` bool
  - Print sizes in raw bytes instead of human-readable units (KB/MB/etc.). Default: human-readable units.
- `-size-width` int
  - Override size column width (0 = auto-fit).
- `-files-width` int
  - Override files column width (0 = auto-fit).
- `-top` int
  - Limit per-user/group lists to the top N entries by size (0 = all).
- `-json` string
  - Write JSON summary to file (or `-` for stdout). When writing to a file you can also pass `-gzip` to compress.
- `-gzip` bool
  - When used with `-json`, compress the JSON output with gzip. If writing to a file and the filename does not end with `.gz`, the program will append `.gz` automatically.
- `-read-json` string
  - Read a JSON summary from file (or `-` for stdin) and render the human tree without scanning. `LoadSummary` will auto-detect gzip-compressed input.
- `-progress` bool
  - Print periodic progress status (processed files/dirs, throughput, memory, elapsed time) while scanning and while building/writing JSON.
- `-version` bool
  - Print embedded version/commit/date and exit.

Behavior & output

- The printed tree shows directories in descending total size order (sums include all files in descendant directories).
- Numeric size and file count columns are right-aligned for easy scanning. Human-readable sizes include a unit suffix; the `-bytes` flag prints raw bytes.
- The `-json` output contains a `stats` object with start/end times, runtime (seconds and human duration), memory/GC info and the embedded binary version.
- When writing JSON with `-gzip`, the program streams JSON into a gzip writer to keep peak memory low.
- `-read-json` accepts plain or gzipped JSON (the program auto-detects gzip) and will decode the JSON using a streaming decoder.

Notes & limitations

- The program uses lstat and does not follow directory symlinks; symlink entries are treated as files.
- UID/GID resolution to names happens when possible; JSON output includes resolved user/group names and numeric ids when resolvable.
- For very large trees the program streams JSON during output to reduce peak memory, but it still builds internal slices for deterministic sorting. If you need to process extremely large datasets with minimal memory, consider a streaming/partitioned approach.

License

This repository is licensed under the MIT License (see `LICENSE`).
