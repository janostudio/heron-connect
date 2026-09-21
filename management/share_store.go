package management

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/janostudio/heron-connect/core"
)

// FileShare is a capability to read one file from a project work dir without
// authentication. Holding the token is the authorization: the /api/v1/share/<token>
// endpoint is deliberately not behind the management token.
//
// The record stores the project-relative path rather than an absolute one so a
// work_dir move does not silently break (or, worse, re-point) existing links.
// FileName is a snapshot taken at creation time purely so the download can send
// a sensible Content-Disposition even if the file has since been renamed.
type FileShare struct {
	Token     string `json:"token"`
	Project   string `json:"project"`
	RelPath   string `json:"rel_path"`
	FileName  string `json:"file_name"`
	CreatedAt int64  `json:"created_at"`
}

// ShareStore persists file shares to <dataDir>/shares/shares.json.
//
// Mirrors core.CronStore: AtomicWriteFile on save, and a load that tolerates a
// missing or corrupt file rather than refusing to start — losing share links is
// annoying, but blocking startup over them is worse.
type ShareStore struct {
	mu     sync.RWMutex
	path   string
	shares map[string]*FileShare // token -> share
}

// NewShareStore creates the store rooted at dataDir (typically cfg.DataDir,
// default ~/.heron-connect).
func NewShareStore(dataDir string) (*ShareStore, error) {
	dir := filepath.Join(dataDir, "shares")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &ShareStore{
		path:   filepath.Join(dir, "shares.json"),
		shares: make(map[string]*FileShare),
	}
	s.load()
	return s, nil
}

func (s *ShareStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var shares []*FileShare
	if err := json.Unmarshal(data, &shares); err != nil {
		slog.Error("share: failed to load shares", "path", s.path, "error", err)
		return
	}
	for _, sh := range shares {
		if sh == nil || sh.Token == "" {
			continue
		}
		s.shares[sh.Token] = sh
	}
}

func (s *ShareStore) save() error {
	s.mu.RLock()
	shares := make([]*FileShare, 0, len(s.shares))
	for _, sh := range s.shares {
		shares = append(shares, sh)
	}
	s.mu.RUnlock()

	// Stable ordering keeps the file diff-friendly and avoids churn.
	sort.Slice(shares, func(i, j int) bool {
		if shares[i].CreatedAt != shares[j].CreatedAt {
			return shares[i].CreatedAt < shares[j].CreatedAt
		}
		return shares[i].Token < shares[j].Token
	})

	data, err := json.MarshalIndent(shares, "", "  ")
	if err != nil {
		return err
	}
	return core.AtomicWriteFile(s.path, data, 0o644)
}

// Create mints a share for one file and persists it. relPath must already be
// a project-relative, slash-separated path as served by the files endpoint;
// the caller is responsible for having validated that the file exists.
func (s *ShareStore) Create(project, relPath, fileName string) (*FileShare, error) {
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "/")

	sh := &FileShare{
		Token:     core.GenerateToken(16),
		Project:   project,
		RelPath:   relPath,
		FileName:  fileName,
		CreatedAt: time.Now().Unix(),
	}

	s.mu.Lock()
	s.shares[sh.Token] = sh
	s.mu.Unlock()

	if err := s.save(); err != nil {
		// Roll back so a failed write cannot leave an in-memory link that
		// survives only until the next restart.
		s.mu.Lock()
		delete(s.shares, sh.Token)
		s.mu.Unlock()
		return nil, err
	}
	return sh, nil
}

// FindByPath returns the share already minted for a project-relative path, if
// any. Creating a share is idempotent: handing out a second token for the same
// file would leave the caller holding a link they cannot tell apart from the
// first, and revoking either one would look like a no-op bug.
//
// Returns the oldest matching share so repeated calls stay stable even if
// duplicate records exist from an earlier version.
func (s *ShareStore) FindByPath(project, relPath string) (*FileShare, bool) {
	relPath = strings.TrimPrefix(filepath.ToSlash(relPath), "/")
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found *FileShare
	for _, sh := range s.shares {
		if sh.Project != project || sh.RelPath != relPath {
			continue
		}
		if found == nil || sh.CreatedAt < found.CreatedAt {
			found = sh
		}
	}
	return found, found != nil
}

// CreateOrReuse returns the existing share for a file, or mints one.
//
// Sharing is idempotent by design: the same file handed out twice would give
// the caller two indistinguishable links, and revoking one would leave the
// other silently working. reused reports which happened so callers can say so.
func (s *ShareStore) CreateOrReuse(project, relPath, fileName string) (sh *FileShare, reused bool, err error) {
	if existing, ok := s.FindByPath(project, relPath); ok {
		return existing, true, nil
	}
	sh, err = s.Create(project, relPath, fileName)
	return sh, false, err
}

// Get returns the share for token, if any.
func (s *ShareStore) Get(token string) (*FileShare, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sh, ok := s.shares[token]
	return sh, ok
}

// Revoke deletes a share. Returns false when the token was already unknown.
func (s *ShareStore) Revoke(token string) bool {
	s.mu.Lock()
	_, ok := s.shares[token]
	if ok {
		delete(s.shares, token)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	if err := s.save(); err != nil {
		slog.Error("share: failed to persist revoke", "token", token, "error", err)
	}
	return true
}

// ListByProject returns every share for a project, newest first. An empty
// project returns all shares.
func (s *ShareStore) ListByProject(project string) []*FileShare {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*FileShare, 0, len(s.shares))
	for _, sh := range s.shares {
		if project != "" && sh.Project != project {
			continue
		}
		out = append(out, sh)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].Token < out[j].Token
	})
	return out
}
