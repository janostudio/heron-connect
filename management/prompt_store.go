package management

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/janostudio/heron-connect/core"
)

// Limits on quick prompts. They exist to bound the on-disk JSON and the height
// of the list rendered above the composer, not to police wording: a prompt is a
// message body the user could have typed anyway.
const (
	QuickPromptMaxRunes      = 2000
	QuickPromptMaxPerProject = 50
)

var (
	ErrQuickPromptNotFound = errors.New("quick prompt not found")
	ErrQuickPromptEmpty    = errors.New("quick prompt content is required")
	ErrQuickPromptTooLong  = fmt.Errorf("quick prompt exceeds %d characters", QuickPromptMaxRunes)
	ErrQuickPromptLimit    = fmt.Errorf("a project can hold at most %d quick prompts", QuickPromptMaxPerProject)
)

// QuickPrompt is one saved prompt of a project. Prompts belong to the project,
// not to a conversation: the same list shows up in every conversation of that
// project and nowhere else.
//
// There is deliberately no title field. The row in the composer is one line of
// the content itself, which keeps creating a prompt to a single text field.
type QuickPrompt struct {
	ID        string    `json:"id"`
	Project   string    `json:"project"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PromptStore persists quick prompts to <dataDir>/prompts/prompts.json.
//
// Mirrors ShareStore: a single JSON file, AtomicWriteFile on every mutation,
// and a load that tolerates a missing file rather than refusing to start.
// Prompt text is user-authored content, so a corrupt store is moved aside
// instead of overwritten — a rename keeps the bytes recoverable by hand, while
// the next successful save would have destroyed them.
//
// The store is process-local state for the management API only; the engine
// never reads it, which is why this lives in management/ rather than core/.
type PromptStore struct {
	mu      sync.RWMutex
	path    string
	prompts []QuickPrompt // sorted by CreatedAt, then ID
}

// NewPromptStore creates the store rooted at dataDir (typically cfg.DataDir,
// default ~/.heron-connect).
func NewPromptStore(dataDir string) (*PromptStore, error) {
	dir := filepath.Join(dataDir, "prompts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &PromptStore{path: filepath.Join(dir, "prompts.json")}
	s.load()
	return s, nil
}

func (s *PromptStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Error("prompts: failed to read store", "path", s.path, "error", err)
		}
		return
	}

	var stored []QuickPrompt
	if err := json.Unmarshal(data, &stored); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%d", s.path, time.Now().Unix())
		if renameErr := os.Rename(s.path, backup); renameErr != nil {
			slog.Error("prompts: failed to back up unreadable store",
				"path", s.path, "error", renameErr)
		}
		slog.Error("prompts: failed to parse store; starting empty",
			"path", s.path, "error", err, "backup", backup)
		return
	}

	dropped := 0
	for _, p := range stored {
		if strings.TrimSpace(p.ID) == "" || strings.TrimSpace(p.Project) == "" ||
			strings.TrimSpace(p.Content) == "" {
			dropped++
			continue
		}
		s.prompts = append(s.prompts, p)
	}
	// Silently dropping records would look like prompts vanishing; the file is
	// left untouched so the data is still there for inspection, but the log has
	// to say it happened.
	if dropped > 0 {
		slog.Warn("prompts: skipped records with missing id/project/content",
			"path", s.path, "dropped", dropped, "loaded", len(s.prompts))
	}
	sortQuickPrompts(s.prompts)
}

// saveLocked writes the store. Callers must hold the write lock; the copy is
// taken so a concurrent marshal cannot observe a half-applied mutation if this
// is ever called without the lock held.
func (s *PromptStore) saveLocked() error {
	sorted := make([]QuickPrompt, len(s.prompts))
	copy(sorted, s.prompts)

	data, err := json.MarshalIndent(sorted, "", "  ")
	if err != nil {
		return err
	}
	if err := core.AtomicWriteFile(s.path, data, 0o644); err != nil {
		return err
	}
	return nil
}

func sortQuickPrompts(list []QuickPrompt) {
	sort.Slice(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.Before(list[j].CreatedAt)
		}
		return list[i].ID < list[j].ID
	})
}

// List returns the project's prompts in creation order. A copy is returned so
// callers cannot mutate the store's slice.
func (s *PromptStore) List(project string) []QuickPrompt {
	project = strings.TrimSpace(project)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]QuickPrompt, 0, len(s.prompts))
	for _, p := range s.prompts {
		if p.Project == project {
			out = append(out, p)
		}
	}
	return out
}

// Create appends a prompt to the project and persists it.
func (s *PromptStore) Create(project, content string) (QuickPrompt, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return QuickPrompt{}, fmt.Errorf("project is required")
	}
	content, err := normalizeQuickPromptContent(content)
	if err != nil {
		return QuickPrompt{}, err
	}

	now := time.Now().UTC()
	p := QuickPrompt{
		ID:        "qp_" + core.GenerateToken(8),
		Project:   project,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, existing := range s.prompts {
		if existing.Project == project {
			count++
		}
	}
	if count >= QuickPromptMaxPerProject {
		return QuickPrompt{}, ErrQuickPromptLimit
	}

	s.prompts = append(s.prompts, p)
	if err := s.saveLocked(); err != nil {
		// Roll back: a prompt that only exists in memory would disappear on
		// the next restart, which reads as data loss to the user.
		s.prompts = s.prompts[:len(s.prompts)-1]
		return QuickPrompt{}, err
	}
	return p, nil
}

// Update replaces one prompt's content. The project must match the stored
// record: an ID from another project reports "not found" rather than editing a
// prompt the caller cannot see.
func (s *PromptStore) Update(project, id, content string) (QuickPrompt, error) {
	project = strings.TrimSpace(project)
	id = strings.TrimSpace(id)
	if project == "" || id == "" {
		return QuickPrompt{}, ErrQuickPromptNotFound
	}
	content, err := normalizeQuickPromptContent(content)
	if err != nil {
		return QuickPrompt{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.indexOfLocked(project, id)
	if idx < 0 {
		return QuickPrompt{}, ErrQuickPromptNotFound
	}

	prev := s.prompts[idx]
	updated := prev
	updated.Content = content
	updated.UpdatedAt = time.Now().UTC()
	s.prompts[idx] = updated
	if err := s.saveLocked(); err != nil {
		s.prompts[idx] = prev
		return QuickPrompt{}, err
	}
	return updated, nil
}

// Delete removes one prompt from the project.
func (s *PromptStore) Delete(project, id string) error {
	project = strings.TrimSpace(project)
	id = strings.TrimSpace(id)
	if project == "" || id == "" {
		return ErrQuickPromptNotFound
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.indexOfLocked(project, id)
	if idx < 0 {
		return ErrQuickPromptNotFound
	}

	prev := make([]QuickPrompt, len(s.prompts))
	copy(prev, s.prompts)
	s.prompts = append(s.prompts[:idx], s.prompts[idx+1:]...)
	if err := s.saveLocked(); err != nil {
		s.prompts = prev
		return err
	}
	return nil
}

func (s *PromptStore) indexOfLocked(project, id string) int {
	for i, p := range s.prompts {
		if p.Project == project && p.ID == id {
			return i
		}
	}
	return -1
}

// normalizeQuickPromptContent trims the content and enforces the size limits.
// Trimming happens at the boundary so a prompt saved with trailing newlines
// sends the same text the user wrote.
func normalizeQuickPromptContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", ErrQuickPromptEmpty
	}
	if utf8.RuneCountInString(content) > QuickPromptMaxRunes {
		return "", ErrQuickPromptTooLong
	}
	return content, nil
}
