package core

import (
	"strings"
	"testing"
)

// TestMergeStreamDisplayContent_dropsEarlierTurnFromFinal is the A→B regression
// (2026-10-27): the entry agent said "已派发…等待结果" in the dispatch turn and
// "已派发…正在等待" when it closed. Both paragraphs reached the user in ONE
// message, so the same sentence appeared twice.
//
// The second paragraph is the turn's answer; the first is progress that the
// preview (or the per-event messages) already showed. The final message must
// carry the answer alone.
func TestMergeStreamDisplayContent_dropsEarlierTurnFromFinal(t *testing.T) {
	const first = "已派发 worker 查询版本号，等待结果返回后告知你。"
	const answer = "已派发 worker 查询版本号，正在等待结果返回。"
	stream := first + "\n\n" + answer

	got := mergeStreamDisplayContent(stream, answer, answer)

	if got != answer {
		t.Fatalf("got %q, want only the turn's answer %q", got, answer)
	}
	if strings.Contains(got, "等待结果返回后告知你") {
		t.Fatalf("earlier turn's progress leaked into the final message: %q", got)
	}
}

// TestMergeStreamDisplayContent_keepsDistinctProgress is the counterweight: when
// the final answer is NOT contained in the streamed text, the streamed text is
// the only place the user saw it, so it must be kept.
func TestMergeStreamDisplayContent_keepsDistinctProgress(t *testing.T) {
	stream := "先读一下配置。"
	final := "配置里没有该仓库。"

	got := mergeStreamDisplayContent(stream, "", final)

	if !strings.Contains(got, "先读一下配置") || !strings.Contains(got, "配置里没有该仓库") {
		t.Fatalf("unrelated progress must be preserved, got %q", got)
	}
}
