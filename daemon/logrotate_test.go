package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestRotatingWriter(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")

	maxSize := int64(500) // 500 bytes
	w, err := NewRotatingWriter(logPath, maxSize, 7)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer w.Close()

	line := strings.Repeat("A", 100) + "\n" // 101 bytes

	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("Write #%d: %v", i, err)
		}
	}

	// After 10 writes of 101 bytes = 1010 bytes, rotation should have occurred.
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("Stat main: %v", err)
	}
	if info.Size() > maxSize+200 {
		t.Errorf("main log too large: %d bytes (max %d)", info.Size(), maxSize)
	}

	// At least one date-stamped archive should exist.
	archives := listArchiveFiles(t, dir, "test")
	if len(archives) == 0 {
		t.Fatalf("expected at least one archived log, got none")
	}

	t.Logf("main: %d bytes, %d archives", info.Size(), len(archives))
}

func TestRotatingWriter_ArchiveNaming(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	w, err := NewRotatingWriter(logPath, 100, 7)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}

	// Force a size rotation on the current day.
	big := strings.Repeat("B", 150) + "\n"
	if _, err := w.Write([]byte(big)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	w.Close()

	archives := listArchiveFiles(t, dir, "app")
	if len(archives) == 0 {
		t.Fatalf("expected an archive, got none")
	}
	// Archive should be named app-YYYY-MM-DD.log (no sequence on first).
	for _, a := range archives {
		if !strings.HasPrefix(a, "app-20") || !strings.HasSuffix(a, ".log") {
			t.Errorf("unexpected archive name: %q", a)
		}
	}
}

func TestRotatingWriter_DayRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	w, err := NewRotatingWriter(logPath, 1<<30, 7)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer w.Close()

	// Inject a controllable clock.
	base := time.Date(2026, 9, 3, 10, 0, 0, 0, time.Local)
	w.now = func() time.Time { return base }

	if _, err := w.Write([]byte("day one\n")); err != nil {
		t.Fatalf("Write day one: %v", err)
	}

	// Advance to the next day; the next write should archive day one.
	w.now = func() time.Time { return base.AddDate(0, 0, 1) }
	if _, err := w.Write([]byte("day two\n")); err != nil {
		t.Fatalf("Write day two: %v", err)
	}

	archives := listArchiveFiles(t, dir, "app")
	if len(archives) != 1 {
		t.Fatalf("expected 1 archive after day rollover, got %d: %v", len(archives), archives)
	}
	if !strings.Contains(archives[0], "2026-09-03") {
		t.Errorf("expected archive dated 2026-09-03, got %q", archives[0])
	}
}

func TestRotatingWriter_PruneOldArchives(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	w, err := NewRotatingWriter(logPath, 100, 7)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer w.Close()

	// Create an old archive (> 7 days) and a recent one.
	old := filepath.Join(dir, "app-2026-08-01.log")
	recent := filepath.Join(dir, "app-2026-09-01.log")
	if err := os.WriteFile(old, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recent, []byte("recent"), 0644); err != nil {
		t.Fatal(err)
	}
	// Set mtimes explicitly so the test is deterministic.
	oldTime := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	recentTime := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	os.Chtimes(old, oldTime, oldTime)
	os.Chtimes(recent, recentTime, recentTime)

	// Pin "now" to 2026-09-03 so old (8/1) is > 7 days, recent (9/1) is not.
	w.now = func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.Local) }

	// Trigger a size rotation, which calls prune.
	big := strings.Repeat("C", 2000) + "\n"
	if _, err := w.Write([]byte(big)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old archive should have been pruned")
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("recent archive should remain: %v", err)
	}
}

func listArchiveFiles(t *testing.T, dir, stem string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, stem+"-") && strings.HasSuffix(name, ".log") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func TestMetaSaveLoad(t *testing.T) {
	origHome := os.Getenv("HOME")
	dir := t.TempDir()
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	m := &Meta{
		LogFile:     "/tmp/test.log",
		LogMaxSize:  1024,
		WorkDir:     "/tmp",
		BinaryPath:  "/usr/local/bin/heron-connect",
		InstalledAt: NowISO(),
	}

	if err := SaveMeta(m); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	loaded, err := LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}

	if loaded.LogFile != m.LogFile {
		t.Errorf("LogFile mismatch: %s != %s", loaded.LogFile, m.LogFile)
	}
	if loaded.WorkDir != m.WorkDir {
		t.Errorf("WorkDir mismatch: %s != %s", loaded.WorkDir, m.WorkDir)
	}
}
