//go:build windows

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// applyRawCmdLine hands the command to cmd.exe verbatim. Go's default
// argument quoting escapes embedded double quotes as \", which cmd.exe
// does not understand, so commands such as dir "C:\a b" or
// findstr /c:"x y" reached cmd with literal backslashes and failed.
// /S makes cmd strip exactly the outer quote pair and run the rest
// unchanged.
func applyRawCmdLine(cmd *exec.Cmd, shell shellCommand, command string) {
	if shell.flag != "/C" {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = `cmd /S /C "` + command + `"`
}

func setProcessGroup(_ *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	time.AfterFunc(3*time.Second, func() {
		_ = cmd.Process.Kill()
	})
}
