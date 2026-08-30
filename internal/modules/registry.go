package modules

import (
	"fmt"
	"sort"
)

// Registry is a deterministic, in-memory collection of validated manifests.
// It is intentionally not a plugin loader.
type Registry struct{ manifests map[string]Manifest }

func NewRegistry() *Registry { return &Registry{manifests: make(map[string]Manifest)} }

func (r *Registry) Load(manifests []Manifest) error {
	if r == nil {
		return fmt.Errorf("nil module registry")
	}
	if r.manifests == nil {
		r.manifests = make(map[string]Manifest)
	}
	for _, manifest := range manifests {
		if err := manifest.Validate(); err != nil {
			return err
		}
		if _, exists := r.manifests[manifest.Name]; exists {
			return fmt.Errorf("duplicate module %q", manifest.Name)
		}
		r.manifests[manifest.Name] = manifest.canonical()
	}
	return nil
}

func (r *Registry) Get(name string) (Manifest, bool) {
	if r == nil {
		return Manifest{}, false
	}
	manifest, ok := r.manifests[name]
	return manifest, ok
}

func (r *Registry) List() []Manifest {
	if r == nil {
		return nil
	}
	result := make([]Manifest, 0, len(r.manifests))
	for _, manifest := range r.manifests {
		result = append(result, manifest)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
