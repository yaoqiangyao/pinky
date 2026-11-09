package adb

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// serverCommand is the server's command: the pipe's handle is inherited,
// and its number goes on the command line, as adb's own launch_server
// starts it.
func serverCommand(path, listen string, reply *os.File) (*exec.Cmd, error) {
	h := windows.Handle(reply.Fd())
	if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		return nil, err
	}
	cmd := exec.Command(path, "-L", listen, "fork-server", "server", "--reply-fd", strconv.FormatUint(uint64(h), 10))
	cmd.SysProcAttr = serverAttr(syscall.Handle(h))
	return cmd, nil
}

// serverAttr starts the server in a process group of its own, with a
// console that has no window (CREATE_NO_WINDOW) rather than with none
// (DETACHED_PROCESS, as adb's own launch_server does). Windows gives a
// console program that a program without a console starts a console of
// its own, with a window: the adb that scoop puts on the PATH is such a
// program, a shim that runs the real adb.exe, so the server's console
// window stayed open for as long as the server ran. A console program that
// the server's command starts now shares its console, unseen. HideWindow
// asks a window shown anyway to start hidden. inherit is the pipe's
// handle.
func serverAttr(inherit syscall.Handle) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags:              windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:                 true,
		AdditionalInheritedHandles: []syscall.Handle{inherit},
	}
}

// captureStderr: on Windows the server may keep the standard error it was
// given open for good, so it gets none.
const captureStderr = false

// hideWindow keeps a console program (adb.exe) that a windowed Pinky runs
// from flashing a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}
