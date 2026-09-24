package swarm

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeTailLog writes n events "e0".."e<n-1>" and returns the path and
// the file size.
func writeTailLog(t *testing.T, n int) (string, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := OpenEventLog(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < n; i++ {
		if err := log.Append(NewEvent(fmt.Sprintf("e%d", i), map[string]any{"pad": "0123456789"})); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	_ = log.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	return path, info.Size()
}

// lineStarts returns the byte offset of every line in the file. Event
// lines differ in length (timestamps drop trailing zeros), so windows
// are computed from real offsets rather than an average line size.
func lineStarts(t *testing.T, path string) []int64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	starts := []int64{0}
	for i, b := range data[:len(data)-1] {
		if b == '\n' {
			starts = append(starts, int64(i+1))
		}
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("log does not end with a newline")
	}
	return starts
}

func eventTypes(evs []Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type
	}
	return out
}

func TestReadEventLogTailSmallFileReadsEverything(t *testing.T) {
	path, size := writeTailLog(t, 5)
	for _, limit := range []int64{0, size, size + 100} {
		got, err := ReadEventLogTail(path, limit)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if len(got) != 5 || got[0].Type != "e0" || got[4].Type != "e4" {
			t.Fatalf("limit %d: got %v", limit, eventTypes(got))
		}
	}
}

// The tail window usually starts mid-line; that partial line must be
// dropped rather than parsed, and every later event returned in order.
func TestReadEventLogTailSkipsPartialFirstLine(t *testing.T) {
	path, size := writeTailLog(t, 50)
	starts := lineStarts(t, path)
	// Start the window in the middle of line 46: e46 is partial.
	mid := (starts[46] + starts[47]) / 2
	got, err := ReadEventLogTail(path, size-mid)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []string{"e47", "e48", "e49"}
	if fmt.Sprint(eventTypes(got)) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", eventTypes(got), want)
	}
}

// A window that falls exactly on a line boundary still drops its first
// line: it cannot tell a boundary from a partial line, and losing one
// event of a bounded transcript is harmless.
func TestReadEventLogTailBoundaryWindow(t *testing.T) {
	path, size := writeTailLog(t, 10)
	starts := lineStarts(t, path)
	// The window starts exactly at the beginning of e6.
	got, err := ReadEventLogTail(path, size-starts[6])
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []string{"e7", "e8", "e9"}
	if fmt.Sprint(eventTypes(got)) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", eventTypes(got), want)
	}
}

func TestReadEventLogTailWindowWithoutNewline(t *testing.T) {
	path, _ := writeTailLog(t, 10)
	got, err := ReadEventLogTail(path, 5) // inside the last line
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want no events and no error", eventTypes(got), err)
	}
}

func TestReadEventLogTailMissingFile(t *testing.T) {
	got, err := ReadEventLogTail(filepath.Join(t.TempDir(), "missing.jsonl"), 1024)
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil, nil", got, err)
	}
}
