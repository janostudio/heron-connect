package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// RotatingWriter is a thread-safe io.Writer that appends to a log file and
// rotates it on two triggers:
//
//   - size: when the active file exceeds maxSize, it is archived with a
//     date-stamped name (e.g. app-2026-09-03.log) and a fresh active file
//     is opened.
//   - day: when the calendar date changes, the previous day's active file is
//     archived the same way.
//
// Archived files older than retentionDays are pruned, so disk usage stays
// bounded. The active file always keeps its original path (e.g. app.log);
// archives are siblings named "<stem>-YYYY-MM-DD[-seq].log".
type RotatingWriter struct {
	mu            sync.Mutex
	file          *os.File
	path          string
	stem          string // path without the .log extension
	ext           string // ".log"
	maxSize       int64
	retentionDays int
	curSize       int64
	curDay        string // "2006-01-02" of the active file
	now           func() time.Time
}

// NewRotatingWriter opens path for appending with size- and day-based
// rotation, keeping archived logs for retentionDays days (default 7 when
// retentionDays <= 0).
func NewRotatingWriter(path string, maxSize int64, retentionDays int) (*RotatingWriter, error) {
	if retentionDays <= 0 {
		retentionDays = DefaultLogRetentionDays
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	if ext == "" {
		ext = ".log"
		stem = path
	}

	return &RotatingWriter{
		file:          f,
		path:          path,
		stem:          stem,
		ext:           ext,
		maxSize:       maxSize,
		retentionDays: retentionDays,
		curSize:       info.Size(),
		curDay:        time.Now().Format("2006-01-02"),
		now:           time.Now,
	}, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}

	// Day rotation first: if the calendar date rolled over, archive the
	// active file under yesterday's date and start fresh.
	today := w.now().Format("2006-01-02")
	if today != w.curDay {
		w.archive(w.curDay)
		w.openActive(today)
	}

	n, err := w.file.Write(p)
	w.curSize += int64(n)

	if w.curSize > w.maxSize {
		w.archive(today)
		w.openActive(today)
		w.prune()
	}
	return n, err
}

// archive renames the active file to a date-stamped archive name. It is a
// no-op if the active file is empty/missing.
func (w *RotatingWriter) archive(day string) {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	if w.curSize == 0 {
		return
	}
	if _, err := os.Stat(w.path); err != nil {
		w.curSize = 0
		return
	}

	dest := w.archivePath(day, 0)
	// If a same-day archive already exists (e.g. multiple size rotations in
	// one day), pick the next free sequence number.
	for n := 2; ; n++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		dest = w.archivePath(day, n)
	}
	if err := os.Rename(w.path, dest); err != nil {
		slog.Warn("logrotate: rename failed", "error", err, "src", w.path, "dest", dest)
	}
	w.curSize = 0
}

func (w *RotatingWriter) archivePath(day string, seq int) string {
	if seq <= 1 {
		return fmt.Sprintf("%s-%s%s", w.stem, day, w.ext)
	}
	return fmt.Sprintf("%s-%s-%d%s", w.stem, day, seq, w.ext)
}

func (w *RotatingWriter) openActive(day string) {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		w.file = nil
		w.curSize = 0
		w.curDay = day
		return
	}
	w.file = f
	w.curSize = 0
	w.curDay = day
}

// prune removes archived logs (date-stamped siblings) older than
// retentionDays. The active file is never removed.
func (w *RotatingWriter) prune() {
	if w.retentionDays <= 0 {
		return
	}
	cutoff := w.now().AddDate(0, 0, -w.retentionDays)

	dir := filepath.Dir(w.stem)
	prefix := filepath.Base(w.stem) + "-"

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var old []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			old = append(old, filepath.Join(dir, name))
		}
	}
	sort.Strings(old)
	for _, p := range old {
		if err := os.Remove(p); err != nil {
			slog.Warn("logrotate: prune failed", "error", err, "path", p)
		}
	}
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}
