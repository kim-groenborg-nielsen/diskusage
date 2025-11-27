package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// package-level version, populated via ldflags in releases (default 'dev')
var version = "dev"

type DirStat struct {
	Size  int64
	Files int64
}

type UserStat struct {
	Size  int64
	Files int64
}

type GroupStat struct {
	Size  int64
	Files int64
}

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

func main() {
	var (
		levels      = flag.Int("levels", 2, "number of directory levels to display (0 means only root)")
		showUser    = flag.Bool("user", false, "show directory owner user")
		showGroup   = flag.Bool("group", false, "show directory owner group")
		showFiles   = flag.Bool("files", false, "show number of files per directory")
		root        = flag.String("root", ".", "root path to analyze (can also be specified as first positional argument)")
		concurrency = flag.Int("concurrency", runtime.NumCPU()*2, "number of concurrent directory readers")
		bytesFlag   = flag.Bool("bytes", false, "print sizes in bytes instead of human-readable units")
		sizeWidth   = flag.Int("size-width", 0, "override size column width (0 = auto-fit)")
		filesWidth  = flag.Int("files-width", 0, "override files column width (0 = auto-fit)")
		topN        = flag.Int("top", 0, "limit per-user/group lists to top N by size (0 = all)")
		jsonOut     = flag.String("json", "", "write JSON summary to file (or '-' for stdout)")
		versionFlag = flag.Bool("version", false, "show version and exit")
	)

	// Custom usage text: show flags and emphasize that options must come before the positional root arg.
	flag.Usage = func() {
		_, _ = fmt.Fprintf(os.Stderr, "Usage: %s [options] <root>\n\n", os.Args[0])
		_, _ = fmt.Fprintln(os.Stderr, "Options:")
		flag.PrintDefaults()
		_, _ = fmt.Fprintln(os.Stderr, "\nNote: flags (options) must be specified before the positional <root> argument.")
		_, _ = fmt.Fprintln(os.Stderr, "Example:")
		_, _ = fmt.Fprintf(os.Stderr, "  %s -levels 3 -files -user -group -bytes /path/to/dir\n", os.Args[0])
	}

	flag.Parse()

	// If user asked for help via -h or --help anywhere, print usage and exit.
	for _, a := range os.Args[1:] {
		if a == "-h" || a == "--help" {
			flag.Usage()
			return
		}
	}

	// If user asked for version, print and exit
	if *versionFlag {
		fmt.Println(version)
		return
	}

	// If a positional argument is provided, use it as the root (allows `./diskusage <path>`)
	if flag.NArg() > 0 {
		// take first positional argument as root
		*root = flag.Arg(0)
	}

	// Note: options must come before the positional root argument. Do not accept flags after the path.

	rootAbs, err := filepath.Abs(*root)
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
	filesToProcess := make(chan string, *concurrency*8)
	var workerWg sync.WaitGroup

	// Stats maps with mutex
	var mu sync.Mutex
	dirStats := make(map[string]*DirStat) // key: relative path to root (".")
	userStats := make(map[string]*UserStat)
	groupStats := make(map[string]*GroupStat)

	// start workers that stat files and aggregate directly
	for i := 0; i < *concurrency; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
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
						dirStats[p] = &DirStat{}
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
					userStats[uname] = &UserStat{}
				}
				userStats[uname].Size += size
				userStats[uname].Files += 1
				if _, ok := groupStats[gname]; !ok {
					groupStats[gname] = &GroupStat{}
				}
				groupStats[gname].Size += size
				groupStats[gname].Files += 1
				mu.Unlock()
			}
		}()
	}

	// atomic counters for scanned items
	var filesScanned int64
	var dirsScanned int64

	// Walk directory tree in main goroutine and push file paths into filesToProcess
	err = filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// skip unreadable entries
			return nil
		}
		if d.IsDir() {
			atomic.AddInt64(&dirsScanned, 1)
			return nil
		}
		atomic.AddInt64(&filesScanned, 1)
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
	// copy sizes to use for sorting without holding the mutex later
	dirSizes := make(map[string]int64, len(dirStats))
	for k, v := range dirStats {
		dirSizes[k] = v.Size
	}
	mu.Unlock()

	// compute size strings and widths using helper (testable)
	sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth := ComputeSizeMapsAndWidths(dirSizes, dirStats, userStats, groupStats, *bytesFlag, *sizeWidth, *filesWidth)

	// If JSON output requested, build JSON structure and write it before human output
	if *jsonOut != "" {
		// compute ended/ runtime now
		endedAt := time.Now()
		runtimeSeconds := endedAt.Sub(startedAt).Seconds()
		runtimeStr := endedAt.Sub(startedAt).String()
		type JsonDir struct {
			Path  string `json:"path"`
			Rel   string `json:"rel"`
			Size  int64  `json:"size"`
			Files int64  `json:"files"`
			UID   uint32 `json:"uid,omitempty"`
			User  string `json:"user,omitempty"`
			GID   uint32 `json:"gid,omitempty"`
			Group string `json:"group,omitempty"`
		}
		type JsonUser struct {
			Name  string `json:"name"`
			Size  int64  `json:"size"`
			Files int64  `json:"files"`
			UID   uint32 `json:"uid,omitempty"`
		}
		type JsonGroup struct {
			Name  string `json:"name"`
			Size  int64  `json:"size"`
			Files int64  `json:"files"`
			GID   uint32 `json:"gid,omitempty"`
		}
		type JsonStats struct {
			StartedAt      string  `json:"started_at"`
			EndedAt        string  `json:"ended_at"`
			RuntimeSeconds float64 `json:"runtime_seconds"`
			Runtime        string  `json:"runtime"`
			DirsScanned    int64   `json:"dirs_scanned"`
			FilesScanned   int64   `json:"files_scanned"`
			// memory and GC stats
			MemAlloc           uint64  `json:"mem_alloc_bytes"`       // bytes allocated and still in use
			TotalAlloc         uint64  `json:"total_alloc_bytes"`     // cumulative bytes allocated for heap objects
			HeapAlloc          uint64  `json:"heap_alloc_bytes"`      // bytes allocated (heap)
			HeapSys            uint64  `json:"heap_sys_bytes"`        // bytes obtained from system for heap
			NumGC              uint32  `json:"num_gc"`                // number of completed GC cycles
			PauseTotalNs       uint64  `json:"pause_total_ns"`        // cumulative GC pause time
			LastGC             string  `json:"last_gc,omitempty"`     // time of last GC (RFC3339)
			GCCPUFraction      float64 `json:"gc_cpu_fraction"`       // fraction of CPU time used by GC
			HeapInuse          uint64  `json:"heap_inuse_bytes"`      // bytes in non-idle span in use
			HeapIdle           uint64  `json:"heap_idle_bytes"`       // bytes in idle spans
			HeapReleased       uint64  `json:"heap_released_bytes"`   // bytes released to OS
			NextGC             uint64  `json:"next_gc_bytes"`         // target heap size for next GC
			LastPauseNs        uint64  `json:"last_pause_ns"`         // most recent GC pause duration
			MaxPauseNs         uint64  `json:"max_pause_ns"`          // max pause observed in buffer
			PeakAllocBytes     uint64  `json:"peak_alloc_bytes"`      // peak bytes allocated and still in use
			PeakHeapAllocBytes uint64  `json:"peak_heap_alloc_bytes"` // peak bytes allocated (heap)
			Version            string  `json:"version"`               // embedded program version
		}
		type JsonOut struct {
			Root  string      `json:"root"`
			Stats JsonStats   `json:"stats"`
			Dirs  []JsonDir   `json:"dirs"`
			Users []JsonUser  `json:"users"`
			Grps  []JsonGroup `json:"groups"`
		}

		// collect memory stats
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)

		// format last GC time if present and compute last/max pause from circular buffer
		lastGC := ""
		if ms.LastGC != 0 {
			lastGC = time.Unix(0, int64(ms.LastGC)).Format(time.RFC3339)
		}
		// compute last and max pause from PauseNs circular buffer
		var lastPause uint64
		var maxPause uint64
		if ms.NumGC > 0 {
			count := int(ms.NumGC)
			if count > len(ms.PauseNs) {
				count = len(ms.PauseNs)
			}
			for i := 0; i < count; i++ {
				idx := (int(ms.NumGC) - 1 - i) % len(ms.PauseNs)
				p := ms.PauseNs[idx]
				if i == 0 {
					lastPause = p
				}
				if p > maxPause {
					maxPause = p
				}
			}
		}

		// compute peak memory estimates (start vs end)
		peakAlloc := ms.Alloc
		if msStart.Alloc > peakAlloc {
			peakAlloc = msStart.Alloc
		}
		peakHeapAlloc := ms.HeapAlloc
		if msStart.HeapAlloc > peakHeapAlloc {
			peakHeapAlloc = msStart.HeapAlloc
		}

		jo := JsonOut{
			Root: rootAbs,
			Stats: JsonStats{
				StartedAt:          startedAt.Format(time.RFC3339),
				EndedAt:            endedAt.Format(time.RFC3339),
				RuntimeSeconds:     runtimeSeconds,
				Runtime:            runtimeStr,
				DirsScanned:        atomic.LoadInt64(&dirsScanned),
				FilesScanned:       atomic.LoadInt64(&filesScanned),
				MemAlloc:           ms.Alloc,
				TotalAlloc:         ms.TotalAlloc,
				HeapAlloc:          ms.HeapAlloc,
				HeapSys:            ms.HeapSys,
				PeakAllocBytes:     peakAlloc,
				PeakHeapAllocBytes: peakHeapAlloc,
				HeapInuse:          ms.HeapInuse,
				HeapIdle:           ms.HeapIdle,
				HeapReleased:       ms.HeapReleased,
				NumGC:              ms.NumGC,
				PauseTotalNs:       ms.PauseTotalNs,
				LastGC:             lastGC,
				LastPauseNs:        lastPause,
				MaxPauseNs:         maxPause,
				GCCPUFraction:      ms.GCCPUFraction,
				NextGC:             ms.NextGC,
				Version:            version,
			},
		}

		// collect directories
		mu.Lock()
		for rel, ds := range dirStats {
			abs := rootAbs
			if rel != "." {
				abs = filepath.Join(rootAbs, rel)
			}
			var uid uint32
			var gid uint32
			var uname, gname string
			if info, err := os.Lstat(abs); err == nil {
				if st, ok := info.Sys().(*syscall.Stat_t); ok {
					uid = st.Uid
					gid = st.Gid
					uidStr := strconv.FormatUint(uint64(uid), 10)
					gidStr := strconv.FormatUint(uint64(gid), 10)
					// always attempt to resolve names when possible
					if uEnt, err := user.LookupId(uidStr); err == nil {
						uname = uEnt.Username
					} else {
						// if lookup fails, leave uname empty
					}
					if gEnt, err := user.LookupGroupId(gidStr); err == nil {
						gname = gEnt.Name
					} else {
						// leave gname empty
					}
				}
			}
			jo.Dirs = append(jo.Dirs, JsonDir{
				Path:  abs,
				Rel:   rel,
				Size:  ds.Size,
				Files: ds.Files,
				UID:   uid,
				GID:   gid,
				User:  uname,
				Group: gname,
			})
		}

		// collect users
		for u, us := range userStats {
			// resolve username and UID if possible. The key `u` may be a username or a numeric uid string.
			resolvedName := u
			var uidNum uint32
			// try lookup by name
			if ent, err := user.Lookup(u); err == nil {
				resolvedName = ent.Username
				if v, err := strconv.ParseUint(ent.Uid, 10, 32); err == nil {
					uidNum = uint32(v)
				}
			} else {
				// try lookup by id (in case key is numeric string)
				if ent, err := user.LookupId(u); err == nil {
					resolvedName = ent.Username
					if v, err := strconv.ParseUint(ent.Uid, 10, 32); err == nil {
						uidNum = uint32(v)
					}
				} else {
					// as a fallback, parse numeric uid if possible
					if v, err := strconv.ParseUint(u, 10, 32); err == nil {
						uidNum = uint32(v)
					}
				}
			}
			jo.Users = append(jo.Users, JsonUser{Name: resolvedName, Size: us.Size, Files: us.Files, UID: uidNum})
		}
		// collect groups
		for g, gs := range groupStats {
			resolvedGroup := g
			var gidNum uint32
			// try lookup by name
			if ent, err := user.LookupGroup(g); err == nil {
				resolvedGroup = ent.Name
				if v, err := strconv.ParseUint(ent.Gid, 10, 32); err == nil {
					gidNum = uint32(v)
				}
			} else {
				// try lookup by id
				if ent, err := user.LookupGroupId(g); err == nil {
					resolvedGroup = ent.Name
					if v, err := strconv.ParseUint(ent.Gid, 10, 32); err == nil {
						gidNum = uint32(v)
					}
				} else {
					// fallback: parse numeric
					if v, err := strconv.ParseUint(g, 10, 32); err == nil {
						gidNum = uint32(v)
					}
				}
			}
			jo.Grps = append(jo.Grps, JsonGroup{Name: resolvedGroup, Size: gs.Size, Files: gs.Files, GID: gidNum})
		}
		mu.Unlock()

		// sort entries for deterministic output
		sort.Slice(jo.Dirs, func(i, j int) bool { return jo.Dirs[i].Path < jo.Dirs[j].Path })
		sort.Slice(jo.Users, func(i, j int) bool { return jo.Users[i].Name < jo.Users[j].Name })
		sort.Slice(jo.Grps, func(i, j int) bool { return jo.Grps[i].Name < jo.Grps[j].Name })

		b, err := json.MarshalIndent(jo, "", "  ")
		if err != nil {
			log.Fatalf("failed to marshal json: %v", err)
		}
		if *jsonOut == "-" {
			fmt.Println(string(b))
		} else {
			if err := os.WriteFile(*jsonOut, b, 0644); err != nil {
				log.Fatalf("failed to write json file: %v", err)
			}
		}
		// exit immediately after JSON output
		return
	}

	// sort children lists by descending total size (fallback to name) -- existing
	for k := range children {
		s := children[k]
		sort.Slice(s, func(i, j int) bool {
			si := dirSizes[s[i]]
			sj := dirSizes[s[j]]
			if si == sj {
				return s[i] < s[j]
			}
			return si > sj
		})
		children[k] = s
	}

	// printing header - build columns consistently (size, files, user, group, path)
	headerCols := []interface{}{}
	headerFmt := fmt.Sprintf("%%%ds", maxSizeWidth) // size right-aligned
	headerCols = append(headerCols, "Size")
	if *showFiles {
		headerFmt += " %" + strconv.Itoa(maxFilesWidth) + "s"
		headerCols = append(headerCols, "Files")
	}
	if *showUser {
		headerFmt += " %-15s"
		headerCols = append(headerCols, "User")
	}
	if *showGroup {
		headerFmt += " %-15s"
		headerCols = append(headerCols, "Group")
	}
	headerFmt += " %s\n"
	headerCols = append(headerCols, "Path")
	fmt.Printf(headerFmt, headerCols...)

	var printDirRec func(pathRel string, curLevel int, prefix string, isLast bool)
	printDirRec = func(pathRel string, curLevel int, prefix string, isLast bool) {
		mu.Lock()
		stat := dirStats[pathRel]
		mu.Unlock()
		// get combined size string for this path
		sizeCombined := "0"
		if val, ok := sizeStrMap[pathRel]; ok {
			sizeCombined = val
		} else if stat != nil {
			if *bytesFlag {
				sizeCombined = strconv.FormatInt(stat.Size, 10)
			} else {
				sizeCombined = humanizeBytes(stat.Size)
			}
		}
		filesStr := ""
		if *showFiles {
			if stat != nil {
				filesStr = strconv.FormatInt(stat.Files, 10)
			} else {
				filesStr = "0"
			}
		}

		userStr := ""
		groupStr := ""
		if *showUser || *showGroup {
			// attempt to stat the directory to get owner
			full := rootAbs
			if pathRel != "." {
				full = filepath.Join(rootAbs, pathRel)
			}
			if info, err := os.Lstat(full); err == nil {
				if st, ok := info.Sys().(*syscall.Stat_t); ok {
					uidStr := strconv.FormatUint(uint64(st.Uid), 10)
					gidStr := strconv.FormatUint(uint64(st.Gid), 10)
					if *showUser {
						if u, err := user.LookupId(uidStr); err == nil {
							userStr = u.Username
						} else {
							userStr = uidStr
						}
					}
					if *showGroup {
						if g, err := user.LookupGroupId(gidStr); err == nil {
							groupStr = g.Name
						} else {
							groupStr = gidStr
						}
					}
				}
			}
		}

		// Build tree-style name with connectors
		var name string
		if curLevel == 0 {
			// root: show absolute path
			name = rootAbs
		} else {
			connector := ""
			if isLast {
				connector = "└── "
			} else {
				connector = "├── "
			}
			name = prefix + connector + filepath.Base(pathRel)
		}

		// print line with combined size, files, user/group, path
		fmtStr := fmt.Sprintf("%%%ds", maxSizeWidth)
		args := []interface{}{sizeCombined}
		if *showFiles {
			fmtStr += " %" + strconv.Itoa(maxFilesWidth) + "s"
			args = append(args, filesStr)
		}
		if *showUser {
			fmtStr += " %-15s"
			args = append(args, userStr)
		}
		if *showGroup {
			fmtStr += " %-15s"
			args = append(args, groupStr)
		}
		fmtStr += " %s\n"
		args = append(args, name)
		fmt.Printf(fmtStr, args...)

		if curLevel >= *levels {
			return
		}

		// iterate children
		mu.Lock()
		kids := children[pathRel]
		mu.Unlock()
		for i, k := range kids {
			last := i == len(kids)-1
			// build child's prefix: if current node is last, use spaces; otherwise use vertical line
			childPrefix := prefix
			if curLevel >= 0 {
				if isLast {
					childPrefix += "    "
				} else {
					childPrefix += "│   "
				}
			}
			printDirRec(k, curLevel+1, childPrefix, last)
		}
	}

	// ensure root exists in dirStats
	mu.Lock()
	if _, ok := dirStats["."]; !ok {
		dirStats["."] = &DirStat{}
	}
	mu.Unlock()

	printDirRec(".", 0, "", true)

	// print per-user summary
	fmt.Println()
	fmt.Println("Per-user summary:")
	mu.Lock()
	userNames := make([]string, 0, len(userStats))
	for u := range userStats {
		userNames = append(userNames, u)
	}
	mu.Unlock()
	sort.Slice(userNames, func(i, j int) bool { return userStats[userNames[i]].Size > userStats[userNames[j]].Size })
	if *topN > 0 && *topN < len(userNames) {
		userNames = userNames[:*topN]
	}
	for _, u := range userNames {
		mu.Lock()
		s := userStats[u]
		mu.Unlock()
		// combined user size string
		sizeCombined := "0"
		if val, ok := userSizeStr[u]; ok {
			sizeCombined = val
		} else if s != nil {
			if *bytesFlag {
				sizeCombined = strconv.FormatInt(s.Size, 10)
			} else {
				sizeCombined = humanizeBytes(s.Size)
			}
		}
		filesCount := int64(0)
		if s != nil {
			filesCount = s.Files
		}
		fmt.Printf("%-20s %"+strconv.Itoa(maxSizeWidth)+"s %"+strconv.Itoa(maxFilesWidth)+"d files\n", u, sizeCombined, filesCount)
	}

	// print per-group summary
	fmt.Println()
	fmt.Println("Per-group summary:")
	mu.Lock()
	groupNames := make([]string, 0, len(groupStats))
	for g := range groupStats {
		groupNames = append(groupNames, g)
	}
	mu.Unlock()
	sort.Slice(groupNames, func(i, j int) bool { return groupStats[groupNames[i]].Size > groupStats[groupNames[j]].Size })
	if *topN > 0 && *topN < len(groupNames) {
		groupNames = groupNames[:*topN]
	}
	for _, g := range groupNames {
		mu.Lock()
		s := groupStats[g]
		mu.Unlock()
		// combined group size string
		sizeCombined := "0"
		if val, ok := groupSizeStr[g]; ok {
			sizeCombined = val
		} else if s != nil {
			if *bytesFlag {
				sizeCombined = strconv.FormatInt(s.Size, 10)
			} else {
				sizeCombined = humanizeBytes(s.Size)
			}
		}
		filesCount := int64(0)
		if s != nil {
			filesCount = s.Files
		}
		fmt.Printf("%-20s %"+strconv.Itoa(maxSizeWidth)+"s %"+strconv.Itoa(maxFilesWidth)+"d files\n", g, sizeCombined, filesCount)
	}
}
