package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vanderheijden86/b9s/internal/brain"
	"github.com/vanderheijden86/b9s/internal/datasource"
	"github.com/vanderheijden86/b9s/pkg/config"
	"github.com/vanderheijden86/b9s/pkg/ui"
)

// storeServer serves a project the Store was handed as a brain store, without
// a Dolt server: the tests here are about what a write and the project sheet
// do with a store, not about reading one (internal/datasource tests that).
func storeServer(t *testing.T, wrapperBody string) (*testServer, brain.Store) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	beads := filepath.Join(t.TempDir(), "task", ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	store := brain.Store{Name: "task", Path: beads}
	if err := os.MkdirAll(filepath.Join(home, ".local", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".local", "bin", "task"), []byte(wrapperBody), 0o755); err != nil {
		t.Fatal(err)
	}

	s := NewStore(time.Hour)
	t.Cleanup(s.Close)
	target := datasource.OpenTarget{Name: "task", Dir: store.Dir(), Store: &store}
	s.mu.Lock()
	s.target = target
	s.info = ProjectInfo{Name: "task", Key: "brain:task"}
	s.mu.Unlock()

	auth := testAuth(t)
	srv, err := NewServer(Options{
		Store:  s,
		Writer: &ui.IssueWriter{},
		Auth:   auth,
		Assets: testAssets,
		Brain:  &brain.Registry{Stores: []brain.Store{store}},
		Projects: func() []config.Project {
			return []config.Project{ui.StoreProject(store), {Name: "gone", Path: t.TempDir(), Store: "gone"}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testServer{Server: ts, store: s, auth: auth}, store
}

func TestStoreWriteRunsThroughTheStoresWrapper(t *testing.T) {
	ts, _ := storeServer(t, "#!/bin/sh\nexport BD_NAME=\"task\"\necho \"wrapper:$BD_NAME:$*\"\n")
	c := ts.pairedClient(t)

	status, res := postWrite(t, ts, c, `{"op":"status","ids":["task-1"],"status":"in_progress"}`)

	if status != http.StatusOK || !res.OK {
		t.Fatalf("status %d, result %+v", status, res)
	}
	if want := "wrapper:task:update task-1 --status=in_progress"; res.Output != want {
		t.Errorf("output = %q, want %q: the write must run the store's wrapper", res.Output, want)
	}
}

func TestStoreWriteShowsBrainsRefusalUnchanged(t *testing.T) {
	ts, _ := storeServer(t, "#!/bin/sh\nexport BD_NAME=\"task\"\necho 'Error: refused by guard hook \"no-wip\"' >&2\nexit 2\n")
	c := ts.pairedClient(t)

	status, res := postWrite(t, ts, c, `{"op":"status","ids":["task-1"],"status":"closed"}`)

	if status != http.StatusUnprocessableEntity || res.OK {
		t.Fatalf("status %d, result %+v; a refused write must fail", status, res)
	}
	if !strings.Contains(res.Error, `refused by guard hook "no-wip"`) {
		t.Errorf("error = %q", res.Error)
	}
}

func TestProjectSheetOpensNoStoreTheRegistryLost(t *testing.T) {
	ts, _ := storeServer(t, "#!/bin/sh\n")
	c := ts.pairedClient(t)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/projects/open", strings.NewReader(`{"key":"brain:gone"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(csrfHeader, ts.auth.CSRF())
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d; a store the registry lost must not open as a plain checkout", resp.StatusCode)
	}
}
