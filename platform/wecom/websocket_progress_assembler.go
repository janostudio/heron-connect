package wecom

import (
	"context"
	"fmt"
	"log/slog"
)

// OnToolStart implements core.ProgressAssembler. It routes a tool-start event
// into the three-region wecomStreamAssembler's progressLines, then triggers a
// merged UpdateMessage so the preview shows tool progress + current visible text
// together (instead of a standalone tool-only frame that would flicker).
func (p *WSPlatform) OnToolStart(ctx context.Context, previewHandle any, toolName, explainArg, rawArg string) error {
	h, ok := previewHandle.(*wsPreviewHandle)
	if !ok || h == nil {
		return nil
	}
	state, err := p.wecomAssemblerFor(h.replyCtx)
	if err != nil {
		slog.Debug("wecom-ws: OnToolStart no assembler", "error", err)
		return nil
	}
	state.onToolStart(toolName, explainArg, rawArg)
	// Re-send the current visibleText via UpdateMessage. This merges progressLines
	// with visibleText in the rendered output. If visibleText is empty, we skip
	// the frame to avoid a standalone tool-only preview (it will be sent when the
	// first visible text arrives).
	return p.sendMergedPreview(ctx, h, state)
}

// OnToolComplete implements core.ProgressAssembler. It adds a completion row
// to progressLines without touching visibleText, then sends a merged preview.
func (p *WSPlatform) OnToolComplete(ctx context.Context, previewHandle any, toolName, resultSummary string) error {
	h, ok := previewHandle.(*wsPreviewHandle)
	if !ok || h == nil {
		return nil
	}
	state, err := p.wecomAssemblerFor(h.replyCtx)
	if err != nil {
		slog.Debug("wecom-ws: OnToolComplete no assembler", "error", err)
		return nil
	}
	state.onToolComplete(toolName, resultSummary)
	return p.sendMergedPreview(ctx, h, state)
}

// sendMergedPreview sends the assembler's current render (progress + visible text)
// to the preview message. If visibleText is empty AND there are progress lines,
// we skip the frame to avoid a standalone tool-only preview — the progress will
// be included when the first visible text arrives.
func (p *WSPlatform) sendMergedPreview(ctx context.Context, h *wsPreviewHandle, state *wecomStreamAssembler) error {
	rendered := state.snapshot()
	if rendered == "" {
		return nil
	}
	// If only progress lines (no visible text yet), skip to avoid flicker.
	// The progress will be merged when the first text arrives via UpdateMessage.
	state.mu.Lock()
	hasVisible := state.visibleText != ""
	state.mu.Unlock()
	if !hasVisible {
		return nil
	}
	return p.sendStreamFrameAndWaitAck(ctx, h.replyCtx, wecomPreviewPayload(rendered), false)
}

// wecomAssemblerFor returns the wecomStreamAssembler for the given reply context,
// creating it lazily inside the stream state if needed.
func (p *WSPlatform) wecomAssemblerFor(rc wsReplyContext) (*wecomStreamAssembler, error) {
	if rc.reqID == "" {
		return nil, fmt.Errorf("wecom-ws: reqID is empty")
	}
	streamID := rc.streamID
	if streamID == "" {
		streamID = "reply"
	}
	key := rc.reqID + ":" + streamID

	p.streamMu.Lock()
	defer p.streamMu.Unlock()
	if p.streamState == nil {
		p.streamState = make(map[string]*wsStreamState)
	}
	state := p.streamState[key]
	if state == nil {
		state = &wsStreamState{}
		p.streamState[key] = state
	}
	if state.wecomAssembler == nil {
		state.wecomAssembler = newWecomStreamAssembler()
	}
	return state.wecomAssembler, nil
}
