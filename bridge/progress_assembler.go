package bridge

import (
	"context"
	"fmt"
	"strings"

	"github.com/janostudio/heron-connect/core"
)

// bridgeProgressState holds the tool-progress lines of one streaming preview
// together with the last body the engine rendered for it.
//
// The engine rewrites the preview body wholesale on every text update (it owns
// the answer text), so tool progress cannot live inside that body: it would be
// overwritten by the next chunk. Keeping the lines here and re-attaching them
// on each update gives the Web client one message that shows both what the
// agent is doing and what it is saying.
type bridgeProgressState struct {
	lines []string
	body  string
}

var _ core.ProgressAssembler = (*BridgePlatform)(nil)

// StreamPreviewMode opts the bridge into stream previews. Without it every
// assistant text chunk degrades to its own message and the final answer lands
// on top of a near-identical one — the duplicate users reported on the Web UI.
func (bp *BridgePlatform) StreamPreviewMode() string { return "tool_hold" }

func progressKey(replyCtx any) string {
	rc, ok := replyCtx.(*bridgeReplyCtx)
	if !ok || rc == nil {
		return ""
	}
	return rc.ReplyCtx
}

// mergeToolProgress renders body together with the tool lines attached to it.
// Every UpdateMessage goes through here so the lines survive body rewrites.
func (bp *BridgePlatform) mergeToolProgress(key, body string) string {
	if key == "" {
		return body
	}
	bp.progressMu.Lock()
	defer bp.progressMu.Unlock()
	if bp.progress == nil {
		bp.progress = make(map[string]*bridgeProgressState)
	}
	state := bp.progress[key]
	if state == nil {
		state = &bridgeProgressState{}
		bp.progress[key] = state
	}
	state.body = body
	if len(state.lines) == 0 {
		return body
	}
	return strings.Join(state.lines, "\n") + "\n\n" + body
}

func (bp *BridgePlatform) clearProgress(key string) {
	if key == "" {
		return
	}
	bp.progressMu.Lock()
	delete(bp.progress, key)
	bp.progressMu.Unlock()
}

// OnToolStart appends one progress line and re-renders the preview with it.
//
// While the body is still empty the line is only recorded: pushing a preview
// that contains nothing but tool lines shows the user a bare "calling tool"
// bubble before any text exists, and the first real chunk would immediately
// replace it anyway.
func (bp *BridgePlatform) OnToolStart(ctx context.Context, previewHandle any, toolName, explainArg, rawArg string) error {
	key := progressKey(previewHandle)
	if key == "" {
		return nil
	}
	line := formatToolProgressLine(toolName, explainArg)

	bp.progressMu.Lock()
	if bp.progress == nil {
		bp.progress = make(map[string]*bridgeProgressState)
	}
	state := bp.progress[key]
	if state == nil {
		state = &bridgeProgressState{}
		bp.progress[key] = state
	}
	state.lines = append(state.lines, line)
	body := state.body
	bp.progressMu.Unlock()

	if body == "" {
		return nil
	}
	// Re-send the current body so the new line reaches the client; the merge in
	// UpdateMessage re-attaches every recorded line.
	return bp.UpdateMessage(ctx, previewHandle, body)
}

// OnToolComplete is intentionally a no-op: the line added by OnToolStart marks
// the call, and its outcome is already part of the answer text the agent writes.
func (bp *BridgePlatform) OnToolComplete(ctx context.Context, previewHandle any, toolName, resultSummary string) error {
	return nil
}

func formatToolProgressLine(toolName, explainArg string) string {
	toolName = strings.TrimSpace(toolName)
	explainArg = strings.TrimSpace(explainArg)
	switch {
	case toolName == "" && explainArg == "":
		return "·"
	case explainArg == "":
		return fmt.Sprintf("· %s", toolName)
	case toolName == "":
		return fmt.Sprintf("· %s", explainArg)
	default:
		return fmt.Sprintf("· %s %s", toolName, explainArg)
	}
}
