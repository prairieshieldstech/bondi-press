//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsole stops a console window flashing when a GUI app spawns a CLI tool.
func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
