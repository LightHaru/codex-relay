package mux

import "encoding/json"

const scopedAccountParameter = "codexMuxAccountId"

var accountScopedPluginMethods = map[string]struct{}{
	"app/installed":         {},
	"app/list":              {},
	"app/read":              {},
	"mcpServer/oauth/login": {},
	"mcpServerStatus/list":  {},
}

// scopedPluginRequest extracts the renderer-only routing marker before the
// request reaches a real Codex app-server, whose strict RPC schema must never
// see our extension field.
func scopedPluginRequest(method string, params json.RawMessage) (string, json.RawMessage, bool) {
	if _, ok := accountScopedPluginMethods[method]; !ok || len(params) == 0 {
		return "", params, false
	}
	var input map[string]json.RawMessage
	if json.Unmarshal(params, &input) != nil {
		return "", params, false
	}
	rawAccountID, ok := input[scopedAccountParameter]
	if !ok {
		return "", params, false
	}
	var accountID string
	if json.Unmarshal(rawAccountID, &accountID) != nil || accountID == "" {
		return "", params, false
	}
	delete(input, scopedAccountParameter)
	cleaned, err := json.Marshal(input)
	if err != nil {
		return "", params, false
	}
	return accountID, cleaned, true
}
