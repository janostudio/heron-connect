package wecom

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// captureWSFramesCapture returns a writeJSONFn that captures all sent frames.
func captureWSFramesCapture(frames *[]map[string]any, mu *sync.Mutex) func(any) error {
	return func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var frame map[string]any
		if err := json.Unmarshal(b, &frame); err != nil {
			return err
		}
		mu.Lock()
		*frames = append(*frames, frame)
		mu.Unlock()
		return nil
	}
}

// ackReqLoopCapture starts a goroutine that auto-acks any req_id the test sees.
func ackReqLoopCapture(p *WSPlatform) func() {
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				p.pendingAcks.Range(func(key, value any) bool {
					if ch, ok := value.(chan error); ok {
						select {
						case ch <- nil:
						default:
						}
						p.pendingAcks.Delete(key)
					}
					return true
				})
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
	return func() { close(stop) }
}

// --- Integration: UpdateMessage merges progressLines with visibleText ---

// TestUpdateMessage_MergesProgressAndVisibleText verifies the core打通 contract:
// when UpdateMessage receives visible text, and the assembler has progressLines,
// the sent frame contains BOTH (progress + separator + visible text).
func TestUpdateMessage_MergesProgressAndVisibleText(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: captureWSFramesCapture(&frames, &mu), streamState: make(map[string]*wsStreamState)}
	stopAck := ackReqLoopCapture(p)
	defer stopAck()

	rc := wsReplyContext{reqID: "req_merge", userID: "user_1", streamID: "stream_merge"}
	handle := &wsPreviewHandle{replyCtx: rc}

	// Step 1: OnToolStart adds a progress line (no visible text yet)
	if err := p.OnToolStart(context.Background(), handle, "Bash", "run tests", "npm test"); err != nil {
		t.Fatalf("OnToolStart failed: %v", err)
	}

	// Step 2: UpdateMessage receives visible text
	if err := p.UpdateMessage(context.Background(), handle, "answer body"); err != nil {
		t.Fatalf("UpdateMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) == 0 {
		t.Fatal("no frames sent")
	}

	// The last frame should contain both tool progress and visible text
	lastFrame := frames[len(frames)-1]
	stream := lastFrame["body"].(map[string]any)["stream"].(map[string]any)
	content := stream["content"].(string)

	if !contains(content, "🛠️") {
		t.Fatalf("frame content = %q, want contain tool progress (🛠️)", content)
	}
	if !contains(content, "answer body") {
		t.Fatalf("frame content = %q, want contain visible text 'answer body'", content)
	}
}

// TestOnToolStart_DoesNotSendStandaloneFrame verifies that OnToolStart does NOT
// independently send a frame when visibleText is empty. The progress line should
// only be sent when the next UpdateMessage (with visible text) triggers a merge.
//
// This is the key to avoiding "alternating flicker" between progress and text.
func TestOnToolStart_DoesNotSendStandaloneFrameWhenNoVisibleText(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: captureWSFramesCapture(&frames, &mu), streamState: make(map[string]*wsStreamState)}
	stopAck := ackReqLoopCapture(p)
	defer stopAck()

	rc := wsReplyContext{reqID: "req_notool", userID: "user_1", streamID: "stream_notool"}
	handle := &wsPreviewHandle{replyCtx: rc}

	// OnToolStart with no prior visible text — should NOT send a frame
	if err := p.OnToolStart(context.Background(), handle, "Bash", "run tests", ""); err != nil {
		t.Fatalf("OnToolStart failed: %v", err)
	}

	// Give the ack loop time to process
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 0 {
		t.Fatalf("frames sent = %d, want 0 (OnToolStart should not send standalone frame when no visible text), frames=%#v", len(frames), frames)
	}
}

// TestFinalizePreviewMessage_ClearsProgressAndSendsOnlyAnswer verifies that
// finalize clears progressLines and the final frame contains ONLY the answer
// text (no tool progress lines).
func TestFinalizePreviewMessage_ClearsProgressAndSendsOnlyAnswer(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: captureWSFramesCapture(&frames, &mu), streamState: make(map[string]*wsStreamState)}
	stopAck := ackReqLoopCapture(p)
	defer stopAck()

	rc := wsReplyContext{reqID: "req_final", userID: "user_1", streamID: "stream_final"}
	handle := &wsPreviewHandle{replyCtx: rc}

	// 1. Add tool progress + visible text
	p.OnToolStart(context.Background(), handle, "Bash", "run tests", "npm test")
	p.UpdateMessage(context.Background(), handle, "partial answer")

	// 2. Finalize with the final answer
	if err := p.FinalizePreviewMessage(context.Background(), handle, "final answer"); err != nil {
		t.Fatalf("FinalizePreviewMessage failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) == 0 {
		t.Fatal("no frames sent")
	}

	// The last frame (finalize) should contain ONLY the answer, no tool progress
	lastFrame := frames[len(frames)-1]
	stream := lastFrame["body"].(map[string]any)["stream"].(map[string]any)
	content := stream["content"].(string)
	finish := stream["finish"].(bool)

	if !finish {
		t.Fatalf("finalize frame finish = false, want true")
	}
	if content != "final answer" {
		t.Fatalf("finalize frame content = %q, want 'final answer' (no tool progress)", content)
	}
}

// TestUpdateMessage_TracksVisibleTextInAssembler verifies that UpdateMessage
// writes the received text into the assembler's visibleText, so that a
// subsequent OnToolStart can merge with the latest visible text.
func TestUpdateMessage_TracksVisibleTextInAssembler(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: captureWSFramesCapture(&frames, &mu), streamState: make(map[string]*wsStreamState)}
	stopAck := ackReqLoopCapture(p)
	defer stopAck()

	rc := wsReplyContext{reqID: "req_track", userID: "user_1", streamID: "stream_track"}
	handle := &wsPreviewHandle{replyCtx: rc}

	// 1. UpdateMessage with visible text "hello"
	p.UpdateMessage(context.Background(), handle, "hello")

	// 2. OnToolStart — should merge tool progress with "hello"
	p.OnToolStart(context.Background(), handle, "Bash", "run", "cmd")

	mu.Lock()
	defer mu.Unlock()
	if len(frames) < 2 {
		t.Fatalf("frames = %d, want >= 2", len(frames))
	}

	// Last frame should contain both "hello" and tool progress
	lastFrame := frames[len(frames)-1]
	stream := lastFrame["body"].(map[string]any)["stream"].(map[string]any)
	content := stream["content"].(string)

	if !contains(content, "hello") {
		t.Fatalf("frame content = %q, want contain 'hello' (visible text tracked across calls)", content)
	}
	if !contains(content, "🛠️") {
		t.Fatalf("frame content = %q, want contain tool progress", content)
	}
}

// TestFullFlow_TextToolTextFinalize verifies the complete end-to-end flow:
// text → tool → text → finalize, with proper merging and final cleanup.
func TestFullFlow_TextToolTextFinalize(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: captureWSFramesCapture(&frames, &mu), streamState: make(map[string]*wsStreamState)}
	stopAck := ackReqLoopCapture(p)
	defer stopAck()

	rc := wsReplyContext{reqID: "req_full", userID: "user_1", streamID: "stream_full"}
	handle := &wsPreviewHandle{replyCtx: rc}

	// 1. Initial text
	p.UpdateMessage(context.Background(), handle, "让我检查一下。")
	// 2. Tool starts
	p.OnToolStart(context.Background(), handle, "Bash", "检查文件", "ls -la")
	// 3. More text
	p.UpdateMessage(context.Background(), handle, "让我检查一下。文件已找到。")
	// 4. Finalize
	p.FinalizePreviewMessage(context.Background(), handle, "检查完成，文件已找到。")

	mu.Lock()
	defer mu.Unlock()
	if len(frames) == 0 {
		t.Fatal("no frames sent")
	}

	// Final frame must be ONLY the answer
	lastFrame := frames[len(frames)-1]
	stream := lastFrame["body"].(map[string]any)["stream"].(map[string]any)
	content := stream["content"].(string)
	finish := stream["finish"].(bool)

	if !finish {
		t.Fatalf("final frame finish = false, want true")
	}
	if content != "检查完成，文件已找到。" {
		t.Fatalf("final frame content = %q, want only the answer (progress cleared)", content)
	}

	// Intermediate frames should have contained tool progress at some point
	allContent := ""
	for _, f := range frames {
		s := f["body"].(map[string]any)["stream"].(map[string]any)
		allContent += s["content"].(string) + "\n"
	}
	if !contains(allContent, "🛠️") {
		t.Fatalf("no frame contained tool progress, all content: %q", allContent)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
