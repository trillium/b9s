package datasource

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/vanderheijden86/b9s/internal/brain"
)

// brainStore writes a store's .beads directory with a server metadata.json and
// returns the store.
func brainStore(t *testing.T, name, metadata string) brain.Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name, ".beads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	return brain.Store{Name: name, Path: dir}
}

const serverMetadata = `{"dolt_mode":"server","dolt_server_host":"127.0.0.1","dolt_database":"brain_unified"}`

func TestStoreSourcesScopeTheServerToTheStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	store := brainStore(t, "task", serverMetadata)

	sources, err := StoreSources(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Type != SourceTypeDolt || sources[0].Database != "brain_unified" {
		t.Fatalf("sources = %+v", sources)
	}
	if got := sources[0].Brain; got == nil || got.Store != "task" || got.Wide {
		t.Errorf("Brain = %+v, want the narrow scope of task", got)
	}
}

func TestStoreSourcesDialTheServerTheWrapperPins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	store := brainStore(t, "task", serverMetadata)
	wrapper := "#!/bin/sh\nexport BD_NAME=\"task\"\nexport BEADS_DOLT_SERVER_HOST=10.1.2.3\nexport BEADS_DOLT_SERVER_PORT=3312\nexec bd \"$@\"\n"
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "task"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}

	sources, err := StoreSources(store)
	if err != nil {
		t.Fatal(err)
	}
	if got := sources[0].Path; got != "10.1.2.3:3312" {
		t.Errorf("address = %s, want the wrapper's pins", got)
	}
}

func TestStoreSourcesRefuseAStoreWithoutAServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := brainStore(t, "task", `{"dolt_mode":"embedded","dolt_database":"task"}`)
	// An export beside the store must not stand in for it: brain's stores
	// share a database, so the copy could hold other stores' beads.
	if err := os.WriteFile(filepath.Join(store.Path, "issues.jsonl"), []byte(`{"id":"other-1","title":"x"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := StoreSources(store); !errors.Is(err, ErrNoStoreServer) {
		t.Fatalf("err = %v, want ErrNoStoreServer", err)
	}
}

func TestOpenProjectOfAStoreWithoutAServerFailsInsteadOfFallingBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store := brainStore(t, "task", `{"dolt_mode":"embedded","dolt_database":"task"}`)
	if err := os.WriteFile(filepath.Join(store.Path, "issues.jsonl"), []byte(`{"id":"other-1","title":"x","status":"open"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened, failure := OpenProject(OpenTarget{Name: "task", Dir: store.Dir(), Store: &store})
	if failure == nil {
		t.Fatalf("opened %d issues from a copy that is not the store's namespace", len(opened.Issues))
	}
	if failure.Reason != OpenUnreadable {
		t.Errorf("reason = %v", failure.Reason)
	}
}

func TestBrainDBsReadsSharedDatabasesOnceAndWide(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BEADS_DOLT_SERVER_PORT", "3399")
	one := brainStore(t, "one", serverMetadata)
	two := brainStore(t, "two", serverMetadata)
	own := brainStore(t, "own", `{"dolt_mode":"server","dolt_database":"own_db"}`)
	noServer := brainStore(t, "noserver", `{"dolt_mode":"embedded"}`)
	reg := &brain.Registry{Stores: []brain.Store{one, two, own, noServer}}

	dbs := BrainDBs(reg)
	var names []string
	for _, db := range dbs {
		names = append(names, db.Database)
		if db.Brain == nil || !db.Brain.Wide {
			t.Errorf("%s: Brain = %+v, want the wide scope", db.Database, db.Brain)
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "brain_unified" || names[1] != "own_db" {
		t.Errorf("databases = %v; the shared database must appear once and a store without a server not at all", names)
	}
}

// brainFixture adds brain's unified tables to the scratch test database and a
// few beads for three namespaces.
func brainFixture(t *testing.T, dbName, addr string) {
	t.Helper()
	db, err := sql.Open("mysql", buildDSN(scratchServerUser(), os.Getenv("BEADS_DOLT_PASSWORD"), addr, dbName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		"CREATE TABLE brain_unified_config (store VARCHAR(255) NOT NULL, `key` VARCHAR(255) NOT NULL, value TEXT, PRIMARY KEY (store, `key`))",
		"CREATE TABLE brain_store_prefixes (prefix VARCHAR(255) PRIMARY KEY, store VARCHAR(255) NOT NULL)",
		"INSERT INTO brain_store_prefixes (prefix, store) VALUES ('task', 'task'), ('proto', 'task'), ('brain', 'brain'), ('nightshift', '__unattributed__')",
		"INSERT INTO issues (id, title) VALUES ('task-1', 'task one'), ('proto-1', 'proto one'), ('brain-1', 'brain one'), ('nightshift-1', 'unclaimed'), ('taskforce-1', 'not task')",
		"INSERT INTO labels (issue_id, label) VALUES ('task-1', 'mine'), ('brain-1', 'theirs')",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func idsOf(t *testing.T, scope brain.Scope, dbName, addr string) []string {
	t.Helper()
	reader, err := NewDoltReader(DataSource{Type: SourceTypeDolt, Path: addr, Database: dbName, User: scratchServerUser(), Brain: &scope})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	issues, err := reader.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestDoltIntegration_NarrowReadIsTheStoresNamespace(t *testing.T) {
	dbName, addr, cleanup := testDB(t)
	defer cleanup()
	brainFixture(t, dbName, addr)

	got := idsOf(t, brain.Scope{Store: "task"}, dbName, addr)
	want := []string{"proto-1", "task-1"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("task reads %v, want %v (not brain-1, nightshift-1 or taskforce-1)", got, want)
	}
	if got := idsOf(t, brain.Scope{Store: "brain"}, dbName, addr); len(got) != 1 || got[0] != "brain-1" {
		t.Errorf("brain reads %v", got)
	}
}

func TestDoltIntegration_NarrowReadKeepsOnlyTheStoresLabels(t *testing.T) {
	dbName, addr, cleanup := testDB(t)
	defer cleanup()
	brainFixture(t, dbName, addr)

	reader, err := NewDoltReader(DataSource{Type: SourceTypeDolt, Path: addr, Database: dbName, User: scratchServerUser(), Brain: &brain.Scope{Store: "task"}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	issues, err := reader.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if issue.ID == "task-1" && (len(issue.Labels) != 1 || issue.Labels[0] != "mine") {
			t.Errorf("task-1 labels = %v", issue.Labels)
		}
	}
	if n, err := reader.CountIssues(); err != nil || n != 2 {
		t.Errorf("CountIssues = %d, %v; the count must be the namespace's", n, err)
	}
}

func TestDoltIntegration_WideReadNamesTheOwnerOfEachBead(t *testing.T) {
	dbName, addr, cleanup := testDB(t)
	defer cleanup()
	brainFixture(t, dbName, addr)

	reader, err := NewDoltReader(DataSource{Type: SourceTypeDolt, Path: addr, Database: dbName, User: scratchServerUser(), Brain: &brain.Scope{Wide: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	issues, err := reader.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	owner := map[string]string{}
	for _, issue := range issues {
		owner[issue.ID] = issue.SourceRepo
	}
	for id, want := range map[string]string{
		"task-1":       "task",
		"proto-1":      "task",
		"brain-1":      "brain",
		"nightshift-1": "nightshift", // claimed by nobody: its id prefix
		"taskforce-1":  "taskforce",
	} {
		if got, ok := owner[id]; !ok || got != want {
			t.Errorf("%s: project %q (present %v), want %q", id, got, ok, want)
		}
	}
}

func TestDoltIntegration_AUnscopedReaderStillReadsTheWholeDatabase(t *testing.T) {
	dbName, addr, cleanup := testDB(t)
	defer cleanup()
	brainFixture(t, dbName, addr)

	reader := newTestDoltReader(t, dbName, addr)
	defer reader.Close()
	issues, err := reader.LoadIssues()
	if err != nil {
		t.Fatal(err)
	}
	// test-001, test-002, test-003, test-005 from the seed, plus the five above.
	if len(issues) != 9 {
		t.Errorf("an ordinary project reads %d issues, want 9", len(issues))
	}
}

// TestDoltIntegration_BrainReadMatchesBdsNarrowList checks the scoped read
// against brain's own: every store b9s reads returns the beads the store's bd
// lists, and nothing else but gates, which bd list hides by default. It runs
// against a scratch copy of brain_unified named by B9S_TEST_BRAIN_HOME, a HOME
// holding a brain registry and the stores' wrappers, and skips without it.
func TestDoltIntegration_BrainReadMatchesBdsNarrowList(t *testing.T) {
	home := os.Getenv("B9S_TEST_BRAIN_HOME")
	if home == "" {
		t.Skip("B9S_TEST_BRAIN_HOME is not set: point it at a scratch HOME with a brain registry")
	}
	t.Setenv("HOME", home)
	t.Setenv(brain.EnvRegistry, "")
	registry, err := brain.Open(brain.On)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range registry.Stores {
		store := store
		t.Run(store.Name, func(t *testing.T) {
			runner := store.Runner()
			if runner.Wrapper == "" {
				t.Skipf("store %s has no wrapper in %s/.local/bin", store.Name, home)
			}
			bdIDs := map[string]bool{}
			for _, args := range [][]string{
				{"list", "--all", "--limit", "0", "--json"},
				{"list", "--all", "--limit", "0", "--json", "--type", "gate"},
			} {
				out, err := exec.Command(runner.Wrapper, args...).Output()
				if err != nil {
					t.Fatalf("%s %v: %v", runner.Wrapper, args, err)
				}
				var rows []struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(out, &rows); err != nil {
					t.Fatalf("%v: %v", args, err)
				}
				for _, row := range rows {
					bdIDs[row.ID] = true
				}
			}

			start := time.Now()
			issues, err := LoadIssuesFromStore(store)
			if err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(start).Round(time.Millisecond)
			read := map[string]bool{}
			for _, issue := range issues {
				read[issue.ID] = true
				if !bdIDs[issue.ID] {
					t.Errorf("b9s read %s, which bd does not list for %s", issue.ID, store.Name)
				}
			}
			for id := range bdIDs {
				if !read[id] {
					t.Errorf("bd lists %s for %s and b9s did not read it", id, store.Name)
				}
			}
			t.Logf("%s: %d beads read in %v, %d listed by bd", store.Name, len(read), elapsed, len(bdIDs))
		})
	}
}
