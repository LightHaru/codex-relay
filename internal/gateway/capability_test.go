package gateway

import (
	"net/http"
	"testing"
)

func TestEndpointRegistryClassifiesReviewedAndUnknownRoutes(t *testing.T) {
	registry := NewEndpointRegistry()
	checks := []struct {
		method, path string
		kind         EndpointKind
		retry        RetryPolicy
	}{
		{http.MethodPost, "/v1/responses", EndpointResponses, RetryBeforeCommit},
		{http.MethodPost, "/v1/responses/compact", EndpointCompact, RetryBeforeCommit},
		{http.MethodGet, "/v1/models", EndpointModels, RetryNever},
		{http.MethodPost, "/v1/future/new", EndpointOpaque, RetryNever},
	}
	for _, check := range checks {
		capability := registry.Lookup(check.method, check.path)
		if capability.Kind != check.kind || capability.RetryPolicy != check.retry {
			t.Errorf("Lookup(%q, %q) = kind %q retry %q, want kind %q retry %q", check.method, check.path, capability.Kind, capability.RetryPolicy, check.kind, check.retry)
		}
	}
}

func TestEndpointRegistryZeroValueUsesSafeDefaults(t *testing.T) {
	capability := (EndpointRegistry{}).Lookup(http.MethodPatch, "/v1/not-yet-reviewed")
	if capability.Kind != EndpointOpaque || capability.RetryPolicy != RetryNever {
		t.Fatalf("zero-value registry returned unsafe capability: %#v", capability)
	}
}

func TestEndpointRegistryRegisterRequiresExplicitReviewedPolicy(t *testing.T) {
	registry := EndpointRegistry{}
	if err := registry.Register(EndpointCapability{Method: http.MethodPost, PublicPath: "/v1/future/json", Kind: EndpointOpaque}); err != nil {
		t.Fatal(err)
	}
	capability := registry.Lookup(http.MethodPost, "/v1/future/json")
	if capability.RetryPolicy != RetryNever || capability.Kind != EndpointOpaque {
		t.Fatalf("registered default policy is not fail-closed: %#v", capability)
	}
	if err := registry.Register(EndpointCapability{Method: http.MethodPost, PublicPath: "/v1/future/retry", Kind: EndpointResponses, RetryPolicy: RetryBeforeCommit}); err != nil {
		t.Fatal(err)
	}
	if capability := registry.Lookup(http.MethodPost, "/v1/future/retry"); capability.RetryPolicy != RetryBeforeCommit {
		t.Fatalf("explicit retry policy was not retained: %#v", capability)
	}
}
