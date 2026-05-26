package wecom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

// ---------------------------------------------------------------------------
// splitByBytes
// ---------------------------------------------------------------------------

func TestSplitByBytes_ShortString(t *testing.T) {
	parts := splitByBytes("hello", 100)
	if len(parts) != 1 || parts[0] != "hello" {
		t.Fatalf("expected single chunk, got %v", parts)
	}
}

func TestSplitByBytes_ExactBoundary(t *testing.T) {
	s := "abcdef"
	parts := splitByBytes(s, 6)
	if len(parts) != 1 || parts[0] != s {
		t.Fatalf("expected single chunk at exact boundary, got %v", parts)
	}
}

func TestSplitByBytes_SplitASCII(t *testing.T) {
	s := "abcdef"
	parts := splitByBytes(s, 4)
	if len(parts) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(parts), parts)
	}
	if parts[0] != "abcd" || parts[1] != "ef" {
		t.Fatalf("unexpected chunks: %v", parts)
	}
}

func TestSplitByBytes_UTF8NeverSplitsMidRune(t *testing.T) {
	// "你好世界" = 4 runes × 3 bytes = 12 bytes
	s := "你好世界"
	parts := splitByBytes(s, 5) // 5 < 6, so only one 3-byte rune fits? Actually 3 fits, 4 doesn't → first chunk = "你" (3 bytes)
	// With maxBytes=5: first iteration end=5, s[5] is a continuation byte → back off to 3 → "你", next end=5 but only 9 left, s[5] continuation → 6 → "好世" wait...
	// Let's just verify no chunk contains a partial rune.
	reassembled := ""
	for _, p := range parts {
		reassembled += p
		// Each chunk must be valid UTF-8 (no partial rune)
		for i := 0; i < len(p); i++ {
			if p[i]>>6 == 0b10 && (i == 0 || p[i-1] < 0x80) {
				t.Fatalf("chunk contains orphaned continuation byte: %q", p)
			}
		}
	}
	if reassembled != s {
		t.Fatalf("reassembled %q != original %q", reassembled, s)
	}
}

func TestSplitByBytes_EmptyString(t *testing.T) {
	parts := splitByBytes("", 100)
	if len(parts) != 1 || parts[0] != "" {
		t.Fatalf("expected single empty chunk, got %v", parts)
	}
}

func TestSplitByBytes_ReassemblesLargeContent(t *testing.T) {
	var s string
	for i := 0; i < 500; i++ {
		s += fmt.Sprintf("line %d: 这是一段中文\n", i)
	}
	parts := splitByBytes(s, 2000)
	reassembled := ""
	for _, p := range parts {
		if len(p) > 2000 {
			t.Fatalf("chunk exceeds maxBytes: %d", len(p))
		}
		reassembled += p
	}
	if reassembled != s {
		t.Fatalf("reassembled content does not match original (len %d vs %d)", len(reassembled), len(s))
	}
}

// ---------------------------------------------------------------------------
// handleMsgCallback — chatID fallback to userID for single chats
// ---------------------------------------------------------------------------

func TestHandleMsgCallback_SingleChat_ChatIDFallback(t *testing.T) {
	p := &WSPlatform{
		allowFrom: "*",
	}

	captured := make(chan *core.Message, 1)
	p.handler = func(_ core.Platform, msg *core.Message) {
		captured <- msg
	}

	body := wsMsgCallbackBody{
		MsgID:    "msg_001",
		ChatID:   "", // single chat: no chatID from server
		ChatType: "single",
		MsgType:  "text",
	}
	body.From.UserID = "zhangsan"
	body.Text.Content = "hello"
	body.CreateTime = time.Now().Unix()

	bodyBytes, _ := json.Marshal(body)
	frame := wsFrame{
		Cmd:     "aibot_msg_callback",
		Headers: wsFrameHeaders{ReqID: "req_123"},
		Body:    bodyBytes,
	}

	p.handleMsgCallback(frame)

	select {
	case msg := <-captured:
		if msg.SessionKey != "wecom:zhangsan:zhangsan" {
			t.Fatalf("expected sessionKey 'wecom:zhangsan:zhangsan', got %q", msg.SessionKey)
		}
		rc := msg.ReplyCtx.(wsReplyContext)
		if rc.chatID != "zhangsan" {
			t.Fatalf("expected chatID to fall back to userID 'zhangsan', got %q", rc.chatID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("handler not called")
	}
}

func TestHandleMsgCallback_GroupChat_ChatIDPreserved(t *testing.T) {
	p := &WSPlatform{
		allowFrom: "*",
	}

	captured := make(chan *core.Message, 1)
	p.handler = func(_ core.Platform, msg *core.Message) {
		captured <- msg
	}

	body := wsMsgCallbackBody{
		MsgID:    "msg_002",
		ChatID:   "group_chat_id_123",
		ChatType: "group",
		MsgType:  "text",
	}
	body.From.UserID = "zhangsan"
	body.Text.Content = "hi group"
	body.CreateTime = time.Now().Unix()

	bodyBytes, _ := json.Marshal(body)
	frame := wsFrame{
		Cmd:     "aibot_msg_callback",
		Headers: wsFrameHeaders{ReqID: "req_456"},
		Body:    bodyBytes,
	}

	p.handleMsgCallback(frame)

	select {
	case msg := <-captured:
		if msg.SessionKey != "wecom:group_chat_id_123:zhangsan" {
			t.Fatalf("expected sessionKey 'wecom:group_chat_id_123:zhangsan', got %q", msg.SessionKey)
		}
		rc := msg.ReplyCtx.(wsReplyContext)
		if rc.chatID != "group_chat_id_123" {
			t.Fatalf("expected chatID 'group_chat_id_123', got %q", rc.chatID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("handler not called")
	}
}

func TestHandleMsgCallback_StripsBotMention(t *testing.T) {
	p := &WSPlatform{
		allowFrom: "*",
		botID:     "robot01",
	}

	captured := make(chan *core.Message, 1)
	p.handler = func(_ core.Platform, msg *core.Message) {
		captured <- msg
	}

	body := wsMsgCallbackBody{
		MsgID:    "msg_mention",
		ChatID:   "grp1",
		ChatType: "group",
		MsgType:  "text",
		AibotID:  "robot01",
	}
	body.From.UserID = "u1"
	body.Text.Content = "允许 @Robot01"
	body.CreateTime = time.Now().Unix()

	bodyBytes, _ := json.Marshal(body)
	frame := wsFrame{
		Cmd:     "aibot_msg_callback",
		Headers: wsFrameHeaders{ReqID: "req_m"},
		Body:    bodyBytes,
	}

	p.handleMsgCallback(frame)

	select {
	case msg := <-captured:
		if msg.Content != "允许" {
			t.Fatalf("expected stripped content %q, got %q", "允许", msg.Content)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("handler not called")
	}
}

func TestWSPreviewStartAndUpdate_ReuseSameStreamID(t *testing.T) {
	var frames []map[string]any
	p := &WSPlatform{writeJSONFn: captureWSFrames(&frames)}
	rctx := wsReplyContext{reqID: "req_123", chatID: "chat_1", userID: "user_1"}
	go func() {
		for i := 0; i < 2; i++ {
			for {
				if v, ok := p.pendingAcks.LoadAndDelete("req_123"); ok {
					v.(chan error) <- nil
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()

	handleAny, err := p.SendPreviewStart(context.Background(), rctx, "partial")
	if err != nil {
		t.Fatalf("SendPreviewStart failed: %v", err)
	}
	handle, ok := handleAny.(*wsPreviewHandle)
	if !ok {
		t.Fatalf("preview handle type = %T", handleAny)
	}
	if handle.replyCtx.streamID == "" {
		t.Fatal("expected preview handle to capture streamID")
	}

	if err := p.UpdateMessage(context.Background(), handle, "partial 2"); err != nil {
		t.Fatalf("UpdateMessage failed: %v", err)
	}

	if len(frames) != 2 {
		t.Fatalf("captured frames = %d, want 2", len(frames))
	}

	firstStream := frames[0]["body"].(map[string]any)["stream"].(map[string]any)
	secondStream := frames[1]["body"].(map[string]any)["stream"].(map[string]any)
	if firstStream["id"] != secondStream["id"] {
		t.Fatalf("stream ids differ: %v vs %v", firstStream["id"], secondStream["id"])
	}
	if firstStream["finish"] != false || secondStream["finish"] != false {
		t.Fatalf("expected non-final preview frames, got %+v and %+v", firstStream, secondStream)
	}
	if firstStream["content"] != "partial" || secondStream["content"] != "partial 2" {
		t.Fatalf("unexpected content: %+v %+v", firstStream, secondStream)
	}
	if frames[0]["headers"].(map[string]any)["req_id"] != "req_123" || frames[1]["headers"].(map[string]any)["req_id"] != "req_123" {
		t.Fatalf("expected req_id req_123, got %+v %+v", frames[0]["headers"], frames[1]["headers"])
	}
}

func TestReply_SendsFinalStreamFrame(t *testing.T) {
	var frames []map[string]any
	p := &WSPlatform{writeJSONFn: captureWSFrames(&frames)}
	rctx := wsReplyContext{reqID: "req_final", userID: "user_1"}
	go func() {
		time.Sleep(10 * time.Millisecond)
		if v, ok := p.pendingAcks.LoadAndDelete("req_final"); ok {
			v.(chan error) <- nil
		}
	}()
	if err := p.Reply(context.Background(), rctx, "final answer"); err != nil {
		t.Fatalf("Reply failed: %v", err)
	}

	if len(frames) != 1 {
		t.Fatalf("captured frames = %d, want 1", len(frames))
	}
	frame := frames[0]
	stream := frame["body"].(map[string]any)["stream"].(map[string]any)
	if stream["finish"] != true {
		t.Fatalf("expected finish=true, got %+v", stream)
	}
	if stream["content"] != "final answer" {
		t.Fatalf("unexpected content: %+v", stream)
	}
}

func TestSendStreamFrameAndWaitAck_SerializesConcurrentUpdates(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var frame map[string]any
		if err := json.Unmarshal(b, &frame); err != nil {
			return err
		}
		mu.Lock()
		frames = append(frames, frame)
		mu.Unlock()
		return nil
	}}
	rc := wsReplyContext{reqID: "req_serial", userID: "user_1", streamID: "stream_fixed"}

	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- p.sendStreamFrameAndWaitAck(context.Background(), rc, "first", false)
	}()

	go func() {
		for {
			if _, ok := p.pendingAcks.Load("req_serial"); ok {
				close(firstStarted)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	<-firstStarted

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- p.sendStreamFrameAndWaitAck(context.Background(), rc, "second", false)
	}()

	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	framesBeforeAck := len(frames)
	mu.Unlock()
	if framesBeforeAck != 1 {
		t.Fatalf("frames before first ack = %d, want 1", framesBeforeAck)
	}

	if v, ok := p.pendingAcks.LoadAndDelete("req_serial"); ok {
		v.(chan error) <- nil
	} else {
		t.Fatal("missing first pending ack")
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first update failed: %v", err)
	}

	for {
		mu.Lock()
		framesAfterFirstAck := len(frames)
		mu.Unlock()
		if framesAfterFirstAck == 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if v, ok := p.pendingAcks.LoadAndDelete("req_serial"); ok {
		v.(chan error) <- nil
	} else {
		t.Fatal("missing second pending ack")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second update failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}
	if got := frames[0]["body"].(map[string]any)["stream"].(map[string]any)["content"]; got != "first" {
		t.Fatalf("first content = %v, want first", got)
	}
	if got := frames[1]["body"].(map[string]any)["stream"].(map[string]any)["content"]; got != "second" {
		t.Fatalf("second content = %v, want second", got)
	}
}

func TestSendStreamFrameAndWaitAck_LatestWinsPendingPreview(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var frame map[string]any
		if err := json.Unmarshal(b, &frame); err != nil {
			return err
		}
		mu.Lock()
		frames = append(frames, frame)
		mu.Unlock()
		return nil
	}}
	rc := wsReplyContext{reqID: "req_latest", userID: "user_1", streamID: "stream_fixed"}

	firstStarted := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- p.sendStreamFrameAndWaitAck(context.Background(), rc, "first", false)
	}()

	go func() {
		for {
			if _, ok := p.pendingAcks.Load("req_latest"); ok {
				close(firstStarted)
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	<-firstStarted

	secondDone := make(chan error, 1)
	thirdDone := make(chan error, 1)
	go func() {
		secondDone <- p.sendStreamFrameAndWaitAck(context.Background(), rc, "second", false)
	}()
	for {
		key, state, err := p.streamStateFor(rc)
		if err != nil {
			t.Fatalf("streamStateFor failed: %v", err)
		}
		_ = key
		state.mu.Lock()
		pendingReady := state.pending != nil && state.pending.content == "second"
		state.mu.Unlock()
		if pendingReady {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	go func() {
		thirdDone <- p.sendStreamFrameAndWaitAck(context.Background(), rc, "third", false)
	}()

	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	framesBeforeAck := len(frames)
	mu.Unlock()
	if framesBeforeAck != 1 {
		t.Fatalf("frames before first ack = %d, want 1", framesBeforeAck)
	}

	if v, ok := p.pendingAcks.LoadAndDelete("req_latest"); ok {
		v.(chan error) <- nil
	} else {
		t.Fatal("missing first pending ack")
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first update failed: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second update should be superseded without error, got %v", err)
	}

	for {
		mu.Lock()
		framesAfterFirstAck := len(frames)
		mu.Unlock()
		if framesAfterFirstAck == 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	for {
		if v, ok := p.pendingAcks.LoadAndDelete("req_latest"); ok {
			v.(chan error) <- nil
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := <-thirdDone; err != nil {
		t.Fatalf("third update failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}
	if got := frames[0]["body"].(map[string]any)["stream"].(map[string]any)["content"]; got != "first" {
		t.Fatalf("first content = %v, want first", got)
	}
	if got := frames[1]["body"].(map[string]any)["stream"].(map[string]any)["content"]; got != "third" {
		t.Fatalf("latest content = %v, want third", got)
	}
}

func TestUpdateMessage_InvalidHandle(t *testing.T) {
	p := &WSPlatform{}
	if err := p.UpdateMessage(context.Background(), "bad-handle", "x"); err == nil {
		t.Fatal("expected invalid handle error")
	}
	if err := p.UpdateMessage(context.Background(), &wsPreviewHandle{}, "x"); err == nil {
		t.Fatal("expected missing stream id error")
	}
}

func TestWSContentAggregator_KeepsOnlyLatestPendingTool(t *testing.T) {
	var agg wsContentAggregator

	first := "🔧 **工具 #1: Bash**\n---\n`wc -m C`"
	second := "🔧 **工具 #1: Bash**\n---\n`wc -m CHANGELOG.md`"
	text := "项目根目录没有 `CHANGELOG.md`。"

	if got := agg.ingest(first); got != "" {
		t.Fatalf("first tool render = %q, want empty", got)
	}
	if got := agg.ingest(second); got != "" {
		t.Fatalf("second tool render = %q, want empty", got)
	}
	if got := agg.ingest(text); got != strings.TrimSpace(second)+"\n\n"+text {
		t.Fatalf("text render = %q", got)
	}
}

func TestWSContentAggregator_FinalizeFlushesPendingTool(t *testing.T) {
	var agg wsContentAggregator
	tool := "🔧 **工具 #1: Bash**\n---\n`wc -m CHANGELOG.md`"

	_ = agg.ingest(tool)
	if got := agg.finalize(""); got != strings.TrimSpace(tool) {
		t.Fatalf("finalize render = %q, want %q", got, strings.TrimSpace(tool))
	}
	if got := agg.render(); got != "" {
		t.Fatalf("aggregator should reset after finalize, got %q", got)
	}
}

func TestSendStreamFrameAndWaitAck_AggregatesPendingToolPreview(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var frame map[string]any
		if err := json.Unmarshal(b, &frame); err != nil {
			return err
		}
		mu.Lock()
		frames = append(frames, frame)
		mu.Unlock()
		return nil
	}}
	rc := wsReplyContext{reqID: "req_agg", userID: "user_1", streamID: "stream_fixed"}

	tool1 := "🔧 **工具 #1: Bash**\n---\n`wc -m C`"
	tool2 := "🔧 **工具 #1: Bash**\n---\n`wc -m CHANGELOG.md`"
	text := "项目根目录没有 `CHANGELOG.md`。"

	done1 := make(chan error, 1)
	go func() { done1 <- p.sendStreamFrameAndWaitAck(context.Background(), rc, tool1, false) }()
	time.Sleep(20 * time.Millisecond)
	done2 := make(chan error, 1)
	go func() { done2 <- p.sendStreamFrameAndWaitAck(context.Background(), rc, tool2, false) }()
	time.Sleep(20 * time.Millisecond)

	if err := <-done1; err != nil {
		t.Fatalf("first send failed: %v", err)
	}
	if err := <-done2; err != nil {
		t.Fatalf("second send failed: %v", err)
	}
	_, state, err := p.streamStateFor(rc)
	if err != nil {
		t.Fatalf("streamStateFor failed: %v", err)
	}
	state.mu.Lock()
	heldTool := state.heldTool
	state.mu.Unlock()
	if heldTool != tool2 {
		t.Fatalf("held tool after second send = %q, want %q", heldTool, tool2)
	}

	mu.Lock()
	if len(frames) != 0 {
		mu.Unlock()
		t.Fatalf("frames after tool-only updates = %d, want 0", len(frames))
	}
	mu.Unlock()

	done3 := make(chan error, 1)
	go func() { done3 <- p.sendStreamFrameAndWaitAck(context.Background(), rc, text, false) }()
	for {
		if _, ok := p.pendingAcks.Load("req_agg"); ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if v, ok := p.pendingAcks.LoadAndDelete("req_agg"); ok {
		v.(chan error) <- nil
	} else {
		t.Fatal("missing text pending ack")
	}
	if err := <-done3; err != nil {
		t.Fatalf("text send failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	firstContent := frames[0]["body"].(map[string]any)["stream"].(map[string]any)["content"]
	want := tool2 + "\n\n" + text
	if firstContent != want {
		t.Fatalf("aggregated content = %v, want %q", firstContent, want)
	}
}

func TestSendStreamFrameAndWaitAck_TextStillStreamsWhileToolIsHeld(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var frame map[string]any
		if err := json.Unmarshal(b, &frame); err != nil {
			return err
		}
		mu.Lock()
		frames = append(frames, frame)
		mu.Unlock()
		return nil
	}}
	rc := wsReplyContext{reqID: "req_stream_text", userID: "user_1", streamID: "stream_fixed"}

	text1 := "先检查一下。"
	tool := "🔧 **工具 #1: Bash**\n---\n`wc -m CHANGELOG.md`"
	text2 := "项目根目录没有 `CHANGELOG.md`。"

	steps := []string{text1, tool, text2}
	for _, step := range steps {
		done := make(chan error, 1)
		go func(content string) { done <- p.sendStreamFrameAndWaitAck(context.Background(), rc, content, false) }(step)
		if err := <-done; err != nil {
			t.Fatalf("send failed for %q: %v", step, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}
	firstContent := frames[0]["body"].(map[string]any)["stream"].(map[string]any)["content"]
	if firstContent != text1 {
		t.Fatalf("first content = %v, want %q", firstContent, text1)
	}
	secondContent := frames[1]["body"].(map[string]any)["stream"].(map[string]any)["content"]
	wantSecond := text1 + "\n\n" + tool + "\n\n" + text2
	if secondContent != wantSecond {
		t.Fatalf("second content = %v, want %q", secondContent, wantSecond)
	}
}

func TestSendStreamFrameAndWaitAck_FinishFlushesHeldTool(t *testing.T) {
	var (
		mu     sync.Mutex
		frames []map[string]any
	)
	p := &WSPlatform{writeJSONFn: func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var frame map[string]any
		if err := json.Unmarshal(b, &frame); err != nil {
			return err
		}
		mu.Lock()
		frames = append(frames, frame)
		mu.Unlock()
		return nil
	}}
	rc := wsReplyContext{reqID: "req_finish_hold", userID: "user_1", streamID: "stream_fixed"}

	tool := "🔧 **工具 #1: Bash**\n---\n`wc -m /tmp/agent.json`"
	text := "问题已经确认。"

	doneTool := make(chan error, 1)
	go func() { doneTool <- p.sendStreamFrameAndWaitAck(context.Background(), rc, tool, false) }()
	if err := <-doneTool; err != nil {
		t.Fatalf("tool send failed: %v", err)
	}

	mu.Lock()
	if len(frames) != 0 {
		mu.Unlock()
		t.Fatalf("frames after held tool = %d, want 0", len(frames))
	}
	mu.Unlock()

	doneFinish := make(chan error, 1)
	go func() { doneFinish <- p.sendStreamFrameAndWaitAck(context.Background(), rc, text, true) }()
	for {
		if _, ok := p.pendingAcks.Load("req_finish_hold"); ok {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if v, ok := p.pendingAcks.LoadAndDelete("req_finish_hold"); ok {
		v.(chan error) <- nil
	} else {
		t.Fatal("missing finish pending ack")
	}
	if err := <-doneFinish; err != nil {
		t.Fatalf("finish send failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(frames))
	}
	stream := frames[0]["body"].(map[string]any)["stream"].(map[string]any)
	if stream["finish"] != true {
		t.Fatalf("finish flag = %v, want true", stream["finish"])
	}
	want := tool + "\n\n" + text
	if stream["content"] != want {
		t.Fatalf("finish content = %v, want %q", stream["content"], want)
	}
}

func captureWSFrames(dst *[]map[string]any) func(any) error {
	return func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		var frame map[string]any
		if err := json.Unmarshal(b, &frame); err != nil {
			return err
		}
		*dst = append(*dst, frame)
		return nil
	}
}

// ---------------------------------------------------------------------------
// ReconstructReplyCtx
// ---------------------------------------------------------------------------

func TestReconstructReplyCtx_Valid(t *testing.T) {
	p := &WSPlatform{}
	rctx, err := p.ReconstructReplyCtx("wecom:chatid123:user456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rc := rctx.(wsReplyContext)
	if rc.chatID != "chatid123" || rc.userID != "user456" {
		t.Fatalf("unexpected context: %+v", rc)
	}
}

func TestReconstructReplyCtx_InvalidPrefix(t *testing.T) {
	p := &WSPlatform{}
	_, err := p.ReconstructReplyCtx("slack:chatid123:user456")
	if err == nil {
		t.Fatal("expected error for invalid prefix")
	}
}

func TestReconstructReplyCtx_TooFewParts(t *testing.T) {
	p := &WSPlatform{}
	_, err := p.ReconstructReplyCtx("wecom:only")
	if err == nil {
		t.Fatal("expected error for too few parts")
	}
}

// ---------------------------------------------------------------------------
// writeAndWaitAck
// ---------------------------------------------------------------------------

func TestWriteAndWaitAck_SuccessfulAck(t *testing.T) {
	p := &WSPlatform{}

	reqID := "send_1"
	ch := make(chan error, 1)
	p.pendingAcks.Store(reqID, ch)

	// Simulate receiving ack in another goroutine
	go func() {
		time.Sleep(10 * time.Millisecond)
		if v, ok := p.pendingAcks.LoadAndDelete(reqID); ok {
			v.(chan error) <- nil
		}
	}()

	ctx := context.Background()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("expected nil ack error, got %v", err)
		}
	case <-ctx.Done():
		t.Fatal("context cancelled unexpectedly")
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ack")
	}
}

func TestWriteAndWaitAck_AckWithError(t *testing.T) {
	p := &WSPlatform{}

	reqID := "send_2"
	ch := make(chan error, 1)
	p.pendingAcks.Store(reqID, ch)

	ackErr := fmt.Errorf("wecom-ws: ack error: errcode=40001 errmsg=invalid token")
	go func() {
		time.Sleep(10 * time.Millisecond)
		if v, ok := p.pendingAcks.LoadAndDelete(reqID); ok {
			v.(chan error) <- ackErr
		}
	}()

	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected ack error, got nil")
		}
		if err.Error() != ackErr.Error() {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for ack")
	}
}

func TestWriteAndWaitAck_Timeout(t *testing.T) {
	p := &WSPlatform{}

	reqID := "send_timeout"
	ch := make(chan error, 1)
	p.pendingAcks.Store(reqID, ch)

	// Nobody sends ack → should timeout
	start := time.Now()
	select {
	case <-ch:
		t.Fatal("should not receive from channel without ack")
	case <-time.After(100 * time.Millisecond):
		// Expected: timed out without blocking forever
	}
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}

	// Clean up
	p.pendingAcks.Delete(reqID)
}

func TestWriteAndWaitAck_ContextCancelled(t *testing.T) {
	p := &WSPlatform{}

	reqID := "send_cancel"
	ch := make(chan error, 1)
	p.pendingAcks.Store(reqID, ch)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	select {
	case <-ch:
		t.Fatal("should not receive ack")
	case <-ctx.Done():
		// Expected: context cancelled
	case <-time.After(1 * time.Second):
		t.Fatal("timed out")
	}

	p.pendingAcks.Delete(reqID)
}

// ---------------------------------------------------------------------------
// handleFrame — ACK dispatch
// ---------------------------------------------------------------------------

func TestHandleFrame_AckDispatch(t *testing.T) {
	p := &WSPlatform{}

	reqID := "aibot_send_msg_1"
	ch := make(chan error, 1)
	p.pendingAcks.Store(reqID, ch)

	errCode := 0
	frame := wsFrame{
		Cmd:     "",
		Headers: wsFrameHeaders{ReqID: reqID},
		ErrCode: &errCode,
		ErrMsg:  "ok",
	}

	p.handleFrame(frame)

	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("expected nil error for successful ack, got %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ack not dispatched")
	}
}

func TestHandleFrame_AckDispatch_WithError(t *testing.T) {
	p := &WSPlatform{}

	reqID := "aibot_send_msg_2"
	ch := make(chan error, 1)
	p.pendingAcks.Store(reqID, ch)

	errCode := 40001
	frame := wsFrame{
		Cmd:     "",
		Headers: wsFrameHeaders{ReqID: reqID},
		ErrCode: &errCode,
		ErrMsg:  "invalid token",
	}

	p.handleFrame(frame)

	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("expected error for failed ack, got nil")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ack not dispatched")
	}
}

func TestHandleFrame_PingAck_ResetsMissedPong(t *testing.T) {
	p := &WSPlatform{}
	p.missedPong.Store(2)

	frame := wsFrame{
		Cmd:     "",
		Headers: wsFrameHeaders{ReqID: "ping_1"},
	}

	p.handleFrame(frame)

	if p.missedPong.Load() != 0 {
		t.Fatalf("expected missedPong to be reset to 0, got %d", p.missedPong.Load())
	}
}

// ---------------------------------------------------------------------------
// generateReqID
// ---------------------------------------------------------------------------

func TestGenerateReqID_Monotonic(t *testing.T) {
	p := &WSPlatform{}

	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := p.generateReqID("test")
		if ids[id] {
			t.Fatalf("duplicate req_id: %s", id)
		}
		ids[id] = true
	}
}

func TestGenerateReqID_Format(t *testing.T) {
	p := &WSPlatform{}
	id := p.generateReqID("ping")
	if id != "ping_1" {
		t.Fatalf("expected ping_1, got %s", id)
	}
	id2 := p.generateReqID("aibot_send_msg")
	if id2 != "aibot_send_msg_2" {
		t.Fatalf("expected aibot_send_msg_2, got %s", id2)
	}
}

// ---------------------------------------------------------------------------
// generateReqID — concurrency safety
// ---------------------------------------------------------------------------

func TestGenerateReqID_ConcurrentSafety(t *testing.T) {
	p := &WSPlatform{}

	var wg sync.WaitGroup
	ids := sync.Map{}
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := p.generateReqID("concurrent")
			if _, loaded := ids.LoadOrStore(id, true); loaded {
				t.Errorf("duplicate req_id: %s", id)
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// newWebSocket
// ---------------------------------------------------------------------------

func TestNewWebSocket_MissingCredentials(t *testing.T) {
	tests := []struct {
		name string
		opts map[string]any
	}{
		{"empty opts", map[string]any{}},
		{"missing bot_secret", map[string]any{"bot_id": "aib123"}},
		{"missing bot_id", map[string]any{"bot_secret": "secret"}},
		{"both empty strings", map[string]any{"bot_id": "", "bot_secret": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newWebSocket(tt.opts)
			if err == nil {
				t.Fatal("expected error for missing credentials")
			}
		})
	}
}

func TestNewWebSocket_ValidConfig(t *testing.T) {
	p, err := newWebSocket(map[string]any{
		"bot_id":     "aibTest",
		"bot_secret": "secretXYZ",
		"allow_from": "user1,user2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ws := p.(*WSPlatform)
	if ws.botID != "aibTest" || ws.secret != "secretXYZ" || ws.allowFrom != "user1,user2" {
		t.Fatalf("unexpected config: botID=%s secret=%s allowFrom=%s", ws.botID, ws.secret, ws.allowFrom)
	}
}
