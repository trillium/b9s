package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// isolate points HOME at an empty directory and clears the overrides, so a
// test never reads the registry of the machine it runs on.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvRegistry, "")
	return home
}

const sampleRegistry = `# brain federation store registry
stores:
    brain:
        path: /srv/brain/.beads
        about: Knowledge and notes
    task:
        path: /srv/task/.beads
    legacy: /srv/legacy/.beads
`

func TestLoadReadsMappingAndScalarEntries(t *testing.T) {
	home := isolate(t)
	file := filepath.Join(home, "stores.yaml")
	writeFile(t, file, sampleRegistry, 0o644)

	reg, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	got := reg.Names()
	want := []string{"brain", "legacy", "task"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
	brain, _ := reg.Store("brain")
	if brain.Path != "/srv/brain/.beads" || brain.About != "Knowledge and notes" {
		t.Errorf("brain = %+v", brain)
	}
	legacy, _ := reg.Store("legacy")
	if legacy.Path != "/srv/legacy/.beads" {
		t.Errorf("a bare path entry must load as the path, got %+v", legacy)
	}
}

func TestLoadRejectsAStoreWithoutAPath(t *testing.T) {
	home := isolate(t)
	file := filepath.Join(home, "stores.yaml")
	writeFile(t, file, "stores:\n  empty:\n    about: nothing\n", 0o644)
	if _, err := Load(file); err == nil {
		t.Fatal("a store with no path must not load")
	}
}

func TestStoreDirIsTheFolderHoldingDotBeads(t *testing.T) {
	if got := (Store{Path: "/srv/task/.beads"}).Dir(); got != "/srv/task" {
		t.Errorf("Dir = %q", got)
	}
	// b9s finds sources through <dir>/.beads, so any other shape has no checkout.
	if got := (Store{Path: "/srv/task/data"}).Dir(); got != "" {
		t.Errorf("a path that is not a .beads directory must have no Dir, got %q", got)
	}
}

func TestOpenAutoFindsTheRegistryUnderHome(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, ".config/brain/stores.yaml"), sampleRegistry, 0o644)
	reg, err := Open(Auto)
	if err != nil || reg == nil {
		t.Fatalf("Open(Auto) = %v, %v", reg, err)
	}
	if len(reg.Stores) != 3 {
		t.Errorf("stores = %d", len(reg.Stores))
	}
}

func TestOpenAutoFallsBackToTheLegacyPaiPath(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, ".config/pai/stores.yaml"), sampleRegistry, 0o644)
	reg, err := Open(Auto)
	if err != nil || reg == nil {
		t.Fatalf("Open(Auto) = %v, %v", reg, err)
	}
}

func TestOpenAutoWithoutARegistryIsNotBrainMode(t *testing.T) {
	isolate(t)
	reg, err := Open(Auto)
	if reg != nil || err != nil {
		t.Fatalf("Open(Auto) = %v, %v; b9s must behave as upstream without a registry", reg, err)
	}
}

func TestOpenOffIgnoresARegistry(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, ".config/brain/stores.yaml"), sampleRegistry, 0o644)
	reg, err := Open(Off)
	if reg != nil || err != nil {
		t.Fatalf("Open(Off) = %v, %v", reg, err)
	}
}

func TestOpenOnWithoutARegistryFails(t *testing.T) {
	isolate(t)
	if _, err := Open(On); err == nil {
		t.Fatal("Open(On) without a registry must fail")
	}
}

func TestOpenHonoursTheRegistryOverride(t *testing.T) {
	isolate(t)
	file := filepath.Join(t.TempDir(), "scratch.yaml")
	writeFile(t, file, sampleRegistry, 0o644)
	t.Setenv(EnvRegistry, file)
	reg, err := Open(Auto)
	if err != nil || reg == nil || reg.File != file {
		t.Fatalf("Open(Auto) = %+v, %v", reg, err)
	}
}

func TestOpenReportsABrokenRegistry(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, ".config/brain/stores.yaml"), "stores: [not, a, map", 0o644)
	if _, err := Open(Auto); err == nil {
		t.Fatal("a broken registry must be reported, not mistaken for no registry")
	}
}

func TestParseMode(t *testing.T) {
	for value, want := range map[string]Mode{"": Auto, "auto": Auto, "on": On, "1": On, "off": Off, "0": Off, " OFF ": Off} {
		got, err := ParseMode(value)
		if err != nil || got != want {
			t.Errorf("ParseMode(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	if _, err := ParseMode("maybe"); err == nil {
		t.Error("an unknown mode must be an error")
	}
}

func TestForDirNeedsAUniqueStore(t *testing.T) {
	reg := &Registry{Stores: []Store{
		{Name: "a", Path: "/srv/one/.beads"},
		{Name: "b", Path: "/srv/shared/.beads"},
		{Name: "c", Path: "/srv/shared/.beads"},
	}}
	if s, ok := reg.ForDir("/srv/one", ""); !ok || s.Name != "a" {
		t.Errorf("ForDir(one) = %v, %v", s, ok)
	}
	if _, ok := reg.ForDir("/srv/shared", ""); ok {
		t.Error("a directory shared by two stores must not be guessed")
	}
	if s, ok := reg.ForDir("/srv/shared", "c"); !ok || s.Name != "c" {
		t.Errorf("BD_NAME must break the tie, got %v, %v", s, ok)
	}
	if _, ok := reg.ForDir("/srv/elsewhere", ""); ok {
		t.Error("an unknown directory is no store")
	}
}
