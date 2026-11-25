package core

import "fmt"

// Registry holds the registered features in registration order. The CLI and TUI
// are assembled from it; neither knows feature names.
type Registry struct {
	features []Feature
	byName   map[string]Feature
	primary  Feature
}

func NewRegistry() *Registry {
	return &Registry{byName: map[string]Feature{}}
}

// Register validates and adds a feature. Registration is the only place that
// knows the ordering and uniqueness rules, so a new feature package only has to
// call Register from its own registration file.
func (r *Registry) Register(f Feature) error {
	if f == nil {
		return fmt.Errorf("cannot register a nil feature")
	}
	d := f.Descriptor()
	if err := validateDescriptor(d); err != nil {
		return err
	}
	if _, exists := r.byName[d.Name]; exists {
		return fmt.Errorf("feature %q is already registered", d.Name)
	}
	if d.Primary && r.primary != nil {
		return fmt.Errorf("feature %q: primary feature already registered as %q", d.Name, r.primary.Descriptor().Name)
	}
	r.features = append(r.features, f)
	r.byName[d.Name] = f
	if d.Primary {
		r.primary = f
	}
	return nil
}

// MustRegister registers or panics. Registration files use it at startup.
func (r *Registry) MustRegister(f Feature) {
	if err := r.Register(f); err != nil {
		panic(err)
	}
}

// Get looks up a feature by machine name.
func (r *Registry) Get(name string) (Feature, bool) {
	f, ok := r.byName[name]
	return f, ok
}

// All returns registered features in registration order.
func (r *Registry) All() []Feature {
	out := make([]Feature, len(r.features))
	copy(out, r.features)
	return out
}

// Validate checks the parts of the registration contract that span features,
// such as a menu entry naming a group declared by another descriptor. Register
// validates one descriptor at a time, so the assembly calls this once after the
// last registration and a broken menu fails at startup instead of drawing a row
// in the wrong place.
func (r *Registry) Validate() error {
	return r.validateMenuLinks()
}

// Primary returns the feature a TUI opens on, if one is registered.
func (r *Registry) Primary() (Feature, bool) {
	return r.primary, r.primary != nil
}
