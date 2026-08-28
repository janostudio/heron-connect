package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupDemoDir creates a temp workspace with a few files and returns the dir.
func setupDemoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"out/report.md":   "# report\n",
		"src/app.ts":      "export {}",
		"y.go":            "package main",
		"x.txt":           "hello",
		"real.md":         "x",
		"my docs/note.md": "note",
	}
	for path, content := range files {
		p := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestTransformLocalRefsToLinks_EmptyProjectName(t *testing.T) {
	dir := setupDemoDir(t)
	in := filepath.Join(dir, "y.go")
	if got := TransformLocalRefsToLinks(in, "", dir); got != in {
		t.Fatalf("expected unchanged when project empty, got %q", got)
	}
}

func TestTransformLocalRefsToLinks_AbsolutePath(t *testing.T) {
	dir := setupDemoDir(t)
	in := "generated " + filepath.Join(dir, "out", "report.md")
	got := TransformLocalRefsToLinks(in, "proj", dir)
	want := "generated [out/report.md](/api/v1/files/proj/out/report.md)"
	if got != want {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want %q", got, want)
	}
}

func TestTransformLocalRefsToLinks_RelativeAndIncompletePath(t *testing.T) {
	dir := setupDemoDir(t)
	in := "check out src/app.ts"
	got := TransformLocalRefsToLinks(in, "proj", dir)
	want := "check out [src/app.ts](/api/v1/files/proj/src/app.ts)"
	if got != want {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want %q", got, want)
	}
}

func TestTransformLocalRefsToLinks_InlineCode(t *testing.T) {
	dir := setupDemoDir(t)
	in := "see `out/report.md`"
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if !strings.Contains(got, "[out/report.md](/api/v1/files/proj/out/report.md)") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want inline code linkified", got)
	}
}

func TestTransformLocalRefsToLinks_PreservesFencedCode(t *testing.T) {
	dir := setupDemoDir(t)
	in := "```\nnot/a/path but code\n```\nafter " + filepath.Join(dir, "x.txt")
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if !strings.Contains(got, "```\nnot/a/path but code\n```") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want fenced code preserved", got)
	}
	if !strings.Contains(got, "[/root/code/demo/x.txt](/api/v1/files/proj/x.txt)") && !strings.Contains(got, "[x.txt](/api/v1/files/proj/x.txt)") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want file after fence linkified", got)
	}
}

func TestTransformLocalRefsToLinks_PreservesWebLink(t *testing.T) {
	dir := setupDemoDir(t)
	in := "see [OpenAI](https://openai.com/) and " + filepath.Join(dir, "y.go")
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if !strings.Contains(got, "[OpenAI](https://openai.com/)") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want web link preserved", got)
	}
	if !strings.Contains(got, "[y.go](/api/v1/files/proj/y.go)") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want local file linkified", got)
	}
}

func TestTransformLocalRefsToLinks_DoesNotLinkifyDirs(t *testing.T) {
	dir := setupDemoDir(t)
	in := "see " + filepath.Join(dir, "out") + string(os.PathSeparator)
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if strings.Contains(got, "/api/v1/files/") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, dir should not be linkified", got)
	}
}

func TestTransformLocalRefsToLinks_DoesNotLinkifyNonexistent(t *testing.T) {
	dir := setupDemoDir(t)
	in := "missing " + filepath.Join(dir, "nope.txt") + " exists " + filepath.Join(dir, "real.md")
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if strings.Contains(got, "nope.txt](/api/v1/files/") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, nonexistent file should not be linkified", got)
	}
	if !strings.Contains(got, "real.md](/api/v1/files/proj/real.md") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, existing file should be linkified", got)
	}
}

func TestTransformLocalRefsToLinks_BasenameUniqueMatchFillsPrefix(t *testing.T) {
	dir := setupDemoDir(t)
	// src/app.ts exists; agent writes just the bare basename "app.ts".
	in := "see app.ts"
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if !strings.Contains(got, "[src/app.ts](/api/v1/files/proj/src/app.ts)") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, want basename fallback to unique src/app.ts", got)
	}
}

func TestTransformLocalRefsToLinks_BasenameAmbiguousNotLinked(t *testing.T) {
	dir := setupDemoDir(t)
	// Two files share the basename "note.md" -> ambiguous, must NOT be linked.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// setupDemoDir already created "my docs/note.md".
	in := "see note.md"
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if strings.Contains(got, "/api/v1/files/") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, ambiguous basename should not be linkified", got)
	}
}

func TestTransformLocalRefsToLinks_BasenameNoMatchNotLinked(t *testing.T) {
	dir := setupDemoDir(t)
	in := "see ghost.md"
	got := TransformLocalRefsToLinks(in, "proj", dir)
	if strings.Contains(got, "/api/v1/files/") {
		t.Fatalf("TransformLocalRefsToLinks() = %q, unmatched basename should not be linkified", got)
	}
}
