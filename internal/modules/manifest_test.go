package modules

import (
	"reflect"
	"testing"
)

func validManifest(name string) Manifest {
	return Manifest{SchemaVersion: 1, Name: name, DisplayName: name, Version: "0.1.0", Capabilities: []string{"quota.read", "pool.manage"}, Routes: []RouteCapability{{Path: "/control/pool", Methods: []string{"get", "POST"}}}}
}

func TestDecodeRejectsRuntimeFields(t *testing.T) {
	_, err := Decode([]byte(`{"schemaVersion":1,"name":"pool","displayName":"Pool","version":"1","executable":"codex.real.exe"}`))
	if err == nil {
		t.Fatal("expected arbitrary executable field to be rejected")
	}
}

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	data := `{"schemaVersion":1,"name":"pool","displayName":"Pool","version":"1"}{"extra":true}`
	if _, err := Decode([]byte(data)); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestManifestValidationRejectsUnsafeValues(t *testing.T) {
	cases := []Manifest{validManifest("../pool"), validManifest("Pool"), validManifest("pool")}
	cases[2].Routes[0].Path = "/etc/passwd"
	for _, manifest := range cases {
		if err := manifest.Validate(); err == nil {
			t.Errorf("expected %q to be rejected", manifest.Name)
		}
	}
}

func TestRegistryListIsDeterministicAndCanonical(t *testing.T) {
	first := validManifest("z-module")
	second := validManifest("a-module")
	registry := NewRegistry()
	if err := registry.Load([]Manifest{first, second}); err != nil {
		t.Fatal(err)
	}
	got := registry.List()
	if got[0].Name != "a-module" || got[1].Name != "z-module" {
		t.Fatalf("unexpected order: %#v", got)
	}
	wantCaps := []string{"pool.manage", "quota.read"}
	if !reflect.DeepEqual(got[0].Capabilities, wantCaps) {
		t.Fatalf("capabilities not canonicalized: %#v", got[0].Capabilities)
	}
	if err := registry.Load([]Manifest{validManifest("a-module")}); err == nil {
		t.Fatal("expected duplicate module to be rejected")
	}
}
