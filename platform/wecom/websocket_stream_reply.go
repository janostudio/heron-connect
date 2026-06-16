package wecom

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/chenhg5/cc-connect/core"
)

// Reply sends a response message via aibot_respond_msg using the stream format.
// Uses the req_id from the original callback.
// The stream content field is a full-replacement (not incremental append), so we
// send the complete content in one frame with finish=true.
// Markdown is natively supported by the stream reply format.
func (p *WSPlatform) Reply(ctx context.Context, rctx any, content string) error {
	rc, ok := rctx.(wsReplyContext)
	if !ok {
		return fmt.Errorf("wecom-ws: invalid reply context type %T", rctx)
	}
	if content == "" {
		return nil
	}

	if err := p.sendFinalReplyChunks(ctx, rc, content); err != nil {
		slog.Error("wecom-ws: reply failed", "user", rc.userID, "error", err)
		return err
	}
	slog.Debug("wecom-ws: reply sent", "user", rc.userID, "len", len(content))
	return nil
}

func (p *WSPlatform) SendPreviewStart(ctx context.Context, rctx any, content string) (any, error) {
	rc, ok := rctx.(wsReplyContext)
	if !ok {
		return nil, fmt.Errorf("wecom-ws: invalid reply context type %T", rctx)
	}
	if rc.reqID == "" {
		return nil, core.ErrNotSupported
	}
	handle := &wsPreviewHandle{replyCtx: rc}
	previewContent := wecomPreviewPayload(content)
	handle.replyCtx.streamID = p.generateReqID("stream")
	if err := p.sendStreamFrameAndWaitAck(ctx, handle.replyCtx, previewContent, false); err != nil {
		return nil, err
	}
	return handle, nil
}

func (p *WSPlatform) UpdateMessage(ctx context.Context, previewHandle any, content string) error {
	h, ok := previewHandle.(*wsPreviewHandle)
	if !ok {
		return fmt.Errorf("wecom-ws: invalid preview handle type %T", previewHandle)
	}
	if h.replyCtx.streamID == "" {
		return fmt.Errorf("wecom-ws: preview handle missing stream id")
	}
	return p.sendStreamFrameAndWaitAck(ctx, h.replyCtx, wecomPreviewPayload(content), false)
}

func (p *WSPlatform) FinalizePreviewMessage(ctx context.Context, previewHandle any, content string) error {
	h, ok := previewHandle.(*wsPreviewHandle)
	if !ok {
		return fmt.Errorf("wecom-ws: invalid preview handle type %T", previewHandle)
	}
	if h.replyCtx.streamID == "" {
		return fmt.Errorf("wecom-ws: preview handle missing stream id")
	}
	if content == "" {
		return nil
	}
	return p.sendFinalReplyChunks(ctx, h.replyCtx, content)
}

func (p *WSPlatform) sendFinalReplyChunks(ctx context.Context, rc wsReplyContext, content string) error {
	chunks := splitByBytes(content, wecomStreamMaxBytes)
	if len(chunks) == 0 {
		return nil
	}
	if len(chunks) == 1 {
		return p.sendStreamFrameAndWaitAck(ctx, rc, chunks[0], true)
	}
	finalRC := rc
	if finalRC.streamID == "" {
		finalRC.streamID = p.generateReqID("stream")
	}
	if err := p.sendStreamFrameAndWaitAck(ctx, finalRC, chunks[0], true); err != nil {
		return err
	}
	for i := 1; i < len(chunks); i++ {
		if err := p.Send(ctx, rc, chunks[i]); err != nil {
			return err
		}
	}
	return nil
}

func wecomPreviewPayload(content string) string {
	chunks := splitByBytes(content, wecomStreamMaxBytes)
	if len(chunks) == 0 {
		return ""
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	notice := "\n\n[内容较长，正在整理后续片段...]"
	headMax := wecomStreamMaxBytes - len([]byte(notice))
	if headMax <= 0 {
		return splitByBytes(notice, wecomStreamMaxBytes)[0]
	}
	head := splitByBytes(chunks[0], headMax)
	if len(head) == 0 {
		return splitByBytes(notice, wecomStreamMaxBytes)[0]
	}
	return head[0] + notice
}

func (p *WSPlatform) sendStreamFrameAndWaitAck(ctx context.Context, rc wsReplyContext, content string, finish bool) error {
	slog.Info("wecom-ws: stream enqueue",
		"user", rc.userID,
		"stream_id", rc.streamID,
		"finish", finish,
		"content", truncateWecomLogBody(content),
	)
	key, state, err := p.streamStateFor(rc)
	if err != nil {
		return err
	}
	return p.enqueueLatestStreamSend(ctx, key, state, rc, content, finish)
}

func (p *WSPlatform) streamStateFor(rc wsReplyContext) (string, *wsStreamState, error) {
	if rc.reqID == "" {
		return "", nil, fmt.Errorf("wecom-ws: reqID is empty, cannot send stream reply")
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
	return key, state, nil
}
