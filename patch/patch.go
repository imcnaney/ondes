// Package patch loads YAML synth programs and applies them to fresh
// voices. The on-disk format mirrors the Java synth: a top-level map of
// component name -> spec, where each spec has a `type` (which routes to
// a registered component.Factory) and an optional `out` directive
// describing where this component's output is wired.
//
// `out: main` plugs into the voice's main summing junction.
// `out: <name>` plugs into another component's default ("main") input.
// `out: <name>.<select>` plugs into a named input on another component.
package patch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"ondes/component"
	"ondes/synth"
)

// Patch is a parsed YAML program. It is immutable after Load and can be
// applied to many voices.
type Patch struct {
	name  string
	specs map[string]component.Spec
	// Stable instantiation order so that `out:` resolution is
	// deterministic and Apply produces the same wire topology every time.
	order []string
}

func (p *Patch) Name() string { return p.name }

// Load resolves a patch by name, searching the on-disk `./program/`
// tree first (recursively) and then the bundled patches under
// `src/main/resources/program/`. Substring matching is supported to
// mirror the Java loader: callers can pass `program/sine` to
// disambiguate against `bell-organ` etc.
func Load(name string) (*Patch, error) {
	path, err := resolvePath(name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parse(name, data)
}

func resolvePath(name string) (string, error) {
	candidates, err := collectPaths()
	if err != nil {
		return "", err
	}
	// Exact basename match wins.
	target := name + ".yaml"
	for _, c := range candidates {
		if filepath.Base(c) == target {
			return c, nil
		}
	}
	// Otherwise first substring match (case-insensitive against the
	// path fragment after the program root).
	lname := strings.ToLower(name)
	for _, c := range candidates {
		if strings.Contains(strings.ToLower(c), lname) && strings.HasSuffix(c, ".yaml") {
			return c, nil
		}
	}
	return "", fmt.Errorf("patch %q not found", name)
}

func collectPaths() ([]string, error) {
	var out []string
	for _, root := range []string{"program", filepath.Join("src", "main", "resources", "program")} {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(p, ".yaml") {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func parse(name string, data []byte) (*Patch, error) {
	var raw map[string]map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("patch %s: %w", name, err)
	}
	p := &Patch{name: name, specs: map[string]component.Spec{}}
	for k, v := range raw {
		p.specs[k] = component.Spec(v)
		p.order = append(p.order, k)
	}
	sort.Strings(p.order)
	return p, nil
}

// Apply instantiates this patch into a fresh voice. It runs in two
// passes: first Make + Configure every component, then resolve `out:`
// directives now that every component (and its Output() wire) exists.
func (p *Patch) Apply(v *synth.Voice) error {
	insts := make(map[string]component.Component, len(p.order))
	for _, name := range p.order {
		spec := p.specs[name]
		typeKey, _ := spec["type"].(string)
		if typeKey == "" {
			return fmt.Errorf("patch %s: component %q missing type", p.name, name)
		}
		c, err := component.Make(typeKey)
		if err != nil {
			return fmt.Errorf("patch %s: component %q: %w", p.name, name, err)
		}
		if err := c.Configure(spec, v, name); err != nil {
			return fmt.Errorf("patch %s: component %q: %w", p.name, name, err)
		}
		insts[name] = c
		v.AddComponent(name, c)
	}
	for _, name := range p.order {
		c := insts[name]
		spec := p.specs[name]
		if raw, ok := spec["out"]; ok {
			dest, ok := raw.(string)
			if !ok {
				return fmt.Errorf("patch %s: component %q: out must be a string", p.name, name)
			}
			if err := wireOut(p.name, name, dest, c.Output(), insts, v); err != nil {
				return err
			}
		}
		// Sidechain outputs: nested-map (linear-out / log-out on
		// midi-note) and flat-string (out-level on env). Wire each
		// according to the component's exposed named output.
		for _, key := range []string{"linear-out", "log-out"} {
			dest, ok := nestedOut(spec, key)
			if !ok {
				continue
			}
			if w := namedOutput(c, key); w != nil {
				if err := wireOut(p.name, name, dest, w, insts, v); err != nil {
					return err
				}
			}
		}
		if dest, ok := spec["out-level"].(string); ok {
			if w := namedOutput(c, "out-level"); w != nil {
				if err := wireOut(p.name, name, dest, w, insts, v); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// nestedOut is the `linear-out: { amp:..., out: "lpf.freq" }` pattern:
// returns the target named in the block's `out:` field.
func nestedOut(spec component.Spec, key string) (string, bool) {
	m, ok := spec[key].(map[string]any)
	if !ok {
		return "", false
	}
	dest, ok := m["out"].(string)
	return dest, ok
}

// namedOutput returns a sidechain wire from a component, if it exposes one
// under the given pin name. Components opt in by implementing methods of
// the same name as the spec key (LinearOut / LogOut / LevelOut).
func namedOutput(c component.Component, key string) *synth.Wire {
	type linearOuter interface{ LinearOut() *synth.Wire }
	type logOuter interface{ LogOut() *synth.Wire }
	type levelOuter interface{ LevelOut() *synth.Wire }
	switch key {
	case "linear-out":
		if x, ok := c.(linearOuter); ok {
			return x.LinearOut()
		}
	case "log-out":
		if x, ok := c.(logOuter); ok {
			return x.LogOut()
		}
	case "out-level":
		if x, ok := c.(levelOuter); ok {
			return x.LevelOut()
		}
	}
	return nil
}

func wireOut(patchName, srcName, dest string, src *synth.Wire, insts map[string]component.Component, v *synth.Voice) error {
	if src == nil {
		return nil
	}
	if dest == "main" {
		v.AddVoiceMixInput(src)
		return nil
	}
	target, sel := dest, "main"
	if i := strings.IndexByte(dest, '.'); i >= 0 {
		target, sel = dest[:i], dest[i+1:]
	}
	dst, ok := insts[target]
	if !ok {
		return fmt.Errorf("patch %s: component %q out: target %q not found", patchName, srcName, target)
	}
	in, ok := dst.(component.Inputter)
	if !ok {
		return fmt.Errorf("patch %s: component %q out: target %q does not accept inputs", patchName, srcName, target)
	}
	in.AddInput(sel, src)
	return nil
}
