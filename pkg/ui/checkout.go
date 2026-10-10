package ui

import (
	"os"
	"path/filepath"

	"github.com/vanderheijden86/b9s/internal/brain"
)

// Checkout is a local Beads checkout that bd commands can run in. It is only
// built by NewCheckout or NewStoreCheckout, so a write can never be sent to bd for a project that
// was opened from its database without a checkout.
type Checkout struct {
	dir string
	// store is set for a brain store: bd then runs as that store, through its
	// wrapper, instead of plain in dir (ADR 0052).
	store *brain.Store
}

// NewCheckout returns the checkout at path when path contains a .beads
// directory.
func NewCheckout(path string) (Checkout, bool) {
	beadsDir, ok := projectBeadsDir(path)
	if !ok {
		return Checkout{}, false
	}
	info, err := os.Stat(beadsDir)
	if err != nil || !info.IsDir() {
		return Checkout{}, false
	}
	return Checkout{dir: path}, true
}

// NewStoreCheckout returns the checkout of a brain store, whose folder holds
// its .beads directory and whose writes run through the store's own command.
func NewStoreCheckout(store brain.Store) (Checkout, bool) {
	checkout, ok := NewCheckout(store.Dir())
	if !ok {
		return Checkout{}, false
	}
	checkout.store = &store
	return checkout, true
}

// Store is the brain store the checkout belongs to, or nil for a plain one.
func (c Checkout) Store() *brain.Store { return c.store }

// Dir is the checkout's root directory, or "" for none.
func (c Checkout) Dir() string { return c.dir }

// projectBeadsDir is the .beads directory of the project checked out at path.
// It reports false for an empty path: filepath.Join would turn that into a
// relative ".beads", which resolves against the working directory and so reads
// the project b9s was started in. Every .beads path in this package goes through
// here, which TestProjectBeadsDirIsTheOnlyBeadsJoin enforces.
func projectBeadsDir(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	return filepath.Join(path, ".beads"), true
}
