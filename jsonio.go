package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"syscall"
	"time"
)

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

// MarshalSummary builds a JsonOut from runtime data and returns pretty-printed JSON bytes.
func MarshalSummary(rootAbs string, dirStats map[string]*DirStat, userStats map[string]*UserStat, groupStats map[string]*GroupStat, startedAt, endedAt time.Time, msStart runtime.MemStats, dirsScanned, filesScanned int64, version string) ([]byte, error) {
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
	sort.Slice(jo.Dirs, func(i, j int) bool { return jo.Dirs[i].Path < jo.Dirs[j].Path })
	sort.Slice(jo.Users, func(i, j int) bool { return jo.Users[i].Name < jo.Users[j].Name })
	sort.Slice(jo.Grps, func(i, j int) bool { return jo.Grps[i].Name < jo.Grps[j].Name })

	b, err := json.MarshalIndent(jo, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return b, nil
}

// LoadSummary reads JSON summary from path (use "-" for stdin) and returns the parsed JsonOut.
func LoadSummary(path string) (JsonOut, error) {
	var jo JsonOut
	var jb []byte
	var err error
	if path == "-" {
		jb, err = io.ReadAll(os.Stdin)
		if err != nil {
			return jo, err
		}
	} else {
		jb, err = os.ReadFile(path)
		if err != nil {
			return jo, err
		}
	}
	if err := json.Unmarshal(jb, &jo); err != nil {
		return jo, err
	}
	return jo, nil
}
