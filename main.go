package main

import (
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
		b, err := MarshalSummary(rootAbs, dirStats, userStats, groupStats, startedAt, endedAt, msStart,
			dirsScanned.Load(), filesScanned.Load(), version)
		if err != nil {
			log.Fatalf("failed to build json: %v", err)
		}
		if cfg.JsonOut == "-" {
			fmt.Println(string(b))
		} else {
			if err := os.WriteFile(cfg.JsonOut, b, 0644); err != nil {
				log.Fatalf("failed to write json file: %v", err)
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
