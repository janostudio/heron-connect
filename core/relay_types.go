package core

import (
	"context"
	"fmt"
	"strings"
)

// RelayBinding represents a bot-to-bot relay binding in a group chat.
type RelayBinding struct {
	Platform string            `json:"platform"`
	ChatID   string            `json:"chat_id"`
	Bots     map[string]string `json:"bots"` // project name → bot display name
}

// RelayRequest is the payload for a relay send.
type RelayRequest struct {
	From       string `json:"from"`        // source project name
	To         string `json:"to"`          // target project name
	SessionKey string `json:"session_key"` // source session key (contains platform + chatID)
	Message    string `json:"message"`
}

// RelayResponse is the result of a relay send.
type RelayResponse struct {
	Response string `json:"response"`
}

// RelayManagerAPI is the interface that a relay manager must satisfy to be
// used by the Engine, APIServer, and related subsystems. relay.RelayManager
// satisfies this interface.
type RelayManagerAPI interface {
	RegisterEngine(name string, e *Engine)
	Bind(platform, chatID string, bots map[string]string)
	AddToBind(platform, chatID, projectName string)
	RemoveFromBind(chatID, projectName string) bool
	GetBinding(chatID string) *RelayBinding
	Unbind(chatID string)
	HasEngine(name string) bool
	ListEngineNames() []string
	ListBoundBots(chatID, selfProject string) map[string]string
	Send(ctx context.Context, req RelayRequest) (*RelayResponse, error)
}

// parseSessionKeyParts extracts the platform and chatID from a session key.
// Format: "platform:chatID:userID"
// Relay session format: "relay:sourceProject:chatID"
func parseSessionKeyParts(sessionKey string) (platform, chatID string, err error) {
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid session key format: %q", sessionKey)
	}
	if parts[0] == "relay" && len(parts) == 3 {
		// For relay sessions, chatID is the third part: "relay:sourceProject:chatID"
		return parts[0], parts[2], nil
	}
	return parts[0], parts[1], nil
}
