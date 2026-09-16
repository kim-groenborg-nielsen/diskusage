package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestAddGzExt(t *testing.T) {
	cases := []struct {
		in  string
		out string
	}{
		{"out.json", "out.json.gz"},
		{"out.json.gz", "out.json.gz"},
		{"OUT.JSON.GZ", "OUT.JSON.GZ"},
		{"data", "data.gz"},
	}
	for _, c := range cases {
		res := addGzExt(c.in)
		if res != c.out {
			t.Fatalf("addGzExt(%q) = %q, want %q", c.in, res, c.out)
		}
	}
}

func TestStreamSummaryGzip(t *testing.T) {
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

	// stream into gzip writer backed by a buffer
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if err := StreamSummary(gw, root, dirStats, userStats, groupStats, started, ended, msStart, 2, 3, "v0.1.0"); err != nil {
		gw.Close()
		t.Fatalf("StreamSummary error: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	// decompress and verify JSON
	r := bytes.NewReader(buf.Bytes())
	gr, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip new reader: %v", err)
	}
	defer gr.Close()
	out, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("read gzipped content: %v", err)
	}
	var jo JsonOut
	if err := json.Unmarshal(out, &jo); err != nil {
		t.Fatalf("unmarshal json from gzipped stream: %v", err)
	}
	if jo.Root != root {
		t.Fatalf("root mismatch: got %q want %q", jo.Root, root)
	}
	if jo.Stats.DirsScanned != 2 || jo.Stats.FilesScanned != 3 {
		t.Fatalf("stats mismatch: %+v", jo.Stats)
	}
}

func TestStreamSummaryGzipToFileAndLoad(t *testing.T) {
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

	// create gzipped file on disk
	outPath := filepath.Join(t.TempDir(), "out.json.gz")
	f, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create out file: %v", err)
	}
	gw := gzip.NewWriter(f)
	if err := StreamSummary(gw, root, dirStats, userStats, groupStats, started, ended, msStart, 2, 3, "v0.1.0"); err != nil {
		gw.Close()
		f.Close()
		t.Fatalf("StreamSummary error: %v", err)
	}
	if err := gw.Close(); err != nil {
		f.Close()
		t.Fatalf("gzip close: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("file close: %v", err)
	}

	// now load via LoadSummary which should auto-detect gzip
	jo, err := LoadSummary(outPath)
	if err != nil {
		t.Fatalf("LoadSummary(gz file) error: %v", err)
	}
	if jo.Root != root {
		t.Fatalf("root mismatch: got %q want %q", jo.Root, root)
	}
	if jo.Stats.DirsScanned != 2 || jo.Stats.FilesScanned != 3 {
		t.Fatalf("stats mismatch: %+v", jo.Stats)
	}
}
