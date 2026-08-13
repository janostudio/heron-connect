package wecom

import (
	"strings"
	"sync"
)

// wecomStreamAssembler is the single source of truth for the WeCom streaming
// preview. Three physically isolated regions:
//   - visibleText:   only model-produced answer text
//   - progressLines: tool progress rows (UI side-channel, cleared on finish)
//   - heldTool:      latest pending tool block (overwritten, not accumulated)
//
// Tool progress lines NEVER enter visibleText. finish() clears progressLines.
// render() is a read-only projection.
type wecomStreamAssembler struct {
	mu sync.Mutex

	visibleText   string
	progressLines []string
	heldTool      string
	finished      bool

	maxProgressLines int
	maxLineChars     int
	detailMode       string
	separator        string
}

const (
	defaultMaxProgressLines = 4
	defaultMaxLineChars     = 120
	defaultSeparator        = "\n\n---\n\n"

	toolPrefix   = "🛠️ "
	completeMark = "✅ "
)

func newWecomStreamAssembler() *wecomStreamAssembler {
	return &wecomStreamAssembler{
		maxProgressLines: defaultMaxProgressLines,
		maxLineChars:     defaultMaxLineChars,
		detailMode:       "explain",
		separator:        defaultSeparator,
	}
}

// appendText sets visibleText to the given text (full replacement, not append).
// The engine passes the accumulated full text on every UpdateMessage call, so we
// must replace rather than append to avoid duplicating content.
// It MUST NOT touch progressLines.
func (a *wecomStreamAssembler) appendText(text string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.visibleText = text
	return a.render()
}

// onToolStart records a tool-start event into progressLines + heldTool.
// It MUST NOT touch visibleText.
func (a *wecomStreamAssembler) onToolStart(toolName, explainArg, rawArg string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	line := a.formatToolLine(toolName, explainArg, rawArg)
	a.heldTool = line
	a.progressLines = appendBounded(a.progressLines, line, a.maxProgressLines)
	return a.render()
}

// onToolComplete adds a completion row. Does NOT enter visibleText.
func (a *wecomStreamAssembler) onToolComplete(toolName, resultSummary string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if resultSummary == "" {
		return a.render()
	}
	line := completeMark + toolName + ": " + truncateMiddle(resultSummary, a.maxLineChars)
	a.progressLines = appendBounded(a.progressLines, line, a.maxProgressLines)
	a.heldTool = ""
	return a.render()
}

// finish sets the final visible text and clears all progress state.
func (a *wecomStreamAssembler) finish(finalText string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if finalText != "" {
		a.visibleText = finalText
	}
	a.progressLines = nil
	a.heldTool = ""
	a.finished = true
	return a.render()
}

// discard clears all state.
func (a *wecomStreamAssembler) discard() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.visibleText = ""
	a.progressLines = nil
	a.heldTool = ""
	a.finished = false
}

// reset is an alias for discard (keeps config).
func (a *wecomStreamAssembler) reset() {
	a.discard()
}

// snapshot returns the current rendered state without mutating anything.
func (a *wecomStreamAssembler) snapshot() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.render()
}

// render is a read-only projection. MUST NOT mutate state.
// Caller must hold a.mu.
func (a *wecomStreamAssembler) render() string {
	var parts []string
	if len(a.progressLines) > 0 {
		parts = append(parts, strings.Join(a.progressLines, "\n"))
	}
	if a.visibleText != "" {
		parts = append(parts, a.visibleText)
	}
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, a.separator)
}

// formatToolLine builds a compact single-line tool progress entry.
// explain: 🛠️ Bash: run tests
// raw:     🛠️ Bash: run tests, npm test --verbose
func (a *wecomStreamAssembler) formatToolLine(toolName, explainArg, rawArg string) string {
	arg := explainArg
	if a.detailMode == "raw" && rawArg != "" {
		arg = explainArg + ", " + rawArg
	}
	if arg == "" {
		arg = toolName
	}
	return truncateMiddle(toolPrefix+toolName+": "+arg, a.maxLineChars)
}

// appendBounded appends a line, dropping oldest entries when max is exceeded (FIFO).
func appendBounded(lines []string, newLine string, max int) []string {
	lines = append(lines, newLine)
	if max > 0 && len(lines) > max {
		lines = lines[len(lines)-max:]
	}
	return lines
}

// truncateMiddle shortens a string to maxChars runes, preserving head and tail
// with an ellipsis in the middle. Strings at or below maxChars are returned unchanged.
func truncateMiddle(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	if maxChars < 5 {
		return string(r[:maxChars])
	}
	headLen := (maxChars - 1) / 2
	tailLen := maxChars - 1 - headLen
	return string(r[:headLen]) + "…" + string(r[len(r)-tailLen:])
}
