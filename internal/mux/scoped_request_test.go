package mux

import (
	"encoding/json"
	"testing"
)

func TestScopedPluginRequestExtractsRoutingMarker(t *testing.T) {
	accountID, cleaned, ok := scopedPluginRequest(
		"app/installed",
		json.RawMessage(`{"forceRefresh":true,"codexMuxAccountId":"subscription-2"}`),
	)
	if !ok || accountID != "subscription-2" {
		t.Fatalf("routing marker was not extracted: id=%q ok=%v", accountID, ok)
	}
	if string(cleaned) != `{"forceRefresh":true}` {
		t.Fatalf("unexpected cleaned params: %s", cleaned)
	}
}

func TestScopedPluginRequestSupportsOriginallyNullaryList(t *testing.T) {
	accountID, cleaned, ok := scopedPluginRequest(
		"app/installed",
		json.RawMessage(`{"codexMuxAccountId":"subscription-2"}`),
	)
	if !ok || accountID != "subscription-2" {
		t.Fatalf("routing marker was not extracted: id=%q ok=%v", accountID, ok)
	}
	if string(cleaned) != `{}` {
		t.Fatalf("unexpected cleaned params: %s", cleaned)
	}
}

func TestScopedPluginRequestIgnoresUnrelatedMethods(t *testing.T) {
	params := json.RawMessage(`{"codexMuxAccountId":"subscription-2"}`)
	if _, got, ok := scopedPluginRequest("thread/list", params); ok || string(got) != string(params) {
		t.Fatal("unrelated request was scoped")
	}
}

func TestScopedPluginRequestRecognizesMCPStatusAndLogin(t *testing.T) {
	for _, method := range []string{"mcpServerStatus/list", "mcpServer/oauth/login"} {
		t.Run(method, func(t *testing.T) {
			accountID, cleaned, ok := scopedPluginRequest(
				method,
				json.RawMessage(`{"name":"github","codexMuxAccountId":"subscription-2"}`),
			)
			if !ok || accountID != "subscription-2" {
				t.Fatalf("expected scoped %s request for subscription-2", method)
			}
			if string(cleaned) != `{"name":"github"}` {
				t.Fatalf("unexpected cleaned params for %s: %s", method, cleaned)
			}
		})
	}
}
