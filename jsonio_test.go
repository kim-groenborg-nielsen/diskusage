package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMarshalSummaryBasic(t *testing.T) {
	root := t.TempDir()
	// create a subdir so Lstat succeeds for one entry
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dirStats := map[string]*DirStat{
		".":   {Size: 1000, Files: 2},
		"sub": {Size: 500, Files: 1},
	}
	userStats := map[string]*UserStat{"u1": {Size: 1500, Files: 3}}
	groupStats := map[string]*GroupStat{"g1": {Size: 1500, Files: 3}}

	started := time.Now()
	ended := started.Add(10 * time.Millisecond)
	var msStart runtime.MemStats

	version := "v0.1.0"
	b, err := MarshalSummary(root, dirStats, userStats, groupStats, started, ended, msStart, 2, 3, version)
	if err != nil {
		t.Fatalf("MarshalSummary error: %v", err)
	}

	var jo JsonOut
	if err := json.Unmarshal(b, &jo); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	if jo.Root != root {
		t.Fatalf("root mismatch: got %q want %q", jo.Root, root)
	}

	// check stats
	if jo.Stats.Version != version {
		t.Fatalf("version mismatch: got %q want %q", jo.Stats.Version, version)
	}
	if jo.Stats.DirsScanned != 2 || jo.Stats.FilesScanned != 3 {
		t.Fatalf("stats scanned mismatch: %+v", jo.Stats)
	}

	// check dirs present and sizes
	m := map[string]JsonDir{}
	for _, d := range jo.Dirs {
		m[d.Rel] = d
	}
	if d, ok := m["."]; !ok {
		t.Fatalf("root dir missing in json dirs")
	} else if d.Size != 1000 || d.Files != 2 {
		t.Fatalf("root dir size/files mismatch: %+v", d)
	}
	if d, ok := m["sub"]; !ok {
		t.Fatalf("sub dir missing in json dirs")
	} else if d.Size != 500 || d.Files != 1 {
		t.Fatalf("sub dir size/files mismatch: %+v", d)
	}

	// users/groups counts
	if len(jo.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(jo.Users))
	}
	if len(jo.Grps) != 1 {
		t.Fatalf("expected 1 group, got %d", len(jo.Grps))
	}
}

func TestLoadSummaryFileAndStdin(t *testing.T) {
	// build a small JsonOut fixture
	fixture := JsonOut{
		Root: "r",
		Stats: JsonStats{
			StartedAt: "s",
			EndedAt:   "e",
			Version:   "vtest",
		},
		Dirs:  []JsonDir{{Path: "p", Rel: ".", Size: 10, Files: 1}},
		Users: []JsonUser{{Name: "u", Size: 10, Files: 1}},
		Grps:  []JsonGroup{{Name: "g", Size: 10, Files: 1}},
	}
	b, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	// write to temp file and test LoadSummary(path)
	tmp := filepath.Join(t.TempDir(), "out.json")
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		t.Fatalf("write temp json: %v", err)
	}
	jo, err := LoadSummary(tmp)
	if err != nil {
		t.Fatalf("LoadSummary(file) error: %v", err)
	}
	if jo.Root != fixture.Root || jo.Stats.Version != fixture.Stats.Version {
		t.Fatalf("loaded fixture mismatch: %+v", jo)
	}

	// now test stdin path: replace os.Stdin with a reader to the bytes
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	// write bytes to writer end and close it
	go func() {
		_, _ = w.Write(b)
		_ = w.Close()
	}()
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()
	jo2, err := LoadSummary("-")
	if err != nil {
		t.Fatalf("LoadSummary(stdin) error: %v", err)
	}
	if jo2.Root != fixture.Root || jo2.Stats.Version != fixture.Stats.Version {
		t.Fatalf("loaded stdin fixture mismatch: %+v", jo2)
	}
}
