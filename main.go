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
	"sort"
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

// printTree renders the directory tree and per-user/group summaries.
func printTree(rootAbs string, children map[string][]string, dirStats map[string]*DirStat, userStats map[string]*UserStat, groupStats map[string]*GroupStat, sizeStrMap, userSizeStr, groupSizeStr map[string]string, maxSizeWidth, maxFilesWidth int, levels int, showFiles, showUser, showGroup, bytesFlag bool, topN int, readMode bool, readOwners, readGroups map[string]string) {
	// copy dirSizes from dirStats
	dirSizes := make(map[string]int64, len(dirStats))
	for k, v := range dirStats {
		dirSizes[k] = v.Size
	}

	// sort children lists by descending total size (fallback to name)
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

	// printing header
	headerCols := []interface{}{}
	headerFmt := fmt.Sprintf("%%%ds", maxSizeWidth)
	headerCols = append(headerCols, "Size")
	if showFiles {
		headerFmt += " %" + strconv.Itoa(maxFilesWidth) + "s"
		headerCols = append(headerCols, "Files")
	}
	if showUser {
		headerFmt += " %-15s"
		headerCols = append(headerCols, "User")
	}
	if showGroup {
		headerFmt += " %-15s"
		headerCols = append(headerCols, "Group")
	}
	headerFmt += " %s\n"
	headerCols = append(headerCols, "Path")
	fmt.Printf(headerFmt, headerCols...)

	var printDirRec func(pathRel string, curLevel int, prefix string, isLast bool)
	printDirRec = func(pathRel string, curLevel int, prefix string, isLast bool) {
		stat := dirStats[pathRel]
		// size string
		sizeCombined := "0"
		if val, ok := sizeStrMap[pathRel]; ok {
			sizeCombined = val
		} else if stat != nil {
			if bytesFlag {
				sizeCombined = strconv.FormatInt(stat.Size, 10)
			} else {
				sizeCombined = humanizeBytes(stat.Size)
			}
		}
		filesStr := ""
		if showFiles {
			if stat != nil {
				filesStr = strconv.FormatInt(stat.Files, 10)
			} else {
				filesStr = "0"
			}
		}

		userStr := ""
		groupStr := ""
		if showUser || showGroup {
			if readMode {
				if showUser {
					if v, ok := readOwners[pathRel]; ok {
						userStr = v
					}
				}
				if showGroup {
					if v, ok := readGroups[pathRel]; ok {
						groupStr = v
					}
				}
			} else {
				full := rootAbs
				if pathRel != "." {
					full = filepath.Join(rootAbs, pathRel)
				}
				if info, err := os.Lstat(full); err == nil {
					if st, ok := info.Sys().(*syscall.Stat_t); ok {
						uidStr := strconv.FormatUint(uint64(st.Uid), 10)
						gidStr := strconv.FormatUint(uint64(st.Gid), 10)
						if showUser {
							if u, err := user.LookupId(uidStr); err == nil {
								userStr = u.Username
							} else {
								userStr = uidStr
							}
						}
						if showGroup {
							if g, err := user.LookupGroupId(gidStr); err == nil {
								groupStr = g.Name
							} else {
								groupStr = gidStr
							}
						}
					}
				}
			}
		}

		var name string
		if curLevel == 0 {
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

		fmtStr := fmt.Sprintf("%%%ds", maxSizeWidth)
		args := []interface{}{sizeCombined}
		if showFiles {
			fmtStr += " %" + strconv.Itoa(maxFilesWidth) + "s"
			args = append(args, filesStr)
		}
		if showUser {
			fmtStr += " %-15s"
			args = append(args, userStr)
		}
		if showGroup {
			fmtStr += " %-15s"
			args = append(args, groupStr)
		}
		fmtStr += " %s\n"
		args = append(args, name)
		fmt.Printf(fmtStr, args...)

		if curLevel >= levels {
			return
		}

		kids := children[pathRel]
		for i, k := range kids {
			last := i == len(kids)-1
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

	if _, ok := dirStats["."]; !ok {
		dirStats["."] = &DirStat{}
	}

	printDirRec(".", 0, "", true)

	// per-user summary
	fmt.Println()
	fmt.Println("Per-user summary:")
	userNames := make([]string, 0, len(userStats))
	for u := range userStats {
		userNames = append(userNames, u)
	}
	sort.Slice(userNames, func(i, j int) bool { return userStats[userNames[i]].Size > userStats[userNames[j]].Size })
	if topN > 0 && topN < len(userNames) {
		userNames = userNames[:topN]
	}
	for _, u := range userNames {
		s := userStats[u]
		// combined user size string
		sizeCombined := "0"
		if val, ok := userSizeStr[u]; ok {
			sizeCombined = val
		} else if s != nil {
			if bytesFlag {
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

	// per-group summary
	fmt.Println()
	fmt.Println("Per-group summary:")
	groupNames := make([]string, 0, len(groupStats))
	for g := range groupStats {
		groupNames = append(groupNames, g)
	}
	sort.Slice(groupNames, func(i, j int) bool { return groupStats[groupNames[i]].Size > groupStats[groupNames[j]].Size })
	if topN > 0 && topN < len(groupNames) {
		groupNames = groupNames[:topN]
	}
	for _, g := range groupNames {
		s := groupStats[g]
		sizeCombined := "0"
		if val, ok := groupSizeStr[g]; ok {
			sizeCombined = val
		} else if s != nil {
			if bytesFlag {
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

// buildChildrenAndSizes builds the children map and dirSizes map from dirStats.
func buildChildrenAndSizes(dirStats map[string]*DirStat) (map[string][]string, map[string]int64) {
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
		readJSON    = flag.String("read-json", "", "read JSON summary from file and print human tree (skips scanning)")
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

	// Shared variables for scanning and read-json mode
	var (
		rootAbs       string
		children      map[string][]string
		dirStats      map[string]*DirStat
		userStats     map[string]*UserStat
		groupStats    map[string]*GroupStat
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

	// If user asked for version, print and exit
	if *versionFlag {
		fmt.Println("Version: ", version)
		fmt.Println("Commit:  ", commit)
		fmt.Println("Date:    ", date)
		return
	}

	// If read-json was provided, load file and prepare data structures for printing, then jump to printing
	if *readJSON != "" {
		// read JSON (allow '-' for stdin)
		jo, err := LoadSummary(*readJSON)
		if err != nil {
			log.Fatalf("failed to load json: %v", err)
		}

		// build maps from jo
		dirStats = make(map[string]*DirStat)
		userStats = make(map[string]*UserStat)
		groupStats = make(map[string]*GroupStat)
		ownerByRel := make(map[string]string)
		groupByRel := make(map[string]string)

		for _, d := range jo.Dirs {
			rel := d.Rel
			if rel == "" {
				rel = "."
			}
			dirStats[rel] = &DirStat{Size: d.Size, Files: d.Files}
			ownerByRel[rel] = d.User
			groupByRel[rel] = d.Group
		}

		for _, u := range jo.Users {
			userStats[u.Name] = &UserStat{Size: u.Size, Files: u.Files}
		}
		for _, g := range jo.Grps {
			groupStats[g.Name] = &GroupStat{Size: g.Size, Files: g.Files}
		}

		if jo.Root != "" {
			rootAbs = filepath.Clean(jo.Root)
		} else {
			rootAbs = "."
		}

		children, dirSizes = buildChildrenAndSizes(dirStats)
		sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth = ComputeSizeMapsAndWidths(dirSizes, dirStats, userStats, groupStats, *bytesFlag, *sizeWidth, *filesWidth)
		readMode = true
		readOwners = ownerByRel
		readGroups = groupByRel
		printTree(rootAbs, children, dirStats, userStats, groupStats, sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth, *levels, *showFiles, *showUser, *showGroup, *bytesFlag, *topN, readMode, readOwners, readGroups)
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
	dirStats = make(map[string]*DirStat) // key: relative path to root (".")
	userStats = make(map[string]*UserStat)
	groupStats = make(map[string]*GroupStat)

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
	children, dirSizes = buildChildrenAndSizes(dirStats)
	mu.Unlock()

	// compute size strings and widths using helper (testable)
	sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth = ComputeSizeMapsAndWidths(dirSizes, dirStats, userStats, groupStats, *bytesFlag, *sizeWidth, *filesWidth)

	// If JSON output requested, build JSON structure and write it before human output
	if *jsonOut != "" {
		// compute ended/ runtime now
		endedAt := time.Now()
		b, err := MarshalSummary(rootAbs, dirStats, userStats, groupStats, startedAt, endedAt, msStart, atomic.LoadInt64(&dirsScanned), atomic.LoadInt64(&filesScanned), version)
		if err != nil {
			log.Fatalf("failed to build json: %v", err)
		}
		if *jsonOut == "-" {
			fmt.Println(string(b))
		} else {
			if err := os.WriteFile(*jsonOut, b, 0644); err != nil {
				log.Fatalf("failed to write json file: %v", err)
			}
		}
		return
	}

	// print tree and summaries
	printTree(rootAbs, children, dirStats, userStats, groupStats, sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth, *levels, *showFiles, *showUser, *showGroup, *bytesFlag, *topN, readMode, readOwners, readGroups)
	return
}
