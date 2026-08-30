// Package modules defines the small, declarative module boundary used by the
// Relay control plane. It intentionally describes metadata only; it does not
// load code, spawn processes, or carry credentials.
package modules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const CurrentSchemaVersion = 1

var (
	namePattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	capabilityPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
)

// Manifest is declarative metadata for a built-in Relay module. Keep this
// type deliberately narrow: paths, executable names and credentials are not
// part of the module contract.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	Name          string            `json:"name"`
	DisplayName   string            `json:"displayName"`
	Version       string            `json:"version"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	Routes        []RouteCapability `json:"routes,omitempty"`
}

type RouteCapability struct {
	Path    string   `json:"path"`
	Methods []string `json:"methods"`
}

// Decode validates JSON shape and values, rejecting fields outside the
// manifest contract so future callers cannot accidentally smuggle runtime
// configuration through this boundary.
func Decode(data []byte) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode module manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("decode module manifest: trailing JSON")
		}
		return Manifest{}, fmt.Errorf("decode module manifest: trailing data: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("module %q: unsupported schemaVersion %d", m.Name, m.SchemaVersion)
	}
	if !namePattern.MatchString(m.Name) || len(m.Name) > 64 {
		return fmt.Errorf("module name %q is not safe", m.Name)
	}
	if strings.TrimSpace(m.DisplayName) == "" || len(m.DisplayName) > 128 {
		return fmt.Errorf("module %q: displayName is required and must be at most 128 characters", m.Name)
	}
	if strings.TrimSpace(m.Version) == "" || strings.ContainsAny(m.Version, "\r\n") {
		return fmt.Errorf("module %q: version is required", m.Name)
	}
	seenCaps := make(map[string]struct{}, len(m.Capabilities))
	for _, capability := range m.Capabilities {
		if !capabilityPattern.MatchString(capability) || len(capability) > 96 {
			return fmt.Errorf("module %q: unsafe capability %q", m.Name, capability)
		}
		if _, exists := seenCaps[capability]; exists {
			return fmt.Errorf("module %q: duplicate capability %q", m.Name, capability)
		}
		seenCaps[capability] = struct{}{}
	}
	seenRoutes := make(map[string]struct{}, len(m.Routes))
	for _, route := range m.Routes {
		if !strings.HasPrefix(route.Path, "/v1/") && !strings.HasPrefix(route.Path, "/control/") {
			return fmt.Errorf("module %q: route %q must be under /v1/ or /control/", m.Name, route.Path)
		}
		if strings.ContainsAny(route.Path, "?\r\n") || strings.Contains(route.Path, "..") || len(route.Path) > 256 {
			return fmt.Errorf("module %q: unsafe route %q", m.Name, route.Path)
		}
		if _, exists := seenRoutes[route.Path]; exists {
			return fmt.Errorf("module %q: duplicate route %q", m.Name, route.Path)
		}
		seenRoutes[route.Path] = struct{}{}
		if len(route.Methods) == 0 {
			return fmt.Errorf("module %q: route %q needs at least one method", m.Name, route.Path)
		}
		seenMethods := make(map[string]struct{}, len(route.Methods))
		for _, method := range route.Methods {
			method = strings.ToUpper(method)
			if method != "GET" && method != "POST" && method != "PUT" && method != "PATCH" && method != "DELETE" {
				return fmt.Errorf("module %q: route %q has unsafe method %q", m.Name, route.Path, method)
			}
			if _, exists := seenMethods[method]; exists {
				return fmt.Errorf("module %q: route %q repeats method %q", m.Name, route.Path, method)
			}
			seenMethods[method] = struct{}{}
		}
	}
	return nil
}

func (m Manifest) canonical() Manifest {
	m.Capabilities = append([]string(nil), m.Capabilities...)
	sort.Strings(m.Capabilities)
	m.Routes = append([]RouteCapability(nil), m.Routes...)
	sort.Slice(m.Routes, func(i, j int) bool { return m.Routes[i].Path < m.Routes[j].Path })
	for i := range m.Routes {
		m.Routes[i].Methods = append([]string(nil), m.Routes[i].Methods...)
		sort.Slice(m.Routes[i].Methods, func(a, b int) bool {
			return strings.ToUpper(m.Routes[i].Methods[a]) < strings.ToUpper(m.Routes[i].Methods[b])
		})
	}
	return m
}
