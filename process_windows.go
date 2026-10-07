//go:build windows

package vitekit

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureProcessGroup starts the dev server as the root of a new process
// group. Two things follow from that: a console control event addressed to its
// PID reaches the whole tree, and the console's own Ctrl+C no longer kills it
// out from under us mid-cleanup — the orchestrator decides when it dies.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// interruptProcessTree asks the dev server tree to shut down cleanly so Vite
// can release its port.
//
// CTRL_BREAK is the mechanism that actually works here. taskkill without /F
// posts WM_CLOSE, which a console application such as node simply ignores — so
// it would report success, nothing would exit, and shutdown would stall for the
// whole grace period before being killed anyway. A break event addressed to the
// group created above reaches node, which maps it to SIGBREAK and exits.
//
// An error is returned rather than swallowed so the caller escalates
// immediately; that is the right outcome when there is no console to signal,
// as in a service or a detached parent.
func interruptProcessTree(process *os.Process) error {
	// A break event travels through a console. A service, a detached parent or
	// a CI runner has none, and GenerateConsoleCtrlEvent can report success
	// there while nothing ever receives it — which would spend the whole grace
	// period waiting for an exit that is not coming. Escalating immediately is
	// both faster and honest about what happened.
	if !hasConsole() {
		return errors.New("vitekit: no console attached, cannot signal the process group")
	}
	// The group ID of a CREATE_NEW_PROCESS_GROUP child is its own PID.
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(process.Pid))
}

// hasConsole reports whether this process is attached to a console at all.
// The code page is asked rather than a standard handle, because stdout and
// stdin are routinely redirected to pipes by a process that still has one.
func hasConsole() bool {
	codePage, err := windows.GetConsoleCP()
	return err == nil && codePage != 0
}

// killProcessTree terminates the dev server and every process it spawned.
// taskkill /T walks the child list, which is what reaches the node and esbuild
// processes npm forks; os.Process.Kill would only end the process we started.
func killProcessTree(process *os.Process) error {
	command := exec.Command("taskkill", taskkillArgs(process.Pid)...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := command.Run(); err == nil {
		return nil
	}
	// taskkill is unavailable or the tree is already gone. A direct kill still
	// covers a single-process dev server.
	return ignoreFinished(process.Kill())
}

// taskkillArgs builds the argument list for a forced, whole-tree termination.
func taskkillArgs(pid int) []string {
	return []string{"/F", "/T", "/PID", strconv.Itoa(pid)}
}

// ignoreFinished treats "the process is already gone" as success — that is the
// outcome Stop wants, and it races naturally with the child exiting on its own.
func ignoreFinished(err error) error {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
