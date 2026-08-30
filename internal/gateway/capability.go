package gateway

import (
	"errors"
	"strings"
)

// EndpointKind describes how Relay treats a public /v1 route.  The opaque
// kind is deliberately conservative: Relay forwards bytes without attempting
// to interpret an endpoint that has not been reviewed yet.
type EndpointKind string

const (
	EndpointResponses EndpointKind = "responses"
	EndpointCompact   EndpointKind = "compact"
	EndpointModels    EndpointKind = "models"
	EndpointOpaque    EndpointKind = "opaque"
)

// RetryPolicy controls whether a request may be replayed on another pooled
// source before a public response has been committed.  Unknown endpoints are
// fail-closed by default because their side effects are not known to Relay.
type RetryPolicy string

const (
	RetryBeforeCommit RetryPolicy = "before_commit"
	RetryNever        RetryPolicy = "never"
)

// EndpointCapability is the protocol contract Relay knows for one route.
// UpstreamPath is relative to the configured Codex endpoint; an empty value
// means the generic /v1 path mapping is used.
type EndpointCapability struct {
	Method       string
	PublicPath   string
	Kind         EndpointKind
	RetryPolicy  RetryPolicy
	UpstreamPath string
}

// EndpointRegistry keeps the reviewed surface explicit while retaining an
// upstream-first escape hatch for future Codex endpoints.
type EndpointRegistry struct {
	entries map[string]EndpointCapability
}

// NewEndpointRegistry returns the default native Codex capability registry.
// The map key includes the HTTP method so a known route cannot accidentally
// inherit a policy intended for another verb.
func NewEndpointRegistry() EndpointRegistry {
	return EndpointRegistry{entries: map[string]EndpointCapability{
		key(httpMethodPost, "/v1/responses"):         {Method: httpMethodPost, PublicPath: "/v1/responses", Kind: EndpointResponses, RetryPolicy: RetryBeforeCommit, UpstreamPath: "responses"},
		key(httpMethodPost, "/v1/responses/compact"): {Method: httpMethodPost, PublicPath: "/v1/responses/compact", Kind: EndpointCompact, RetryPolicy: RetryBeforeCommit, UpstreamPath: "responses/compact"},
		key(httpMethodGet, "/v1/models"):             {Method: httpMethodGet, PublicPath: "/v1/models", Kind: EndpointModels, RetryPolicy: RetryNever, UpstreamPath: "models"},
	}}
}

// Register adds a reviewed route to the registry. It is intentionally
// explicit: callers must opt into a retry policy for a future endpoint rather
// than weakening the safe opaque default.
func (r *EndpointRegistry) Register(capability EndpointCapability) error {
	if r == nil {
		return errors.New("nil endpoint registry")
	}
	method := strings.ToUpper(strings.TrimSpace(capability.Method))
	path := strings.TrimSpace(capability.PublicPath)
	if method == "" || path == "" || !strings.HasPrefix(path, "/v1/") {
		return errors.New("endpoint capability requires an HTTP method and /v1/ path")
	}
	if capability.Kind == "" {
		return errors.New("endpoint capability kind is required")
	}
	if capability.RetryPolicy == "" {
		capability.RetryPolicy = RetryNever
	}
	capability.Method, capability.PublicPath = method, path
	if r.entries == nil {
		r.entries = NewEndpointRegistry().entries
	}
	r.entries[key(method, path)] = capability
	return nil
}

// Lookup returns a reviewed capability or the conservative opaque default.
// A fresh registry value (including the zero value) remains useful and always
// fails closed for unknown routes.
func (r EndpointRegistry) Lookup(method, publicPath string) EndpointCapability {
	publicPath = strings.TrimSpace(publicPath)
	method = strings.ToUpper(strings.TrimSpace(method))
	entries := r.entries
	if entries == nil {
		entries = NewEndpointRegistry().entries
	}
	if capability, ok := entries[key(method, publicPath)]; ok {
		return capability
	}
	return EndpointCapability{Method: method, PublicPath: publicPath, Kind: EndpointOpaque, RetryPolicy: RetryNever}
}

func key(method, publicPath string) string { return strings.ToUpper(method) + " " + publicPath }

// Keep method literals local to avoid exporting another protocol surface.
const (
	httpMethodGet  = "GET"
	httpMethodPost = "POST"
)
