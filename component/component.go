// Package component defines the component interface, the per-voice spec
// type, and a registry that maps YAML `type` keys to constructors.
package component

import (
	"fmt"

	"ondes/synth"
)

// Spec is the deserialized YAML map for a single component definition.
type Spec map[string]any

// Component is anything that participates in a Voice's audio graph.
// Configure runs at voice instantiation; Output is the wire others can
// pull from. Components that consume inputs additionally implement
// Inputter; those that listen to MIDI implement synth.MidiListener.
type Component interface {
	Configure(spec Spec, v *synth.Voice, name string) error
	Output() *synth.Wire
}

// Inputter is implemented by components that can be named as the
// destination of an `out:` directive. AddInput attaches src to the
// receiver's input channel named by `select` (commonly "main").
type Inputter interface {
	AddInput(selectName string, src *synth.Wire)
}

type Factory func() Component

var registry = map[string]Factory{}

// Register associates a YAML `type` value with a constructor.
// Component packages call this from init().
func Register(typeKey string, f Factory) {
	if _, dup := registry[typeKey]; dup {
		panic("component: duplicate registration for type " + typeKey)
	}
	registry[typeKey] = f
}

// Make returns a fresh component for the given type, or an error if
// the type is unknown.
func Make(typeKey string) (Component, error) {
	f, ok := registry[typeKey]
	if !ok {
		return nil, fmt.Errorf("unknown component type %q", typeKey)
	}
	return f(), nil
}
