//go:build windows

package tui

import (
	"encoding/binary"
	"testing"
)

func TestResizeWatcherIsPerTerminal(t *testing.T) {
	first, second := &ProcTerm{}, &ProcTerm{}
	var started int
	first.resizeOnce.Do(func() { started++ })
	first.resizeOnce.Do(func() { started++ })
	second.resizeOnce.Do(func() { started++ })
	if started != 2 {
		t.Fatalf("watchers started = %d, want one per terminal", started)
	}
}

func TestConsoleRecordYieldsByte(t *testing.T) {
	for _, tc := range []struct {
		name      string
		eventType uint16
		keyDown   bool
		key       uint16
		char      uint16
		want      bool
	}{
		{"character", keyEvent, true, 0x41, 'a', true},
		{"key up", keyEvent, false, 0x26, 0, false},
		{"arrow up", keyEvent, true, 0x26, 0, true},
		{"delete", keyEvent, true, 0x2e, 0, true},
		{"home", keyEvent, true, 0x24, 0, true},
		{"function key", keyEvent, true, 0x70, 0, true},
		{"shift", keyEvent, true, 0x10, 0, false},
		{"focus", 0x0010, false, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inputRecord{eventType: tc.eventType}
			if tc.keyDown {
				binary.LittleEndian.PutUint32(r.event[0:4], 1)
			}
			binary.LittleEndian.PutUint16(r.event[4:6], tc.key)
			binary.LittleEndian.PutUint16(r.event[10:12], tc.char)
			if got := r.yieldsByte(); got != tc.want {
				t.Errorf("yieldsByte() = %v, want %v", got, tc.want)
			}
		})
	}
}
