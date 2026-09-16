package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// package-level version, populated via ldflags in releases (default 'dev')
var version = "dev"
var commit = "none"
var date = "unknown"

type Stat struct {
	Size  int64
	Files int64
}

type StatMap map[string]*Stat

func humanizeBytes(s int64) string {
	if s < 0 {
		return "-"
	}
	const unit = 1024
	if s < unit {
		return fmt.Sprintf("%dB", s)
	}
	d := float64(s)
	div := float64(unit)
	for _, suffix := range []string{"KB", "MB", "GB", "TB", "PB", "EB"} {
		d = d / div
		if d < div {
			return fmt.Sprintf("%.1f%s", d, suffix)
		}
	}
	return fmt.Sprintf("%dB", s)
}

// ComputeSizeMapsAndWidths is defined in format.go; helper removed here.

// buildChildrenAndSizes builds the children map and dirSizes map from dirStats.
func buildChildrenAndSizes(dirStats map[string]*Stat) (map[string][]string, map[string]int64) {
	children := make(map[string][]string)
	for p := range dirStats {
		if p == "." {
			children["."] = children["."] // ensure key exists
			continue
		}
		parent := filepath.Dir(p)
		if parent == "" {
			parent = "."
		}
		children[parent] = append(children[parent], p)
		if _, ok := children[p]; !ok {
			children[p] = []string{}
		}
	}
	dirSizes := make(map[string]int64, len(dirStats))
	for k, v := range dirStats {
		dirSizes[k] = v.Size
	}
	return children, dirSizes
}

func addGzExt(p string) string {
	if strings.HasSuffix(strings.ToLower(p), ".gz") {
		return p
	}
	return p + ".gz"
}

func main() {
	cfg := GetConfig()

	if cfg.Environment == "development" && cfg.Profile != "" {
		f, err := os.Create(cfg.Profile)
		if err != nil {
			log.Fatalf("failed to create profile file: %v", err)
		}
		defer func() {
			_ = f.Close()
		}()
		err = pprof.StartCPUProfile(f)
		if err != nil {
			log.Fatalf("failed to start CPU profile: %v", err)
		}
		defer pprof.StopCPUProfile()
	}

	// Shared variables for scanning and read-json mode
	var (
		rootAbs       string
		children      map[string][]string
		dirStats      StatMap
		userStats     StatMap
		groupStats    StatMap
		dirSizes      map[string]int64
		sizeStrMap    map[string]string
		userSizeStr   map[string]string
		groupSizeStr  map[string]string
		maxSizeWidth  int
		maxFilesWidth int
		readMode      bool
		readOwners    map[string]string
		readGroups    map[string]string
	)

	// If read-json was provided, load file and prepare data structures for printing, then jump to printing
	if cfg.ReadJson != "" {
		// read JSON (allow '-' for stdin)
		jo, err := LoadSummary(cfg.ReadJson)
		if err != nil {
			log.Fatalf("failed to load json: %v", err)
		}

		// build maps from jo
		dirStats = make(StatMap)
		userStats = make(StatMap)
		groupStats = make(StatMap)
		ownerByRel := make(map[string]string)
		groupByRel := make(map[string]string)

		for _, d := range jo.Dirs {
			rel := d.Rel
			if rel == "" {
				rel = "."
			}
			dirStats[rel] = &Stat{Size: d.Size, Files: d.Files}
			ownerByRel[rel] = d.User
			groupByRel[rel] = d.Group
		}

		for _, u := range jo.Users {
			userStats[u.Name] = &Stat{Size: u.Size, Files: u.Files}
		}
		for _, g := range jo.Grps {
			groupStats[g.Name] = &Stat{Size: g.Size, Files: g.Files}
		}

		if jo.Root != "" {
			rootAbs = filepath.Clean(jo.Root)
		} else {
			rootAbs = "."
		}

		children, dirSizes = buildChildrenAndSizes(dirStats)
		sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth = ComputeSizeMapsAndWidths(dirSizes, dirStats,
			userStats, groupStats, cfg.BytesFlag, cfg.SizeWidth, cfg.FilesWidth)
		readMode = true
		readOwners = ownerByRel
		readGroups = groupByRel
		printTree(rootAbs, children, dirStats, userStats, groupStats, sizeStrMap, userSizeStr, groupSizeStr,
			maxSizeWidth, maxFilesWidth, cfg.Levels, cfg.ShowFiles, cfg.ShowUser, cfg.ShowGroup, cfg.BytesFlag,
			cfg.TopN, readMode, readOwners, readGroups)
		return
	}

	// If a positional argument is provided, use it as the root (allows `./diskusage <path>`)
	if flag.NArg() > 0 {
		// take first positional argument as root
		cfg.Root = flag.Arg(0)
	}

	// Note: options must come before the positional root argument. Do not accept flags after the path.

	rootAbs, err := filepath.Abs(cfg.Root)
	if err != nil {
		log.Fatalf("failed to resolve root path: %v", err)
	}
	rootAbs = filepath.Clean(rootAbs)

	// record start time for runtime measurement
	startedAt := time.Now()

	// take initial memory snapshot to help estimate peak memory during run
	var msStart runtime.MemStats
	runtime.ReadMemStats(&msStart)

	// channel of file paths to process and worker waitgroup
	filesToProcess := make(chan string, cfg.Concurrency*8)
	var workerWg sync.WaitGroup

	// Stats maps with mutex
	var mu sync.Mutex
	dirStats = make(StatMap) // key: relative path to root (".")
	userStats = make(StatMap)
	groupStats = make(StatMap)

	// start workers that stat files and aggregate directly
	for i := 0; i < cfg.Concurrency; i++ {
		workerWg.Go(func() {
			for path := range filesToProcess {
				info, err := os.Lstat(path)
				if err != nil {
					continue
				}
				// get size and owner
				size := info.Size()
				var uid uint32
				var gid uint32
				if st, ok := info.Sys().(*syscall.Stat_t); ok {
					uid = st.Uid
					gid = st.Gid
				}

				// compute relative directory path
				fileDir := filepath.Dir(path)
				rel, err := filepath.Rel(rootAbs, fileDir)
				if err != nil {
					rel = fileDir
				}
				if rel == "" {
					rel = "."
				}

				// aggregate into dirStats and user/group maps
				mu.Lock()
				p := rel
				for {
					if _, ok := dirStats[p]; !ok {
						dirStats[p] = &Stat{}
					}
					dirStats[p].Size += size
					dirStats[p].Files += 1
					if p == "." {
						break
					}
					p = filepath.Dir(p)
				}

				uidStr := strconv.FormatUint(uint64(uid), 10)
				gidStr := strconv.FormatUint(uint64(gid), 10)
				var uname, gname string
				if u, err := user.LookupId(uidStr); err == nil {
					uname = u.Username
				} else {
					uname = uidStr
				}
				if g, err := user.LookupGroupId(gidStr); err == nil {
					gname = g.Name
				} else {
					gname = gidStr
				}
				if _, ok := userStats[uname]; !ok {
					userStats[uname] = &Stat{}
				}
				userStats[uname].Size += size
				userStats[uname].Files += 1
				if _, ok := groupStats[gname]; !ok {
					groupStats[gname] = &Stat{}
				}
				groupStats[gname].Size += size
				groupStats[gname].Files += 1
				mu.Unlock()
			}
		})
	}

	// atomic counters for scanned items
	var filesScanned atomic.Int64
	var dirsScanned atomic.Int64

	// progress done channel for ticker goroutine; always created to simplify closing
	done := make(chan struct{})
	if cfg.ProgressFlag {
		// start progress ticker that prints a concise, human-friendly status line (single-line)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			var lastFiles int64
			for {
				select {
				case <-ticker.C:
					//fscnt := atomic.LoadInt64(&filesScanned)
					fscnt := filesScanned.Load()
					//dirs := atomic.LoadInt64(&dirsScanned)
					dirs := dirsScanned.Load()
					var m runtime.MemStats
					runtime.ReadMemStats(&m)
					elapsed := time.Since(startedAt)
					// instant rate over last interval (files per second)
					intervalSec := 2.0
					rate := float64(fscnt-lastFiles) / intervalSec
					lastFiles = fscnt
					// single-line status (overwrites itself); avoid printing goroutine count or delta
					memStr := humanizeBytes(int64(m.Alloc))
					status := fmt.Sprintf("files=%d | %.1f/s | dirs=%d | mem=%s | %s", fscnt, rate, dirs, memStr, formatDurationShort(elapsed))
					// pad with spaces to clear previous content and use \r to overwrite
					_, _ = fmt.Fprintf(os.Stderr, "\r%s", status+"                                        ")
				case <-done:
					// stop without printing a newline; final will overwrite the line
					return
				}
			}
		}()
	}

	// Walk directory tree in main goroutine and push file paths into filesToProcess
	err = filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, err error) error {
		// Let walkdir handle d.info()
		// for files do:   info, err := d.Info()   if err==nil {     if st, ok := info.Sys().(syscall.Stat_t); ok { send fileJob{filepath.Dir(path), info.Size(), st.Uid, st.Gid} to channel }   }
		//•
		//Worker consumes fileJob (no os.Lstat call), aggregates sizes.
		// And cache user.LookupId/LookupGroupId in sync.Map to avoid repeated lookups for same uid/gid.

		if err != nil {
			// skip unreadable entries
			return nil
		}
		if d.IsDir() {
			dirsScanned.Add(1)
			return nil
		}
		filesScanned.Add(1)
		filesToProcess <- path
		return nil
	})
	if err != nil {
		log.Printf("walk error: %v", err)
	}

	// finished enqueuing paths; close and wait for workers
	close(filesToProcess)
	workerWg.Wait()

	// stop progress ticker and print final progress if progress flag enabled
	// closing done signals the goroutine to exit; safe to close even if goroutine not started
	close(done)
	if cfg.ProgressFlag {
		fscnt := filesScanned.Load()
		dirs := dirsScanned.Load()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		elapsed := time.Since(startedAt)
		var avg float64
		if elapsed.Seconds() > 0 {
			avg = float64(fscnt) / elapsed.Seconds()
		}
		// final summary printed on its own line, overwrite previous progress with \r
		memStr := humanizeBytes(int64(m.Alloc))
		final := fmt.Sprintf("final: files=%d avg=%.1f/s dirs=%d mem=%s elapsed=%s", fscnt, avg, dirs, memStr, formatDurationShort(elapsed))
		_, _ = fmt.Fprintf(os.Stderr, "\r%s\n", final)
	}

	// Build children map for printing
	mu.Lock()
	children, dirSizes = buildChildrenAndSizes(dirStats)
	mu.Unlock()

	// compute size strings and widths using helper (testable)
	sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth = ComputeSizeMapsAndWidths(dirSizes, dirStats,
		userStats, groupStats, cfg.BytesFlag, cfg.SizeWidth, cfg.FilesWidth)

	// If JSON output requested, build JSON structure and write it before human output
	if cfg.JsonOut != "" {
		// compute ended/ runtime now
		endedAt := time.Now()
		// enable progress messages inside jsonio when user requested progress
		ProgressEnabled = cfg.ProgressFlag
		if ProgressEnabled {
			progressf("building JSON summary (this may take a moment)")
		}
		// If gzip requested, stream directly into gzip.Writer to avoid building large in-memory []byte
		if cfg.GzipFlag {
			if cfg.JsonOut == "-" {
				if ProgressEnabled {
					progressf("streaming gzipped JSON to stdout (-)")
				}
				gw := gzip.NewWriter(os.Stdout)
				if err := StreamSummary(gw, rootAbs, dirStats, userStats, groupStats, startedAt, endedAt, msStart,
					dirsScanned.Load(), filesScanned.Load(), version); err != nil {
					_ = gw.Close()
					log.Fatalf("failed to stream gzipped json to stdout: %v", err)
				}
				if err := gw.Close(); err != nil {
					log.Fatalf("failed to close gzip writer: %v", err)
				}
				if ProgressEnabled {
					progressf("finished streaming gzipped JSON to stdout")
				}
			} else {
				outPath := addGzExt(cfg.JsonOut)
				if ProgressEnabled {
					progressf("streaming gzipped JSON to %s", outPath)
				}
				f, err := os.Create(outPath)
				if err != nil {
					log.Fatalf("failed to create output file %s: %v", outPath, err)
				}
				gw := gzip.NewWriter(f)
				if err := StreamSummary(gw, rootAbs, dirStats, userStats, groupStats, startedAt, endedAt, msStart,
					dirsScanned.Load(), filesScanned.Load(), version); err != nil {
					_ = gw.Close()
					_ = f.Close()
					log.Fatalf("failed to stream gzipped json to %s: %v", outPath, err)
				}
				if err := gw.Close(); err != nil {
					_ = f.Close()
					log.Fatalf("failed to close gzip writer: %v", err)
				}
				// stat file for size
				st, _ := f.Stat()
				if err := f.Close(); err != nil {
					log.Fatalf("failed to close output file: %v", err)
				}
				if ProgressEnabled {
					progressf("finished writing gzipped JSON to %s, %d bytes", outPath, st.Size())
				}
			}
			return
		}
		// non-gzip path: build bytes and write (existing behavior)
		b, err := MarshalSummary(rootAbs, dirStats, userStats, groupStats, startedAt, endedAt, msStart,
			dirsScanned.Load(), filesScanned.Load(), version)
		if err != nil {
			log.Fatalf("failed to build json: %v", err)
		}
		if cfg.JsonOut == "-" {
			if ProgressEnabled {
				progressf("writing JSON to stdout (-)")
			}
			fmt.Println(string(b))
			if ProgressEnabled {
				progressf("finished writing JSON to stdout, %d bytes", len(b))
			}
		} else {
			outPath := cfg.JsonOut
			if ProgressEnabled {
				progressf("writing JSON to %s", outPath)
			}
			if err := os.WriteFile(outPath, b, 0644); err != nil {
				log.Fatalf("failed to write json file: %v", err)
			}
			if ProgressEnabled {
				progressf("finished writing JSON to %s, %d bytes", outPath, len(b))
			}
		}
		return
	}

	// print tree and summaries
	printTree(rootAbs, children, dirStats, userStats, groupStats, sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth,
		maxFilesWidth, cfg.Levels, cfg.ShowFiles, cfg.ShowUser, cfg.ShowGroup, cfg.BytesFlag, cfg.TopN, readMode,
		readOwners, readGroups)
	return
}

// formatDurationShort returns a compact HH:MM:SS-like string for durations
func formatDurationShort(d time.Duration) string {
	s := int(d.Seconds())
	h := s / 3600
	s -= h * 3600
	m := s / 60
	s -= m * 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
