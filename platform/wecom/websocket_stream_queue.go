package wecom

import (
	"context"
	"log/slog"
)

func (p *WSPlatform) enqueueLatestStreamSend(ctx context.Context, key string, state *wsStreamState, rc wsReplyContext, content string, finish bool) error {
	req := &wsStreamSend{content: content, finish: finish, done: make(chan error, 1)}
	var superseded *wsStreamSend

	state.mu.Lock()
	if !finish && state.lastAcked == content {
		state.mu.Unlock()
		return nil
	}
	// Note: shouldHoldOnlyTool/holdTool logic was removed because tool_hold is now
	// driven explicitly by the engine via ProgressAssembler.OnToolStart/OnToolComplete.
	// The queue no longer needs to guess whether content is a tool-only block.
	if pending := state.pending; pending != nil {
		if finish || !pending.finish {
			superseded = pending
			state.pending = req
		} else {
			state.mu.Unlock()
			return nil
		}
		state.mu.Unlock()
		if superseded != nil {
			superseded.done <- nil
			close(superseded.done)
		}
		select {
		case err := <-req.done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state.pending = req
	state.completed = false
	if state.running {
		state.mu.Unlock()
		select {
		case err := <-req.done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state.running = true
	state.mu.Unlock()

	go p.runStreamQueue(key, state, rc)

	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *WSPlatform) runStreamQueue(key string, state *wsStreamState, rc wsReplyContext) {
	for {
		state.mu.Lock()
		req := state.pending
		state.pending = nil
		state.mu.Unlock()

		if req == nil {
			state.mu.Lock()
			state.running = false
			idle := state.pending == nil
			completed := state.completed
			state.mu.Unlock()
			if idle {
				if completed {
					p.streamMu.Lock()
					delete(p.streamState, key)
					p.streamMu.Unlock()
				}
				return
			}
			state.mu.Lock()
			if !state.running {
				state.running = true
			}
			state.mu.Unlock()
			continue
		}

		state.mu.Lock()
		// New path: content from UpdateMessage/FinalizePreviewMessage is already
		// the final rendered output from wecomStreamAssembler. We no longer run
		// the old wsStreamAssembler.ingest() here — that caused double processing
		// (new assembler merges in UpdateMessage, old assembler merges again here).
		rendered := req.content
		shouldSend := true
		lastAckedMatches := !req.finish && state.lastAcked == rendered
		state.mu.Unlock()

		if !shouldSend {
			req.done <- nil
			close(req.done)
			continue
		}

		slog.Info("wecom-ws: stream aggregate", "key", key, "finish", req.finish, "content", truncateWecomLogBody(rendered))

		if lastAckedMatches {
			slog.Info("wecom-ws: stream skip duplicate", "key", key, "finish", req.finish, "content", truncateWecomLogBody(rendered))
			req.done <- nil
			close(req.done)
			continue
		}

		reqID, frame, err := p.buildStreamFrame(rc, rendered, req.finish)
		if err == nil {
			err = p.writeAndWaitAck(context.Background(), frame, reqID)
		}
		if err == nil && !req.finish {
			state.mu.Lock()
			state.lastAcked = rendered
			state.mu.Unlock()
		}
		if req.finish && err == nil {
			state.mu.Lock()
			state.lastAcked = rendered
			state.assembler.reset()
			if state.wecomAssembler != nil {
				state.wecomAssembler.reset()
			}
			state.completed = true
			state.mu.Unlock()
		}
		req.done <- err
		close(req.done)
	}
}
