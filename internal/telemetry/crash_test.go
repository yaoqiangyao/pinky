package telemetry

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The crash tests run this test binary again as a child that captures its
// crashes and then crashes, exits or hangs as TELEMETRY_CHILD says.
const childEnv = "TELEMETRY_CHILD"

func TestMain(m *testing.M) {
	if mode := os.Getenv(childEnv); mode != "" {
		runChild(mode, os.Getenv("TELEMETRY_CHILD_DIR"), os.Getenv("TELEMETRY_CHILD_PROCESS"))
		return
	}
	os.Exit(m.Run())
}

func runChild(mode, dir, process string) {
	cr := CaptureCrashes(CrashOptions{Dir: dir, Process: process, Version: "1.2.3"})
	switch mode {
	case "panic":
		crashHere()
	case "fatal":
		// A fatal error the runtime cannot recover from, on one goroutine.
		// Racing map writes, used before, now and then made two threads
		// fail at once (a fault in the torn map and the race found), and
		// the runtime then hung instead of exiting, with or without crash
		// capture: about one child in a thousand, on Windows and macOS
		// alike (TestFatalErrorBecomesAReport ran into the 10-minute
		// timeout on Windows CI, 2026-11-08).
		var mu sync.Mutex
		mu.Unlock()
	case "exit":
		os.Exit(3) // no Stop: as if killed
	case "clean":
		cr.Stop()
		os.Exit(0)
	case "hang":
		fmt.Println("ready")
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

// crashHere panics with what a careless panic message might carry.
func crashHere() {
	home, _ := os.UserHomeDir()
	panic("boom: cannot read " + filepath.Join(home, "Documents", "notes.txt") + " for someone@example.com")
}

func child(t *testing.T, mode, dir, process string) *exec.Cmd {
	t.Helper()
	if testing.Short() {
		t.Skip("runs a child process")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), childEnv+"="+mode, "TELEMETRY_CHILD_DIR="+dir, "TELEMETRY_CHILD_PROCESS="+process,
		"GOTRACEBACK=single")
	return cmd
}

func runChildTo(t *testing.T, mode, dir, process string) {
	t.Helper()
	out, _ := child(t, mode, dir, process).CombinedOutput()
	if mode == "panic" && !strings.Contains(string(out), "boom") {
		t.Fatalf("the child did not panic:\n%s", out)
	}
}

func capture(t *testing.T, dir string) *Crashes {
	t.Helper()
	cr := CaptureCrashes(CrashOptions{Dir: dir, Process: "app", Version: "1.2.4"})
	t.Cleanup(cr.Stop)
	return cr
}

func TestCrashBecomesAReport(t *testing.T) {
	dir := t.TempDir()
	runChildTo(t, "panic", dir, "daemon")
	cr := capture(t, dir)
	found := cr.Found()
	if len(found) != 1 {
		t.Fatalf("found %d reports, want 1; unclean %v", len(found), cr.Unclean())
	}
	r := found[0]
	if r.Process != "daemon" || r.Version != "1.2.3" || r.OS == "" || r.Arch == "" || r.GoVersion == "" || r.Started.IsZero() {
		t.Fatalf("report: %+v", r)
	}
	if !strings.HasPrefix(r.Reason, "panic: boom: cannot read ~") || !strings.Contains(r.Reason, "<email>") {
		t.Fatalf("reason %q", r.Reason)
	}
	if !strings.Contains(r.Stack, "crashHere") || !strings.Contains(r.Stack, "goroutine ") {
		t.Fatalf("stack:\n%s", r.Stack)
	}
	home, _ := os.UserHomeDir()
	if strings.Contains(r.Stack, home) || strings.Contains(r.Stack, "someone@example.com") {
		t.Fatalf("the stack keeps the home folder or the e-mail address:\n%s", r.Stack)
	}

	// The client offers it, counts it, and sends or discards it.
	b := &fakeBackend{}
	t.Setenv("DO_NOT_TRACK", "")
	c := Open(Options{Dir: dir, Version: "1.2.4", Backend: b, Crashes: cr, Usage: true})
	if got := c.Report().Counts["crashes.daemon"]; got != 1 {
		t.Fatalf("crashes.daemon = %d", got)
	}
	list := c.Crashes()
	if len(list) != 1 || list[0].ID != r.ID {
		t.Fatalf("waiting reports: %+v", list)
	}
	if err := c.SendCrash(context.Background(), r.ID); err != nil {
		t.Fatal(err)
	}
	if len(b.crashes) != 1 || b.crashes[0].Reason != r.Reason || len(c.Crashes()) != 0 {
		t.Fatalf("sent %+v; still waiting %d", b.crashes, len(c.Crashes()))
	}
	if err := c.SendCrash(context.Background(), r.ID); err == nil {
		t.Fatal("sent a report twice")
	}
	if err := c.SendCrash(context.Background(), "../telemetry"); err == nil {
		t.Fatal("an id outside the crash folder was accepted")
	}

	// The next start finds nothing new.
	if again := capture(t, dir); len(again.Found()) != 0 || len(again.Unclean()) != 0 {
		t.Fatalf("found again: %+v %v", again.Found(), again.Unclean())
	}
}

func TestFatalErrorBecomesAReport(t *testing.T) {
	dir := t.TempDir()
	runChildTo(t, "fatal", dir, "app")
	found := capture(t, dir).Found()
	// The runtime prints the message before it copies to the crash file,
	// so the reason is the function that failed.
	if len(found) != 1 || found[0].Reason != "fatal error in internal/sync.fatal" {
		t.Fatalf("found %+v", found)
	}

	// A daemon's standard error goes to daemon.log, which has the message.
	runChildTo(t, "fatal", dir, "daemon")
	os.WriteFile(filepath.Join(dir, "daemon.log"), []byte("level=INFO msg=x\nfatal error: concurrent map writes\n\ngoroutine 11 [running]:\n"), 0o600)
	found = capture(t, dir).Found()
	if len(found) != 1 || found[0].Reason != "fatal error: concurrent map writes" {
		t.Fatalf("found %+v", found)
	}
}

func TestExitsWithoutACrash(t *testing.T) {
	dir := t.TempDir()
	runChildTo(t, "clean", dir, "app")
	runChildTo(t, "exit", dir, "daemon")
	cr := capture(t, dir)
	if len(cr.Found()) != 0 || len(cr.Unclean()) != 1 || cr.Unclean()[0] != "daemon" {
		t.Fatalf("found %+v, unclean %v; want one unclean daemon", cr.Found(), cr.Unclean())
	}
	// A process that scans but never counts (a second app that found
	// Pinky running) leaves the counting to the next client.
	runChildTo(t, "exit", dir, "daemon")
	runChildTo(t, "clean", dir, "app")
	t.Setenv("DO_NOT_TRACK", "")
	c := Open(Options{Dir: dir, Crashes: cr, Usage: true})
	if c.Report().Counts["exits.unclean.daemon"] != 2 || len(c.Crashes()) != 0 {
		t.Fatalf("counts %v, crashes %d", c.Report().Counts, len(c.Crashes()))
	}
	c.Close()
	c = Open(Options{Dir: dir, Crashes: cr, Usage: true})
	if c.Report().Counts["exits.unclean.daemon"] != 2 {
		t.Fatalf("counted twice: %v", c.Report().Counts)
	}
}

func TestRunningProcessIsLeftAlone(t *testing.T) {
	dir := t.TempDir()
	cmd := child(t, "hang", dir, "daemon")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "ready" {
		t.Fatalf("child said %q", line)
	}
	cr := CaptureCrashes(CrashOptions{Dir: dir, Process: "app"})
	if len(cr.Found()) != 0 || len(cr.Unclean()) != 0 {
		t.Fatalf("a running daemon's file was taken: %+v %v", cr.Found(), cr.Unclean())
	}
	runs, _ := filepath.Glob(filepath.Join(dir, "crashes", "daemon-*.run"))
	if len(runs) != 1 {
		t.Fatalf("daemon files: %v", runs)
	}
	cr.Stop()
	if mine, _ := filepath.Glob(filepath.Join(dir, "crashes", "app-*.run")); len(mine) != 0 {
		t.Fatalf("Stop left %v", mine)
	}

	cmd.Process.Kill()
	cmd.Wait()
	if cr := capture(t, dir); len(cr.Unclean()) != 1 {
		t.Fatalf("after the kill: unclean %v", cr.Unclean())
	}
}

func TestCrashReportsFollowUsageStatistics(t *testing.T) {
	dir := t.TempDir()
	runChildTo(t, "panic", dir, "app")
	cr := capture(t, dir)
	b := &fakeBackend{}
	t.Setenv("DO_NOT_TRACK", "")
	c := Open(Options{Dir: dir, Version: "1.2.4", Backend: b, Crashes: cr})
	c.sendCrashes(context.Background())
	if len(b.crashes) != 0 || len(c.Crashes()) != 1 {
		t.Fatal("sent with usage statistics off, or dropped the report")
	}
	t.Setenv("DO_NOT_TRACK", "1")
	c.SetUsage(true)
	c.sendCrashes(context.Background())
	if len(b.crashes) != 0 {
		t.Fatal("sent with DO_NOT_TRACK=1")
	}
	t.Setenv("DO_NOT_TRACK", "")
	b.setErr(fmt.Errorf("offline"))
	c.sendCrashes(context.Background())
	if len(c.Crashes()) != 1 {
		t.Fatal("a report that failed to go out was dropped")
	}
	b.setErr(nil)
	c.sendCrashes(context.Background()) // waits an hour after a failure
	if len(b.crashes) != 0 {
		t.Fatal("tried again at once")
	}
	c.crashRetry = time.Time{}
	c.sendCrashes(context.Background())
	if len(b.crashes) != 1 || len(c.Crashes()) != 0 {
		t.Fatalf("sent %d, waiting %d", len(b.crashes), len(c.Crashes()))
	}
	// Discarding forgets a report.
	runChildTo(t, "panic", dir, "app")
	cr = capture(t, dir)
	c = Open(Options{Dir: dir, Backend: b, Crashes: cr})
	c.DiscardCrash(cr.Found()[0].ID)
	if len(c.Crashes()) != 0 {
		t.Fatal("DiscardCrash kept the report")
	}
}

func TestOldReportsArePruned(t *testing.T) {
	dir := t.TempDir()
	cdir := filepath.Join(dir, "crashes")
	os.MkdirAll(cdir, 0o700)
	now := time.Now()
	for i := 0; i < maxPendingCrashes+3; i++ {
		r := CrashReport{ID: fmt.Sprintf("app-%d-1", i), Crashed: now.Add(-time.Duration(i) * time.Hour)}
		writeReport(t, cdir, r)
	}
	writeReport(t, cdir, CrashReport{ID: "app-old-1", Crashed: now.Add(-40 * 24 * time.Hour)})
	capture(t, dir)
	left := readCrashes(cdir)
	if len(left) != maxPendingCrashes || left[0].ID != "app-0-1" {
		t.Fatalf("kept %d, newest %q", len(left), left[0].ID)
	}
}

func writeReport(t *testing.T, dir string, r CrashReport) {
	t.Helper()
	data := fmt.Sprintf(`{"id":%q,"crashed":%q}`, r.ID, r.Crashed.Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(dir, r.ID+".json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
