package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAttachmentsToDisk_Basic(t *testing.T) {
	workDir := t.TempDir()
	images := []ImageAttachment{
		{MimeType: "image/png", Data: []byte("png-bytes"), FileName: "shot.png"},
	}
	files := []FileAttachment{
		{MimeType: "text/plain", Data: []byte("hello"), FileName: "note.txt"},
	}
	atts := SaveAttachmentsToDisk(workDir, "s89", images, files)
	if len(atts) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(atts))
	}

	img, f := atts[0], atts[1]
	if img.Kind != "image" || img.Name != "shot.png" {
		t.Errorf("unexpected image attachment: %+v", img)
	}
	if f.Kind != "file" || f.Name != "note.txt" {
		t.Errorf("unexpected file attachment: %+v", f)
	}

	for _, a := range atts {
		if !strings.HasPrefix(a.Path, ".heron-connect/history-attachments/s89/") {
			t.Errorf("path %q missing session-scoped prefix", a.Path)
		}
		if strings.Contains(a.Path, "\\") {
			t.Errorf("path %q uses backslashes", a.Path)
		}
		abs := filepath.Join(workDir, filepath.FromSlash(a.Path))
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("expected file on disk at %q: %v", abs, err)
		}
	}
}

func TestSaveAttachmentsToDisk_Dedup(t *testing.T) {
	workDir := t.TempDir()
	first := SaveAttachmentsToDisk(workDir, "s1", []ImageAttachment{
		{MimeType: "image/png", Data: []byte("a"), FileName: "pic.png"},
	}, nil)
	second := SaveAttachmentsToDisk(workDir, "s1", []ImageAttachment{
		{MimeType: "image/png", Data: []byte("b"), FileName: "pic.png"},
	}, nil)

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("expected 1 each, got %d / %d", len(first), len(second))
	}
	if first[0].Path == second[0].Path {
		t.Errorf("expected dedup suffix, both paths are %q", first[0].Path)
	}
	if !strings.HasSuffix(second[0].Path, "pic(2).png") {
		t.Errorf("expected 'pic(2).png' suffix, got %q", second[0].Path)
	}
}

func TestSaveAttachmentsToDisk_MissingExtAndName(t *testing.T) {
	workDir := t.TempDir()
	atts := SaveAttachmentsToDisk(workDir, "s2", []ImageAttachment{
		{MimeType: "image/jpeg", Data: []byte("jpeg"), FileName: ""},
	}, nil)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if !strings.HasSuffix(atts[0].Path, ".jpg") {
		t.Errorf("expected generated .jpg extension, got %q", atts[0].Path)
	}
}

func TestSaveAttachmentsToDisk_PathTraversal(t *testing.T) {
	workDir := t.TempDir()
	atts := SaveAttachmentsToDisk(workDir, "s3", []ImageAttachment{
		{MimeType: "image/png", Data: []byte("x"), FileName: "../../evil.png"},
	}, nil)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if strings.Contains(atts[0].Path, "..") {
		t.Errorf("path %q contains traversal", atts[0].Path)
	}
	if !strings.HasSuffix(atts[0].Path, "evil.png") {
		t.Errorf("expected base name 'evil.png', got %q", atts[0].Path)
	}
}

func TestSaveAttachmentsToDisk_Empty(t *testing.T) {
	if atts := SaveAttachmentsToDisk(t.TempDir(), "s4", nil, nil); atts != nil {
		t.Errorf("expected nil for empty input, got %v", atts)
	}
}
