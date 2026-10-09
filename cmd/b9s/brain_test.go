package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vanderheijden86/b9s/internal/brain"
	"github.com/vanderheijden86/b9s/internal/datasource"
	"github.com/vanderheijden86/b9s/pkg/config"
	"github.com/vanderheijden86/b9s/pkg/model"
)

// brainHome writes a registry of two stores under a fresh HOME and returns the
// stores' folders.
func brainHome(t *testing.T) (task, notes string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(brain.EnvRegistry, "")
	t.Setenv(brain.EnvMode, "")
	root := t.TempDir()
	task, notes = filepath.Join(root, "task"), filepath.Join(root, "notes")
	for _, dir := range []string{task, notes} {
		if err := os.MkdirAll(filepath.Join(dir, ".beads"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	registry := "stores:\n  task:\n    path: " + filepath.Join(task, ".beads") + "\n  notes:\n    path: " + filepath.Join(notes, ".beads") + "\n"
	if err := os.MkdirAll(filepath.Join(home, ".config/brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config/brain/stores.yaml"), []byte(registry), 0o644); err != nil {
		t.Fatal(err)
	}
	return task, notes
}

func TestResolveBrainWithoutARegistryIsUpstreamB9s(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(brain.EnvRegistry, "")
	t.Setenv(brain.EnvMode, "")
	got, err := resolveBrain(brainFlags{}, "/work/proj", "", &bytes.Buffer{})
	if err != nil || got.Registry != nil || got.Store != nil {
		t.Fatalf("resolveBrain = %+v, %v; without a registry nothing changes", got, err)
	}
}

func TestResolveBrainIsAutomaticWithARegistry(t *testing.T) {
	task, _ := brainHome(t)
	got, err := resolveBrain(brainFlags{}, "/somewhere/else", "", &bytes.Buffer{})
	if err != nil || got.Registry == nil || got.Store != nil {
		t.Fatalf("resolveBrain = %+v, %v; brain mode on, no store chosen", got, err)
	}
	got, err = resolveBrain(brainFlags{}, task, "", &bytes.Buffer{})
	if err != nil || got.Store == nil || got.Store.Name != "task" {
		t.Fatalf("started in a store's folder: %+v, %v", got, err)
	}
}

func TestResolveBrainOffIgnoresTheRegistry(t *testing.T) {
	task, _ := brainHome(t)
	got, err := resolveBrain(brainFlags{off: true}, task, "", &bytes.Buffer{})
	if err != nil || got.Registry != nil {
		t.Fatalf("--no-brain: %+v, %v", got, err)
	}
	t.Setenv(brain.EnvMode, "off")
	if got, err := resolveBrain(brainFlags{}, task, "", &bytes.Buffer{}); err != nil || got.Registry != nil {
		t.Fatalf("B9S_BRAIN=off: %+v, %v", got, err)
	}
}

func TestResolveBrainStoreFlagPicksTheStoreFromAnywhere(t *testing.T) {
	_, notes := brainHome(t)
	got, err := resolveBrain(brainFlags{store: "notes"}, "/somewhere/else", "", &bytes.Buffer{})
	if err != nil || got.Store == nil || got.Store.Dir() != notes {
		t.Fatalf("resolveBrain = %+v, %v", got, err)
	}
	if _, err := resolveBrain(brainFlags{store: "nope"}, "/x", "", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "notes, task") {
		t.Errorf("an unknown store must name the known ones, got %v", err)
	}
}

func TestResolveBrainRequiredWithoutARegistryFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(brain.EnvRegistry, "")
	t.Setenv(brain.EnvMode, "")
	if _, err := resolveBrain(brainFlags{on: true}, "/x", "", &bytes.Buffer{}); err == nil {
		t.Fatal("--brain without a registry must fail")
	}
}

func TestResolveBrainBrokenRegistryWarnsInAutomaticMode(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(brain.EnvRegistry, "")
	t.Setenv(brain.EnvMode, "")
	if err := os.MkdirAll(filepath.Join(home, ".config/brain"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config/brain/stores.yaml"), []byte("stores: [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warn bytes.Buffer
	got, err := resolveBrain(brainFlags{}, "/x", "", &warn)
	if err != nil || got.Registry != nil {
		t.Fatalf("resolveBrain = %+v, %v; an ordinary project must still open", got, err)
	}
	if !strings.Contains(warn.String(), "ignoring the brain registry") {
		t.Errorf("warning = %q", warn.String())
	}
	if _, err := resolveBrain(brainFlags{on: true}, "/x", "", &warn); err == nil {
		t.Error("--brain must not ignore a broken registry")
	}
}

func TestChooseStartupProjectOpensAStoreThroughItsNamespace(t *testing.T) {
	task, _ := brainHome(t)
	store := brain.Store{Name: "task", Path: filepath.Join(task, ".beads")}
	var targets []datasource.OpenTarget
	open := func(target datasource.OpenTarget) (datasource.OpenedProject, *datasource.OpenFailure) {
		targets = append(targets, target)
		return datasource.OpenedProject{Issues: make([]model.Issue, 2)}, nil
	}

	choice, err := chooseStartupProject("task", task, &store, &brain.Registry{Stores: []brain.Store{store}}, configWithOpened(), true, open)

	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Store == nil || targets[0].Store.Name != "task" {
		t.Errorf("targets = %+v, want the task store", targets)
	}
	if choice.Store == nil || choice.Store.Name != "task" {
		t.Errorf("choice = %+v", choice)
	}
}

func TestChooseStartupProjectFallsBackToARecentStore(t *testing.T) {
	task, _ := brainHome(t)
	store := brain.Store{Name: "task", Path: filepath.Join(task, ".beads")}
	registry := &brain.Registry{Stores: []brain.Store{store}}
	cfg := configWithOpened(config.RecentProject{Name: "task", Store: "task", Path: task, OpenedAt: time.Now()})
	var targets []datasource.OpenTarget
	open := func(target datasource.OpenTarget) (datasource.OpenedProject, *datasource.OpenFailure) {
		targets = append(targets, target)
		if target.Store == nil {
			return datasource.OpenedProject{}, &datasource.OpenFailure{Reason: datasource.OpenNotAProject, Project: target.Name, Dir: target.Dir}
		}
		return datasource.OpenedProject{}, nil
	}

	choice, err := chooseStartupProject("ghost", "/work/ghost", nil, registry, cfg, true, open)

	if err != nil {
		t.Fatal(err)
	}
	if choice.Store == nil || choice.Store.Name != "task" || choice.StartupFailure == nil {
		t.Errorf("choice = %+v, want the task store after ghost failed", choice)
	}
}

func TestChooseStartupProjectNeverOpensARecentStoreAsAFolder(t *testing.T) {
	task, _ := brainHome(t)
	cfg := configWithOpened(config.RecentProject{Name: "task", Store: "task", Path: task, OpenedAt: time.Now()})
	open := func(target datasource.OpenTarget) (datasource.OpenedProject, *datasource.OpenFailure) {
		if target.Dir == task {
			t.Fatalf("opened the store's folder %+v as a plain checkout", target)
		}
		return datasource.OpenedProject{}, &datasource.OpenFailure{Reason: datasource.OpenNotAProject, Project: target.Name, Dir: target.Dir}
	}

	// Without a registry (b9s --no-brain) the store has no namespace to open through.
	if _, err := chooseStartupProject("ghost", "/work/ghost", nil, nil, cfg, true, open); err == nil {
		t.Fatal("there is nothing else to fall back to")
	}
}
