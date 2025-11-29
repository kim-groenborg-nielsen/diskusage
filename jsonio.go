package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProgressEnabled controls whether jsonio reports progress messages to stderr.
var ProgressEnabled bool

func progressf(format string, a ...interface{}) {
	if ProgressEnabled {
		_, _ = fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}

// JSON schema types
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
	StartedAt          string  `json:"started_at"`
	EndedAt            string  `json:"ended_at"`
	RuntimeSeconds     float64 `json:"runtime_seconds"`
	Runtime            string  `json:"runtime"`
	DirsScanned        int64   `json:"dirs_scanned"`
	FilesScanned       int64   `json:"files_scanned"`
	MemAlloc           uint64  `json:"mem_alloc_bytes"`
	TotalAlloc         uint64  `json:"total_alloc_bytes"`
	HeapAlloc          uint64  `json:"heap_alloc_bytes"`
	HeapSys            uint64  `json:"heap_sys_bytes"`
	NumGC              uint32  `json:"num_gc"`
	PauseTotalNs       uint64  `json:"pause_total_ns"`
	LastGC             string  `json:"last_gc,omitempty"`
	GCCPUFraction      float64 `json:"gc_cpu_fraction"`
	HeapInuse          uint64  `json:"heap_inuse_bytes"`
	HeapIdle           uint64  `json:"heap_idle_bytes"`
	HeapReleased       uint64  `json:"heap_released_bytes"`
	NextGC             uint64  `json:"next_gc_bytes"`
	LastPauseNs        uint64  `json:"last_pause_ns"`
	MaxPauseNs         uint64  `json:"max_pause_ns"`
	PeakAllocBytes     uint64  `json:"peak_alloc_bytes"`
	PeakHeapAllocBytes uint64  `json:"peak_heap_alloc_bytes"`
	Version            string  `json:"version"`
}

type JsonOut struct {
	Root  string      `json:"root"`
	Stats JsonStats   `json:"stats"`
	Dirs  []JsonDir   `json:"dirs"`
	Users []JsonUser  `json:"users"`
	Grps  []JsonGroup `json:"groups"`
}

// StreamSummary writes the JSON summary directly to an io.Writer. It's safe to pass a gzip.Writer
// as the writer so the JSON is streamed into compressed output without creating a large []byte.
func StreamSummary(w io.Writer, rootAbs string, dirStats map[string]*DirStat, userStats map[string]*UserStat, groupStats map[string]*GroupStat, startedAt, endedAt time.Time, msStart runtime.MemStats, dirsScanned, filesScanned int64, version string) error {
	// collect memory stats
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	// format last GC
	lastGC := ""
	if ms.LastGC != 0 {
		lastGC = time.Unix(0, int64(ms.LastGC)).Format(time.RFC3339)
	}

	// compute last and max pause
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

	// compute peak estimates
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
			RuntimeSeconds:     endedAt.Sub(startedAt).Seconds(),
			Runtime:            endedAt.Sub(startedAt).String(),
			DirsScanned:        dirsScanned,
			FilesScanned:       filesScanned,
			MemAlloc:           ms.Alloc,
			TotalAlloc:         ms.TotalAlloc,
			HeapAlloc:          ms.HeapAlloc,
			HeapSys:            ms.HeapSys,
			NumGC:              ms.NumGC,
			PauseTotalNs:       ms.PauseTotalNs,
			LastGC:             lastGC,
			GCCPUFraction:      ms.GCCPUFraction,
			HeapInuse:          ms.HeapInuse,
			HeapIdle:           ms.HeapIdle,
			HeapReleased:       ms.HeapReleased,
			NextGC:             ms.NextGC,
			LastPauseNs:        lastPause,
			MaxPauseNs:         maxPause,
			PeakAllocBytes:     peakAlloc,
			PeakHeapAllocBytes: peakHeapAlloc,
			Version:            version,
		},
	}

	// collect directories (attempt to stat to get uid/gid)
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
				if uEnt, err := user.LookupId(uidStr); err == nil {
					uname = uEnt.Username
				}
				if gEnt, err := user.LookupGroupId(gidStr); err == nil {
					gname = gEnt.Name
				}
			}
		}
		jo.Dirs = append(jo.Dirs, JsonDir{Path: abs, Rel: rel, Size: ds.Size, Files: ds.Files, UID: uid, User: uname, GID: gid, Group: gname})
	}

	// collect users
	for u, us := range userStats {
		resolvedName := u
		var uidNum uint32
		if ent, err := user.Lookup(u); err == nil {
			resolvedName = ent.Username
			if v, err := strconv.ParseUint(ent.Uid, 10, 32); err == nil {
				uidNum = uint32(v)
			}
		} else if ent, err := user.LookupId(u); err == nil {
			resolvedName = ent.Username
			if v, err := strconv.ParseUint(ent.Uid, 10, 32); err == nil {
				uidNum = uint32(v)
			}
		} else if v, err := strconv.ParseUint(u, 10, 32); err == nil {
			uidNum = uint32(v)
		}
		jo.Users = append(jo.Users, JsonUser{Name: resolvedName, Size: us.Size, Files: us.Files, UID: uidNum})
	}

	// collect groups
	for g, gs := range groupStats {
		resolved := g
		var gidNum uint32
		if ent, err := user.LookupGroup(g); err == nil {
			resolved = ent.Name
			if v, err := strconv.ParseUint(ent.Gid, 10, 32); err == nil {
				gidNum = uint32(v)
			}
		} else if ent, err := user.LookupGroupId(g); err == nil {
			resolved = ent.Name
			if v, err := strconv.ParseUint(ent.Gid, 10, 32); err == nil {
				gidNum = uint32(v)
			}
		} else if v, err := strconv.ParseUint(g, 10, 32); err == nil {
			gidNum = uint32(v)
		}
		jo.Grps = append(jo.Grps, JsonGroup{Name: resolved, Size: gs.Size, Files: gs.Files, GID: gidNum})
	}

	// deterministic ordering
	progressf("sorting dirs (%d), users (%d), groups (%d)", len(jo.Dirs), len(jo.Users), len(jo.Grps))
	sort.Slice(jo.Dirs, func(i, j int) bool { return jo.Dirs[i].Path < jo.Dirs[j].Path })
	sort.Slice(jo.Users, func(i, j int) bool { return jo.Users[i].Name < jo.Users[j].Name })
	sort.Slice(jo.Grps, func(i, j int) bool { return jo.Grps[i].Name < jo.Grps[j].Name })

	// Stream the JSON with pretty indentation to the provided writer.
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	// We'll write the object manually so we can stream large arrays without building an extra []byte buffer.
	if _, err := io.WriteString(w, "{\n"); err != nil {
		return err
	}
	// root
	rootVal, _ := json.MarshalIndent(jo.Root, "", "  ")
	rootLine := fmt.Sprintf("  \"root\": %s,\n", string(rootVal))
	if _, err := io.WriteString(w, rootLine); err != nil {
		return err
	}
	// stats
	statsBytes, _ := json.MarshalIndent(jo.Stats, "", "  ")
	statsLine := fmt.Sprintf("  \"stats\": %s,\n", string(statsBytes))
	if _, err := io.WriteString(w, statsLine); err != nil {
		return err
	}

	// dirs array
	if _, err := io.WriteString(w, "  \"dirs\": [\n"); err != nil {
		return err
	}
	for i, d := range jo.Dirs {
		b, _ := json.MarshalIndent(d, "", "  ")
		// indent entries by two spaces
		entry := string(b)
		// replace leading '{' with '    {' to keep pretty indent consistent
		entry = indentString(entry, 4)
		if i < len(jo.Dirs)-1 {
			entry += ",\n"
		} else {
			entry += "\n"
		}
		if _, err := io.WriteString(w, entry); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "  ],\n"); err != nil {
		return err
	}

	// users array
	if _, err := io.WriteString(w, "  \"users\": [\n"); err != nil {
		return err
	}
	for i, u := range jo.Users {
		b, _ := json.MarshalIndent(u, "", "  ")
		entry := indentString(string(b), 4)
		if i < len(jo.Users)-1 {
			entry += ",\n"
		} else {
			entry += "\n"
		}
		if _, err := io.WriteString(w, entry); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "  ],\n"); err != nil {
		return err
	}

	// groups array
	if _, err := io.WriteString(w, "  \"groups\": [\n"); err != nil {
		return err
	}
	for i, g := range jo.Grps {
		b, _ := json.MarshalIndent(g, "", "  ")
		entry := indentString(string(b), 4)
		if i < len(jo.Grps)-1 {
			entry += ",\n"
		} else {
			entry += "\n"
		}
		if _, err := io.WriteString(w, entry); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "  ]\n"); err != nil {
		return err
	}

	// close object
	if _, err := io.WriteString(w, "}\n"); err != nil {
		return err
	}

	// done
	return nil
}

// indentString prefixes each line of s with n spaces (except empty lines)
func indentString(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, L := range lines {
		if strings.TrimSpace(L) == "" {
			lines[i] = ""
		} else {
			lines[i] = pad + L
		}
	}
	return strings.Join(lines, "\n")

}

// MarshalSummary builds a JsonOut from runtime data and returns pretty-printed JSON bytes.
func MarshalSummary(rootAbs string, dirStats map[string]*DirStat, userStats map[string]*UserStat, groupStats map[string]*GroupStat, startedAt, endedAt time.Time, msStart runtime.MemStats, dirsScanned, filesScanned int64, version string) ([]byte, error) {
	// For backward compatibility we still construct the bytes via StreamSummary into a buffer.
	var buf bytes.Buffer
	if err := StreamSummary(&buf, rootAbs, dirStats, userStats, groupStats, startedAt, endedAt, msStart, dirsScanned, filesScanned, version); err != nil {
		return nil, err
	}
	return ioutil.ReadAll(&buf)
}

// LoadSummary reads JSON summary from path (use "-" for stdin) and returns the parsed JsonOut.
func LoadSummary(path string) (JsonOut, error) {
	var jo JsonOut
	// Use a reader that allows peeking at the first bytes to detect gzip magic without reading everything.
	var (
		r   io.Reader
		f   *os.File
		err error
	)
	if path == "-" {
		r = os.Stdin
	} else {
		f, err = os.Open(path)
		if err != nil {
			return jo, err
		}
		defer f.Close()
		r = f
	}
	bufr := bufio.NewReader(r)
	// peek up to 2 bytes to detect gzip
	peek, err := bufr.Peek(2)
	if err != nil && err != io.EOF {
		return jo, err
	}
	isGz := len(peek) >= 2 && peek[0] == 0x1f && peek[1] == 0x8b
	if isGz {
		gr, err := gzip.NewReader(bufr)
		if err != nil {
			return jo, fmt.Errorf("gzip reader: %w", err)
		}
		defer gr.Close()
		dec := json.NewDecoder(gr)
		if err := dec.Decode(&jo); err != nil {
			return jo, err
		}
		return jo, nil
	}
	// not gzipped: decode directly from buffered reader
	dec := json.NewDecoder(bufr)
	if err := dec.Decode(&jo); err != nil {
		return jo, err
	}
	return jo, nil
}
