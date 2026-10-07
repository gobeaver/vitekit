//go:build !windows

package vitekit

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the dev server into a process group of its own so
// the whole tree can be signaled at once. This matters because "npm run dev"
// is not Vite: npm forks node, which forks esbuild workers. Signaling only the
// process we started leaves Vite running, still holding its TCP port and the
// output pipes it inherited.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// interruptProcessTree asks the dev server and everything it spawned to shut
// down, giving Vite the chance to release its port cleanly.
func interruptProcessTree(process *os.Process) error {
	return signalProcessTree(process, syscall.SIGTERM)
}

// killProcessTree terminates the dev server and everything it spawned without
// giving it a further say.
func killProcessTree(process *os.Process) error {
	return signalProcessTree(process, syscall.SIGKILL)
}

func signalProcessTree(process *os.Process, signal syscall.Signal) error {
	group, err := syscall.Getpgid(process.Pid)
	// Falling back to the bare process is deliberate: if the child never made
	// it into a group of its own, its group is *ours*, and signaling the
	// negative pgid would take down the calling program along with it.
	if err != nil || group <= 0 || group == syscall.Getpgrp() {
		return ignoreFinished(process.Signal(signal))
	}
	return ignoreFinished(syscall.Kill(-group, signal))
}

// ignoreFinished treats "the process is already gone" as success — that is the
// outcome Stop wants, and it races naturally with the child exiting on its own.
func ignoreFinished(err error) error {
	if err == nil || errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
