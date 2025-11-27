diskusage - concurrent disk usage reporter

This is a small multithreaded disk-usage tool written in Go.

Build

```bash
cd /Users/kgn/coding/novo/diskusage
go build -o diskusage
```

Usage

```bash
# basic
./diskusage -root /path/to/dir -levels 2

# show file counts and owners
./diskusage -root /path/to/dir -levels 1 -files -user -group

# do not use human-readable sizes
./diskusage -root /path/to/dir -levels 1 -human=false

# tune concurrency
./diskusage -root /path -concurrency 16
```

Flags

- `-root` (string): root path to analyze (default `.`)
- `-levels` (int): number of directory levels to display. `0` prints only the root entry. Default: `2`.
- `-files` (bool): include number of files per directory
- `-user` (bool): show directory owner user (username)
- `-group` (bool): show directory owner group
- `-human` (bool): print human-readable sizes (default true)
- `-concurrency` (int): number of concurrent directory readers (defaults to 2 * CPU cores)

Output

The tool prints a small table with aggregated directory sizes (sums include all files in descendant directories). When `-files` is enabled it also prints file counts per directory. When `-user`/`-group` are enabled it prints owner information for each listed directory. Finally there are two summary sections: per-user and per-group totals (size and number of files).

Notes & limitations

- The tool reads filesystem metadata using lstat and does not follow symlinks for directories (it treats symlink entries as files).
- On platforms without `syscall.Stat_t` fields the UID/GID resolution may fall back to numeric IDs; on macOS and Linux it should work.
- Exclusions, depth-based aggregation filtering, JSON output, sorting by size, and parallel traversal improvements can be added if you want.

Next steps I can help with

- Add JSON or CSV output
- Add include/exclude patterns
- Add sorting or threshold filtering (e.g. show only dirs > 100MB)
- Add unit tests and CI

If you want any of these, tell me which and I'll implement it.

## Release & distribution (goreleaser)

This project includes a `.goreleaser.yml` to build cross-platform artifacts. Before using the release workflow, set the GitHub repo owner in `.goreleaser.yml` (already set to `kgn` in this repo; change if needed).

Quick commands:

```bash
# Run tests and build locally
make test
make build

# Create a local snapshot archive (no publish)
make goreleaser-snapshot

# Run a full release (requires a Git tag and GITHUB_TOKEN)
make goreleaser-release
```

Notes:
- On tags (v*), the GitHub Actions workflow will run goreleaser and publish releases.
- goreleaser embeds the Git tag into the binary via ldflags; the program exposes the embedded version via `-version` and in JSON output.

## JSON output

When using the `-json` flag the program emits a structured JSON object. The top-level `stats` object includes timing and memory metrics and also contains a `version` field with the embedded binary version (e.g. `"version": "v1.2.3"` or `"dev"` for local builds).
