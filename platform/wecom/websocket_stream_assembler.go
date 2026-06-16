package wecom

import "strings"

type wsStreamAssembler struct {
	visibleText string
	heldTool    string
}

const wecomToolBlockPrefix = "🔧 **"

func (a *wsStreamAssembler) reset() {
	a.visibleText = ""
	a.heldTool = ""
}

func (a *wsStreamAssembler) holdTool(content string) {
	a.heldTool = strings.TrimSpace(content)
}

func (a *wsStreamAssembler) ingest(content string, finish bool) (string, bool) {
	trimmed := strings.TrimSpace(content)
	if !finish && a.shouldHoldOnlyTool(trimmed) {
		a.holdTool(trimmed)
		return a.visibleText, false
	}
	if trimmed == "" {
		if finish && a.heldTool != "" {
			a.visibleText = appendWecomStreamSegment(a.visibleText, a.heldTool)
			a.heldTool = ""
			return a.visibleText, a.visibleText != ""
		}
		return a.visibleText, a.visibleText != ""
	}
	if strings.HasPrefix(trimmed, wecomToolBlockPrefix) && !a.shouldHoldOnlyTool(trimmed) {
		a.heldTool = ""
		a.visibleText = trimmed
		return a.visibleText, true
	}
	if a.heldTool != "" {
		a.visibleText = appendWecomStreamSegment(a.visibleText, a.heldTool)
		a.heldTool = ""
		a.visibleText = appendWecomStreamSegment(a.visibleText, trimmed)
		return a.visibleText, true
	}
	// Normal text/tool+answer updates from the engine are already full-replacement
	// payloads for the visible stream, so without a held tool we replace instead of append.
	a.visibleText = trimmed
	return a.visibleText, true
}

func appendWecomStreamSegment(base, addition string) string {
	base = strings.TrimSpace(base)
	addition = strings.TrimSpace(addition)
	if addition == "" {
		return base
	}
	if base == "" {
		return addition
	}
	return base + "\n\n" + addition
}

func (a *wsStreamAssembler) shouldHoldOnlyTool(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || !strings.HasPrefix(trimmed, wecomToolBlockPrefix) {
		return false
	}
	if idx := strings.LastIndex(trimmed, "```"); idx >= 0 {
		if suffix := strings.TrimSpace(trimmed[idx+3:]); suffix != "" {
			return false
		}
	}
	return true
}
