package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vanderheijden86/b9s/internal/brain"
	"github.com/vanderheijden86/b9s/internal/datasource"
	"github.com/vanderheijden86/b9s/pkg/config"
)

// projectOpener loads a project; datasource.OpenProject in production.
type projectOpener func(datasource.OpenTarget) (datasource.OpenedProject, *datasource.OpenFailure)

// startupChoice is the project b9s opens at startup.
type startupChoice struct {
	Name string
	Dir  string
	// Store is the brain store the project opened as, or nil for a checkout.
	Store  *brain.Store
	Opened datasource.OpenedProject
	// StartupFailure is why the folder b9s was started in did not open, when
	// b9s fell back to the last project that opened successfully.
	StartupFailure *datasource.OpenFailure
}

// chooseStartupProject opens the startup folder, or, when that fails in an
// interactive terminal, the project that most recently opened successfully.
//
// Without a terminal b9s never opens a different project: a script that
// starts b9s in the wrong folder must fail, not quietly act on other data.
// Only a recent project with a local checkout can be the fallback, because a
// database without one needs the startup project's Dolt user to connect.
//
// In brain mode the startup folder may be a brain store (store is not nil), and
// a recent project that is a store opens as that store, through registry. A
// store is never opened as a plain checkout: its folder shares a database with
// other stores.
func chooseStartupProject(name, dir string, store *brain.Store, registry *brain.Registry, cfg *config.Config, interactive bool, open projectOpener) (startupChoice, error) {
	opened, failure := open(openTarget(name, dir, store))
	if failure == nil {
		return startupChoice{Name: name, Dir: dir, Store: store, Opened: opened}, nil
	}
	if !interactive {
		return startupChoice{}, startupError(formatOpenFailure(failure))
	}

	if registry == nil {
		cfg = withoutStores(cfg)
	}
	except := config.RecentProject{Name: name, Path: dir}
	if store != nil {
		except = config.RecentFromStore(store.Name, dir)
	}
	last, ok := cfg.LastOpened(except)
	if !ok || last.Path == "" {
		return startupChoice{}, startupError(formatOpenFailure(failure) +
			"No recent project with a local checkout has opened successfully, so there is nothing to fall back to.\n")
	}
	var lastStore *brain.Store
	if last.Store != "" {
		s, found := registry.Store(last.Store)
		if !found {
			return startupChoice{}, startupError(formatOpenFailure(failure) +
				fmt.Sprintf("The last project that opened, %s, is no longer a store in the brain registry.\n", last.Name))
		}
		lastStore = &s
	}
	fallback, fallbackFailure := open(openTarget(last.Name, last.Path, lastStore))
	if fallbackFailure != nil {
		return startupChoice{}, startupError(formatOpenFailure(failure) + formatOpenFailure(fallbackFailure))
	}
	return startupChoice{Name: last.Name, Dir: last.Path, Store: lastStore, Opened: fallback, StartupFailure: failure}, nil
}

// openTarget names what to open: a brain store through its namespace, or a
// folder.
func openTarget(name, dir string, store *brain.Store) datasource.OpenTarget {
	if store != nil {
		return datasource.OpenTarget{Name: name, Dir: store.Dir(), Store: store}
	}
	return datasource.OpenTarget{Name: name, Dir: dir}
}

// withoutStores is cfg without its recent brain stores, for a run that has no
// brain registry to open them through.
func withoutStores(cfg *config.Config) *config.Config {
	trimmed := *cfg
	trimmed.RecentProjects = nil
	for _, p := range cfg.RecentProjects {
		if p.Store == "" {
			trimmed.RecentProjects = append(trimmed.RecentProjects, p)
		}
	}
	return &trimmed
}

// startupSources discovers the sources of the project that opened: for a brain
// store its scoped server, otherwise every source in the folder.
func startupSources(choice startupChoice, beadsDir string) ([]datasource.DataSource, error) {
	if choice.Store != nil {
		return datasource.StoreSources(*choice.Store)
	}
	return datasource.DiscoverSources(datasource.DiscoveryOptions{
		BeadsDir:               beadsDir,
		ValidateAfterDiscovery: false,
	})
}

// startupRecent describes the project that opened, for the recent list.
func startupRecent(choice startupChoice, name, path string) (config.RecentProject, bool) {
	if choice.Store != nil {
		return config.RecentFromStore(choice.Store.Name, path), true
	}
	return config.RecentFromCheckout(name, path)
}

func startupError(text string) error {
	return errors.New(strings.TrimRight(text, "\n"))
}

// formatOpenFailure renders a failure for stderr.
func formatOpenFailure(f *datasource.OpenFailure) string {
	project := f.Project
	if project == "" {
		project = f.Dir
	}
	var b strings.Builder
	fmt.Fprintf(&b, "b9s: cannot open %s\n", project)
	fmt.Fprintf(&b, "  Reason  %s\n", f.Message())
	if f.Err != nil {
		fmt.Fprintf(&b, "  Detail  %v\n", f.Err)
	}
	for i, step := range f.Try() {
		label := "        "
		if i == 0 {
			label = "Try     "
		}
		fmt.Fprintf(&b, "  %s%s\n", label, step)
	}
	return b.String()
}
