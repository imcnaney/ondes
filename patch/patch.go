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
		raw, ok := p.specs[name]["out"]
		if !ok {
			continue
		}
		dest, ok := raw.(string)
		if !ok {
			return fmt.Errorf("patch %s: component %q: out must be a string", p.name, name)
		}
		if err := wireOut(p.name, name, dest, c, insts, v); err != nil {
			return err
		}
	}
	return nil
}

func wireOut(patchName, srcName, dest string, src component.Component, insts map[string]component.Component, v *synth.Voice) error {
	if dest == "main" {
		v.AddVoiceMixInput(src.Output())
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
	in.AddInput(sel, src.Output())
	return nil
}
