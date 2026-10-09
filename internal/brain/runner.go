package brain

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// wrapperReadLimit bounds how much of a wrapper script is read to check its
// pins; brain's wrappers are a few hundred bytes.
const wrapperReadLimit = 64 << 10

// Runner says how to run bd for one store. Brain resolves a store in its
// wrapper (~/.local/bin/<name>), which pins BD_NAME, BEADS_DIR, the server and
// the render root, so a write made through it meets the same checks as one
// typed at the prompt.
type Runner struct {
	// Wrapper is the store's wrapper script, or "" when the store has none.
	Wrapper string
	// Env pins the store for a plain bd when there is no wrapper.
	Env []string
}

// Runner resolves the command for the store. A wrapper counts only when it
// pins this store's BD_NAME, so an unrelated program that happens to share the
// store's name is never run.
func (s Store) Runner() Runner {
	if wrapper := s.wrapper(); wrapper != "" {
		return Runner{Wrapper: wrapper}
	}
	return Runner{Env: []string{"BD_NAME=" + s.Name, "BEADS_DIR=" + s.Path}}
}

// Getenv answers for the environment bd would run in for this store: the
// variables the store's wrapper exports (the Dolt server's host and port among
// them), then b9s's own environment. Reading the pins keeps b9s dialling the
// server the store's bd dials, where the store's metadata names no port.
func (s Store) Getenv() func(string) string {
	pins := map[string]string{}
	if wrapper := s.wrapper(); wrapper != "" {
		if script, ok := readScript(wrapper); ok {
			pins = exportedPins(script)
		}
	}
	return func(name string) string {
		if v, ok := pins[name]; ok {
			return v
		}
		return os.Getenv(name)
	}
}

// exportPin matches `export NAME=value` at the start of a line, the form
// brain's server-store wrapper pins its environment in.
var exportPin = regexp.MustCompile(`(?m)^[ \t]*export[ \t]+([A-Za-z_][A-Za-z0-9_]*)=(.*?)[ \t]*$`)

func exportedPins(script string) map[string]string {
	pins := map[string]string{}
	for _, m := range exportPin.FindAllStringSubmatch(script, -1) {
		value := m[2]
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(value, "'")
		}
		pins[m[1]] = value
	}
	return pins
}

func readScript(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	buf := make([]byte, wrapperReadLimit)
	n, _ := f.Read(buf)
	return string(buf[:n]), true
}

// wrapper finds the store's wrapper in the directory brain writes it to.
func (s Store) wrapper() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	path := filepath.Join(home, ".local", "bin", s.Name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	script, ok := readScript(path)
	if !ok || !pinsName(script, s.Name) {
		return ""
	}
	return path
}

// pinsName reports whether script sets BD_NAME to name, in the two shapes
// brain's wrappers use: an export line and an env prefix on the exec line.
func pinsName(script, name string) bool {
	re := regexp.MustCompile(`(?m)(^|\s)BD_NAME=["']?` + regexp.QuoteMeta(name) + `["']?(\s|$)`)
	return re.MatchString(script)
}
