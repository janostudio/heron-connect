package core

import (
	"context"
	"sync"
	"testing"
	"time"
)

// teamLabelledPlatform records the Team label a background reply carries.
// Only the label-aware path is of interest here, so it embeds the ordinary stub
// and adds SendWithTeam.
type teamLabelledPlatform struct {
	*stubPlatformEngine

	mu       sync.Mutex
	teams    []string
	labelled []string
}

func (p *teamLabelledPlatform) SendWithTeam(_ context.Context, _ any, team, content string) error {
	p.mu.Lock()
	p.teams = append(p.teams, team)
	p.labelled = append(p.labelled, content)
	p.mu.Unlock()
	return nil
}

func (p *teamLabelledPlatform) getLabelled() ([]string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.teams...), append([]string(nil), p.labelled...)
}

// A background turn's answer must name the Team that wrote it.
//
// The engine already puts the writer on the event ("team" in metadata) and the
// bridge already renders it, but the unsolicited reader sent the text through
// the plain send path: the label was dropped on the way, so a client showing a
// sub-team's result could not say whose result it was (A→B fixture,
// 2026-10-28).
func TestUnsolicitedReader_LabelsReplyWithTeam(t *testing.T) {
	p := &teamLabelledPlatform{stubPlatformEngine: &stubPlatformEngine{n: "test"}}
	sess := newControllableSession("unsol-team-label")
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	defer e.Stop()

	sessions := e.sessions
	session := sessions.GetOrCreateActive("test:ch1:u1")

	state := &interactiveState{
		agentSession:     sess,
		platform:         p,
		replyCtx:         "ctx",
		eventsNeedResync: false,
	}
	iKey := "test:ch1:u1"
	e.interactiveMu.Lock()
	e.interactiveStates[iKey] = state
	e.interactiveMu.Unlock()

	e.startUnsolicitedReader(state, session, sessions, iKey, "")
	defer e.stopUnsolicitedReader(state)

	sess.events <- Event{
		Type:     EventResult,
		Content:  "版本号是 v9.9.9。",
		Metadata: map[string]any{"team": "worker"},
	}

	deadline := time.After(5 * time.Second)
	for {
		teams, texts := p.getLabelled()
		if len(teams) > 0 {
			if teams[0] != "worker" {
				t.Fatalf("background reply was labelled %q, want worker", teams[0])
			}
			if texts[0] != "版本号是 v9.9.9。" {
				t.Fatalf("labelled content = %q", texts[0])
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("background reply reached the platform without a Team label (sent=%v)", p.getSent())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
