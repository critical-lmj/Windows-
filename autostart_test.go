package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseStartupFlags(t *testing.T) {
	opts := parseStartupFlags([]string{"--daemon", "--interval", "15", "--minimized", "--no-elevate"})
	if !opts.Daemon || !opts.Minimized || !opts.NoElevate {
		t.Fatalf("unexpected boolean flags: %+v", opts)
	}
	if opts.Interval != 15*time.Second {
		t.Fatalf("Interval = %s, want 15s", opts.Interval)
	}

	optsEq := parseStartupFlags([]string{"--interval=20"})
	if optsEq.Interval != 20*time.Second {
		t.Fatalf("Interval (=) = %s, want 20s", optsEq.Interval)
	}

	optsDef := parseStartupFlags(nil)
	if optsDef.Interval != defaultMonitorInterval {
		t.Fatalf("default Interval = %s, want %s", optsDef.Interval, defaultMonitorInterval)
	}
}

func TestBuildAutoStartArgs(t *testing.T) {
	exe := `D:\tools\TaskSchedulerManager.exe`
	daemonArgs := buildDaemonAutoStartArgs(exe)
	assertContainsInOrder(t, daemonArgs, []string{"/create", "/tn", daemonTaskName, "/tr", `"D:\tools\TaskSchedulerManager.exe" --daemon`, "/sc", "ONSTART", "/ru", "SYSTEM", "/rl", "HIGHEST", "/f"})

	trayArgs := buildTrayLogonAutoStartArgs(exe)
	assertContainsInOrder(t, trayArgs, []string{"/create", "/tn", trayLogonTaskName, "/tr", `"D:\tools\TaskSchedulerManager.exe" --minimized`, "/sc", "ONLOGON", "/rl", "HIGHEST", "/f"})
}

func TestNamedMutexPreventsDuplicateOwner(t *testing.T) {
	name := fmt.Sprintf(`Local\TaskSchedulerManager_TestMutex_%d`, time.Now().UnixNano())
	m1, err := acquireNamedMutex(name)
	if err != nil {
		t.Fatalf("first acquireNamedMutex failed: %v", err)
	}
	defer m1.Release()
	if !m1.isOwner {
		t.Fatalf("expected first mutex acquisition to be owner")
	}

	m2, err := acquireNamedMutex(name)
	if err != nil {
		t.Fatalf("second acquireNamedMutex returned error: %v", err)
	}
	defer m2.Release()
	if m2.isOwner {
		t.Fatalf("expected second mutex acquisition NOT to be owner while first holds lock")
	}

	m1.Release()
	m3, err := acquireNamedMutex(name)
	if err != nil {
		t.Fatalf("third acquireNamedMutex after release failed: %v", err)
	}
	defer m3.Release()
	if !m3.isOwner {
		t.Fatalf("expected acquisition after release to become owner")
	}
	if !strings.Contains(m3.name, "TestMutex") {
		t.Fatalf("unexpected mutex name: %q", m3.name)
	}
}
