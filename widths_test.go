package main

import (
	"testing"
)

func TestComputeSizeMapsAndWidths_AutoFitHuman(t *testing.T) {
	// small tree with varying sizes
	dirs := map[string]int64{
		".": 1024 * 1024 * 2, // 2.0MB -> "2.0MB" length 4
		"a": 1536,            // 1.5KB length 4
		"b": 512,             // 512B length 4
	}
	dirstats := map[string]*DirStat{
		".": {Size: dirs["."], Files: 10},
		"a": {Size: dirs["a"], Files: 2},
		"b": {Size: dirs["b"], Files: 1},
	}
	users := map[string]*UserStat{
		"u1": {Size: dirs["."], Files: 10},
	}
	groups := map[string]*GroupStat{}

	sizeMap, _, _, sw, fw := ComputeSizeMapsAndWidths(dirs, dirstats, users, groups, false, 0, 0)
	// expect size strings like "2.0MB", "1.5KB", "512B"
	if sizeMap["."] != "2.0MB" || sizeMap["a"] != "1.5KB" || sizeMap["b"] != "512B" {
		t.Fatalf("unexpected sizeMap values: %v", sizeMap)
	}
	// size width should be at least the longest of those: len("2.0MB") == 5
	if sw < 4 {
		t.Fatalf("size width too small: %d", sw)
	}
	// files width should accommodate 10 -> 2
	if fw < 2 {
		t.Fatalf("files width too small: %d", fw)
	}
}

func TestComputeSizeMapsAndWidths_BytesOverride(t *testing.T) {
	dirs := map[string]int64{".": 2777066}
	dirstats := map[string]*DirStat{".": {Size: 2777066, Files: 13}}
	users := map[string]*UserStat{"u": {Size: 2777066, Files: 13}}
	groups := map[string]*GroupStat{}

	_, _, _, sw, fw := ComputeSizeMapsAndWidths(dirs, dirstats, users, groups, true, 0, 0)
	// bytes length should be at least len("2777066") == 7
	if sw < 7 {
		t.Fatalf("expected size width >=7, got %d", sw)
	}
	if fw < 2 {
		t.Fatalf("files width too small: %d", fw)
	}
}

func TestComputeSizeMapsAndWidths_OverridesAndTop(t *testing.T) {
	dirs := map[string]int64{".": 1024}
	dirstats := map[string]*DirStat{".": {Size: 1024, Files: 5}}
	users := map[string]*UserStat{}
	groups := map[string]*GroupStat{}

	_, _, _, sw, fw := ComputeSizeMapsAndWidths(dirs, dirstats, users, groups, false, 10, 6)
	if sw != 10 {
		t.Fatalf("expected size width override 10, got %d", sw)
	}
	if fw != 6 {
		t.Fatalf("expected files width override 6, got %d", fw)
	}
}
