package adb

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The test binary also plays a shim and the console program it runs, for
// TestServerHasNoWindow. With PINKY_CONSOLE_TEST=shim it runs itself again
// as that program the way scoop's adb shim runs adb.exe (no creation
// flags, handles inherited), and waits for it; with
// PINKY_CONSOLE_TEST=program it writes what it finds of its console to
// PINKY_CONSOLE_TEST_OUT.
func init() {
	switch os.Getenv("PINKY_CONSOLE_TEST") {
	case "shim":
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "PINKY_CONSOLE_TEST=program")
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "program":
		s := readConsole(500 * time.Millisecond)
		if err := os.WriteFile(os.Getenv("PINKY_CONSOLE_TEST_OUT"), []byte(s.String()), 0o644); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// consoleState is what a process finds of its console.
type consoleState struct {
	Console bool // it has one
	Window  bool // the console has a window
	Visible bool // which was visible
}

func (s consoleState) String() string {
	return fmt.Sprintf("%t %t %t", s.Console, s.Window, s.Visible)
}

var procGetConsoleWindow = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")

// readConsole looks at this process's console for d, as a window may show
// a moment after the console is made.
func readConsole(d time.Duration) consoleState {
	var s consoleState
	if h, err := windows.CreateFile(windows.StringToUTF16Ptr("CONOUT$"), windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0); err == nil {
		s.Console = true
		windows.CloseHandle(h)
	}
	for end := time.Now().Add(d); ; {
		if hwnd, _, _ := procGetConsoleWindow.Call(); hwnd != 0 {
			s.Window = true
			if windows.IsWindowVisible(windows.HWND(hwnd)) {
				s.Visible = true
			}
		}
		if s.Visible || time.Now().After(end) {
			return s
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestServerHasNoWindow: a console program that the server's command
// starts, as scoop's adb shim starts the real adb.exe, shares the server's
// console, which has no window. With DETACHED_PROCESS, as Pinky started
// the server before, that program got a console of its own whose window
// stayed open as long as the server ran; the test checks that too, which
// shows a console window for half a second, so that it fails where it
// would not catch the window.
func TestServerHasNoWindow(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(change func(*syscall.SysProcAttr)) consoleState {
		t.Helper()
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		cmd, err := serverCommand(exe, "tcp:0", w)
		if err != nil {
			w.Close()
			t.Fatal(err)
		}
		if change != nil {
			change(cmd.SysProcAttr)
		}
		out := filepath.Join(t.TempDir(), "console.txt")
		cmd.Env = append(os.Environ(), "PINKY_CONSOLE_TEST=shim", "PINKY_CONSOLE_TEST_OUT="+out)
		err = cmd.Run()
		w.Close()
		if err != nil {
			t.Fatalf("the shim: %v", err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var s consoleState
		if _, err := fmt.Sscan(string(b), &s.Console, &s.Window, &s.Visible); err != nil {
			t.Fatalf("%q: %v", b, err)
		}
		return s
	}

	now := run(nil)
	t.Logf("started as the server: %+v", now)
	if !now.Console || now.Visible {
		t.Errorf("the program the server's command started: %+v, want a console without a visible window", now)
	}

	before := run(func(a *syscall.SysProcAttr) {
		a.CreationFlags = windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP
		a.HideWindow = false
	})
	t.Logf("started with DETACHED_PROCESS: %+v", before)
	if !before.Visible {
		t.Errorf("with DETACHED_PROCESS the program got %+v, not the visible window this test is to catch", before)
	}
}
