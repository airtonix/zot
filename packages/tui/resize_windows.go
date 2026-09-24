//go:build windows

package tui

import (
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// resizePollInterval bounds how long a size change goes unnoticed.
const resizePollInterval = 100 * time.Millisecond

var resizeWatcher sync.Once

// Windows has no SIGWINCH. Console resize events arrive as input records,
// but stdin is read as a VT byte stream, so those records never reach us.
// Poll the console size instead and fire the callbacks on every change.
// This matters most under ConPTY hosts such as multiplexers, where the
// pane width changes without any user input.
func (p *ProcTerm) installResizeHandler() {
	resizeWatcher.Do(func() {
		go func() {
			lastW, lastH := p.Size()
			ticker := time.NewTicker(resizePollInterval)
			defer ticker.Stop()
			for range ticker.C {
				w, h := p.Size()
				if w == lastW && h == lastH {
					continue
				}
				lastW, lastH = w, h
				for _, cb := range p.resizeCBs {
					cb()
				}
			}
		}()
	})
}

func (p *ProcTerm) SetNonblock(enable bool) error { return nil }

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procPeekConsoleInputW   = kernel32.NewProc("PeekConsoleInputW")
	procReadConsoleInputW   = kernel32.NewProc("ReadConsoleInputW")
	procGetNumberOfConsoleE = kernel32.NewProc("GetNumberOfConsoleInputEvents")
)

// inputRecord mirrors the Win32 INPUT_RECORD layout (20 bytes). Only
// key events are decoded; the union is kept as raw bytes.
type inputRecord struct {
	eventType uint16
	_         uint16
	event     [16]byte
}

const keyEvent = 0x0001

// yieldsByte reports whether a console record turns into at least one
// byte for ReadFile: a key-down with a character. Key-ups, focus,
// mouse, menu, and buffer-size records produce no bytes.
func (r *inputRecord) yieldsByte() bool {
	if r.eventType != keyEvent {
		return false
	}
	keyDown := *(*int32)(unsafe.Pointer(&r.event[0]))
	char := *(*uint16)(unsafe.Pointer(&r.event[10]))
	return keyDown != 0 && char != 0
}

// consoleHasByte checks pending console input without consuming bytes.
// Leading records that would not produce a byte are discarded so the
// handle stops signalling for them; otherwise a lone focus event would
// make every wait return immediately while ReadFile still blocks.
func consoleHasByte(h windows.Handle) (bool, error) {
	for {
		var count uint32
		if r, _, err := procGetNumberOfConsoleE.Call(uintptr(h), uintptr(unsafe.Pointer(&count))); r == 0 {
			return false, err
		}
		if count == 0 {
			return false, nil
		}
		records := make([]inputRecord, count)
		var read uint32
		if r, _, err := procPeekConsoleInputW.Call(uintptr(h), uintptr(unsafe.Pointer(&records[0])), uintptr(count), uintptr(unsafe.Pointer(&read))); r == 0 {
			return false, err
		}
		skip := uint32(0)
		for skip < read && !records[skip].yieldsByte() {
			skip++
		}
		if skip < read {
			return true, nil
		}
		if skip == 0 {
			return false, nil
		}
		if r, _, err := procReadConsoleInputW.Call(uintptr(h), uintptr(unsafe.Pointer(&records[0])), uintptr(skip), uintptr(unsafe.Pointer(&read))); r == 0 {
			return false, err
		}
	}
}

// peekStdin waits up to d for one byte of console input. It uses the
// console handle's wait state plus PeekConsoleInput, so a missing reply
// (for example an OSC 11 background query the host never answers) or a
// bare Esc times out instead of blocking until the next keystroke.
// Non-console stdin falls back to a blocking read.
func peekStdin(in *os.File, d time.Duration) (byte, bool, error) {
	h := windows.Handle(in.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		var b [1]byte
		if _, err := in.Read(b[:]); err != nil {
			return 0, false, err
		}
		return b[0], true, nil
	}
	deadline := time.Now().Add(d)
	for {
		ready, err := consoleHasByte(h)
		if err != nil {
			return 0, false, err
		}
		if ready {
			var b [1]byte
			if _, err := in.Read(b[:]); err != nil {
				return 0, false, err
			}
			return b[0], true, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, false, nil
		}
		event, err := windows.WaitForSingleObject(h, uint32(remaining.Milliseconds())+1)
		if err != nil {
			return 0, false, err
		}
		if event == uint32(windows.WAIT_TIMEOUT) {
			return 0, false, nil
		}
	}
}
