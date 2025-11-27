package main

import (
	"strconv"
	"testing"
)

func TestHumanizeBytesFormat(t *testing.T) {
	cases := []struct {
		input  int64
		expect string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1.0MB"},
		{5 * 1024 * 1024 * 1024, "5.0GB"},
	}
	for _, c := range cases {
		got := humanizeBytes(c.input)
		if got != c.expect {
			t.Fatalf("humanizeBytes(%d) = %q; want %q", c.input, got, c.expect)
		}
	}
}

func TestCombinedSizeString(t *testing.T) {
	// small helper mimicking combined logic
	combined := func(bytes int64, useBytes bool) string {
		if useBytes {
			return strconv.FormatInt(bytes, 10)
		}
		return humanizeBytes(bytes)
	}

	cases := []struct {
		b        int64
		useBytes bool
		expect   string
	}{
		{0, false, "0B"},
		{0, true, "0"},
		{1536, false, "1.5KB"},
		{1536, true, "1536"},
		{1024 * 1024, false, "1.0MB"},
	}
	for _, c := range cases {
		got := combined(c.b, c.useBytes)
		if got != c.expect {
			t.Fatalf("combined(%d,%v) = %q; want %q", c.b, c.useBytes, got, c.expect)
		}
	}
}
