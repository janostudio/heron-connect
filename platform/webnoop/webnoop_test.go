package webnoop

import (
	"context"
	"testing"

	"github.com/janostudio/heron-connect/core"
)

func TestPlatform_implementsCorePlatform(t *testing.T) {
	var _ core.Platform = (*Platform)(nil)
}

func TestNew(t *testing.T) {
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if p.Name() != "web" {
		t.Errorf("Name() = %q, want %q", p.Name(), "web")
	}
}

func TestPlatform_StartDoesNotInvokeHandler(t *testing.T) {
	p, _ := New(nil)
	called := false
	if err := p.Start(func(core.Platform, *core.Message) {
		called = true
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if called {
		t.Error("Start() should never invoke the handler (no real transport)")
	}
}

func TestPlatform_NoOpMethods(t *testing.T) {
	p, _ := New(nil)
	if err := p.Stop(); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
	if err := p.Reply(context.Background(), nil, "hello"); err != nil {
		t.Errorf("Reply() error = %v", err)
	}
	if err := p.Send(context.Background(), nil, "hello"); err != nil {
		t.Errorf("Send() error = %v", err)
	}
}

func TestRegistered(t *testing.T) {
	p, err := core.CreatePlatform("web", nil)
	if err != nil {
		t.Fatalf("CreatePlatform(\"web\", nil) error = %v", err)
	}
	if p.Name() != "web" {
		t.Errorf("Name() = %q, want %q", p.Name(), "web")
	}
}
