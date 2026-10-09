package datasource

import (
	"errors"
	"fmt"
	"sort"

	"github.com/vanderheijden86/b9s/internal/brain"
	"github.com/vanderheijden86/b9s/pkg/model"
)

// ErrNoStoreServer means a brain store names no Dolt server to read.
var ErrNoStoreServer = errors.New("no Dolt server")

// StoreSources finds the source a brain store is read from: the Dolt server
// its metadata names, found with the host and port its wrapper pins, and
// limited to the store's own namespace (ADR 0052).
//
// Only the server qualifies. Brain's stores share one database, so a JSONL or
// SQLite copy beside the store would hold, or stand in for, beads of other
// stores, and falling back to one would show them as this store's.
func StoreSources(store brain.Store) ([]DataSource, error) {
	sources, err := discoverDoltSources(store.Path, DiscoveryOptions{Env: store.Getenv()})
	if err != nil {
		return nil, err
	}
	var scoped []DataSource
	for _, source := range sources {
		if source.Type != SourceTypeDolt {
			continue
		}
		source.Brain = &brain.Scope{Store: store.Name}
		scoped = append(scoped, source)
	}
	if len(scoped) == 0 {
		return nil, fmt.Errorf("store %s: %w named in %s/metadata.json; b9s reads brain stores through their server", store.Name, ErrNoStoreServer, store.Path)
	}
	return scoped, nil
}

// LoadIssuesFromStore reads a brain store's own namespace.
func LoadIssuesFromStore(store brain.Store) ([]model.Issue, error) {
	sources, err := StoreSources(store)
	if err != nil {
		return nil, err
	}
	return LoadFromSource(sources[0])
}

// BrainDBs lists the databases the all-projects view reads for a brain
// registry. Stores that share a database are read once, wide, so every
// namespace appears and each issue is named for the store that owns it; a
// store still on a database of its own is read whole, and the wide scope
// leaves such a database unrestricted.
func BrainDBs(reg *brain.Registry) []DoltDBInfo {
	if reg == nil {
		return nil
	}
	seen := make(map[string]bool)
	var dbs []DoltDBInfo
	for _, store := range reg.Stores {
		sources, err := StoreSources(store)
		if err != nil {
			continue
		}
		source := sources[0]
		key := source.Path + "/" + source.Database
		if seen[key] {
			continue
		}
		seen[key] = true
		dbs = append(dbs, DoltDBInfo{
			Name:     store.Name,
			Host:     source.Path,
			User:     source.User,
			Database: source.Database,
			Brain:    &brain.Scope{Wide: true},
		})
	}
	sort.Slice(dbs, func(i, j int) bool { return dbs[i].Database < dbs[j].Database })
	return dbs
}
