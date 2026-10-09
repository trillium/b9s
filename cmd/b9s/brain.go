package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vanderheijden86/b9s/internal/brain"
)

// brainFlags are the command-line switches of brain mode (ADR 0052).
type brainFlags struct {
	on    bool   // --brain: require a brain registry
	off   bool   // --no-brain: ignore it
	store string // --store: open this store at startup
}

// brainStartup is what brain mode decided at startup.
type brainStartup struct {
	Registry *brain.Registry // nil when b9s runs as it does without brain
	Store    *brain.Store    // the store to open, nil for the startup folder
}

// resolveBrain decides whether b9s runs in brain mode and which store it opens.
//
// Brain mode is automatic when a registry exists, so a machine with brain
// shows its stores without a flag, and a machine without one behaves exactly
// as upstream b9s. --brain insists on a registry, --no-brain and B9S_BRAIN=off
// ignore it, and --store names the store to open (and implies --brain). A
// registry that exists but cannot be read is a warning in automatic mode,
// because it must not stop an ordinary project from opening.
//
// startupDir is the folder b9s was started in, and bdName the BD_NAME of its
// environment; together they tell whether that folder is a store.
func resolveBrain(flags brainFlags, startupDir, bdName string, warn io.Writer) (brainStartup, error) {
	mode, err := brain.ParseMode(os.Getenv(brain.EnvMode))
	if err != nil {
		return brainStartup{}, err
	}
	switch {
	case flags.on && flags.off:
		return brainStartup{}, fmt.Errorf("--brain and --no-brain are mutually exclusive")
	case flags.off:
		mode = brain.Off
	case flags.on || flags.store != "":
		mode = brain.On
	}
	if flags.off && flags.store != "" {
		return brainStartup{}, fmt.Errorf("--store needs brain mode, which --no-brain turns off")
	}

	registry, err := brain.Open(mode)
	if err != nil {
		if mode == brain.Auto {
			fmt.Fprintf(warn, "b9s: ignoring the brain registry: %v\n", err)
			return brainStartup{}, nil
		}
		return brainStartup{}, err
	}
	if registry == nil {
		return brainStartup{}, nil
	}

	if flags.store != "" {
		store, ok := registry.Store(flags.store)
		if !ok {
			return brainStartup{}, fmt.Errorf("%q is not a store in %s; the stores are: %s", flags.store, registry.File, strings.Join(registry.Names(), ", "))
		}
		if store.Dir() == "" {
			return brainStartup{}, fmt.Errorf("store %s: %s is not a .beads directory", store.Name, store.Path)
		}
		return brainStartup{Registry: registry, Store: &store}, nil
	}
	if store, ok := registry.ForDir(startupDir, bdName); ok {
		return brainStartup{Registry: registry, Store: &store}, nil
	}
	return brainStartup{Registry: registry}, nil
}
