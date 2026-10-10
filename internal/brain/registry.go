// Package brain adapts b9s to brain, a fork of Beads whose named stores share
// one Dolt database (ADR 0052). It reads brain's store registry, resolves the
// command that runs bd for a store, and scopes reads to a store's namespace
// with the rule brain's own narrow read uses. It imports nothing else from
// b9s, so every other package can depend on it.
package brain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// EnvRegistry names a registry file. Brain itself reads only the files
	// under the home directory; this override exists so a scratch registry can
	// stand in for the real one.
	EnvRegistry = "BRAIN_STORES_FILE"
	// EnvMode switches brain mode: "on" requires a registry, "off" ignores it.
	EnvMode = "B9S_BRAIN"

	registryRel       = ".config/brain/stores.yaml"
	registryLegacyRel = ".config/pai/stores.yaml"
	beadsDirName      = ".beads"
)

// Store is one entry of brain's registry: a named store and its .beads
// directory.
type Store struct {
	Name  string
	Path  string // the store's .beads directory
	About string
}

// Dir is the checkout directory of the store, the folder that holds its .beads
// directory. It is empty when the registry path is not a .beads directory,
// because b9s finds a project's sources through <dir>/.beads.
func (s Store) Dir() string {
	if s.Path == "" || filepath.Base(filepath.Clean(s.Path)) != beadsDirName {
		return ""
	}
	return filepath.Dir(filepath.Clean(s.Path))
}

// Registry is the parsed store registry.
type Registry struct {
	File   string
	Stores []Store // sorted by name
}

// Store returns the store called name.
func (r *Registry) Store(name string) (Store, bool) {
	if r == nil {
		return Store{}, false
	}
	for _, s := range r.Stores {
		if s.Name == name {
			return s, true
		}
	}
	return Store{}, false
}

// Names lists the store names in order.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	names := make([]string, len(r.Stores))
	for i, s := range r.Stores {
		names[i] = s.Name
	}
	return names
}

// ForDir finds the store whose checkout directory is dir. Several stores can
// share a directory, so preferred (the BD_NAME of the caller's environment)
// breaks a tie; without a unique answer ok is false and b9s must not guess.
func (r *Registry) ForDir(dir, preferred string) (Store, bool) {
	if r == nil || dir == "" {
		return Store{}, false
	}
	dir = filepath.Clean(dir)
	var matches []Store
	for _, s := range r.Stores {
		if d := s.Dir(); d != "" && d == dir {
			matches = append(matches, s)
		}
	}
	switch {
	case len(matches) == 1:
		return matches[0], true
	case len(matches) > 1:
		for _, s := range matches {
			if s.Name == preferred {
				return s, true
			}
		}
	}
	return Store{}, false
}

// Mode says whether b9s runs in brain mode.
type Mode uint8

const (
	// Auto turns brain mode on when a registry is present.
	Auto Mode = iota
	// On requires a registry.
	On
	// Off ignores any registry, so b9s behaves as it does without brain.
	Off
)

// ParseMode reads the value of B9S_BRAIN. An empty value is Auto.
func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return Auto, nil
	case "1", "on", "true", "yes":
		return On, nil
	case "0", "off", "false", "no":
		return Off, nil
	}
	return Auto, fmt.Errorf("%s=%q: want on, off or auto", EnvMode, value)
}

// ErrNoRegistry reports that no brain store registry exists.
var ErrNoRegistry = errors.New("no brain store registry")

// Open returns the registry brain mode runs on, or nil when b9s should behave
// as it does without brain. Auto finds the registry by itself and reports a
// broken file as an error the caller may downgrade to a warning; On fails
// when there is no registry.
func Open(mode Mode) (*Registry, error) {
	if mode == Off {
		return nil, nil
	}
	file, ok := registryFile()
	if !ok {
		if mode == On {
			return nil, fmt.Errorf("%w: looked for %s", ErrNoRegistry, strings.Join(registryCandidates(), ", "))
		}
		return nil, nil
	}
	reg, err := Load(file)
	if err != nil {
		return nil, err
	}
	if len(reg.Stores) == 0 {
		if mode == On {
			return nil, fmt.Errorf("%s lists no stores", file)
		}
		return nil, nil
	}
	return reg, nil
}

// registryCandidates lists the files that can hold the registry, best first.
func registryCandidates() []string {
	if env := os.Getenv(EnvRegistry); env != "" {
		return []string{env}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	return []string{filepath.Join(home, registryRel), filepath.Join(home, registryLegacyRel)}
}

func registryFile() (string, bool) {
	for _, file := range registryCandidates() {
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			return file, true
		}
	}
	return "", false
}

// Load parses a registry file. An entry is either a bare path or a mapping
// with a path, as brain writes them.
func Load(file string) (*Registry, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading brain registry: %w", err)
	}
	var raw struct {
		Stores map[string]yaml.Node `yaml:"stores"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing brain registry %s: %w", file, err)
	}
	reg := &Registry{File: file}
	for name, node := range raw.Stores {
		store := Store{Name: name}
		switch node.Kind {
		case yaml.ScalarNode:
			store.Path = node.Value
		case yaml.MappingNode:
			var entry struct {
				Path  string `yaml:"path"`
				About string `yaml:"about"`
			}
			if err := node.Decode(&entry); err != nil {
				return nil, fmt.Errorf("parsing brain registry %s: store %s: %w", file, name, err)
			}
			store.Path, store.About = entry.Path, entry.About
		default:
			return nil, fmt.Errorf("parsing brain registry %s: store %s is neither a path nor a mapping", file, name)
		}
		if store.Path == "" {
			return nil, fmt.Errorf("parsing brain registry %s: store %s has no path", file, name)
		}
		reg.Stores = append(reg.Stores, store)
	}
	sort.Slice(reg.Stores, func(i, j int) bool { return reg.Stores[i].Name < reg.Stores[j].Name })
	return reg, nil
}
