package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/vanderheijden86/b9s/internal/brain"
	"github.com/vanderheijden86/b9s/internal/datasource"
	"github.com/vanderheijden86/b9s/pkg/config"
)

// brainRegistry lists three stores, two of which share a folder.
func brainRegistry(t *testing.T) *brain.Registry {
	t.Helper()
	root := t.TempDir()
	var stores []brain.Store
	for _, s := range []struct{ name, folder string }{{"brain", "shared"}, {"decisions", "shared"}, {"task", "task"}} {
		beads := filepath.Join(root, s.folder, ".beads")
		if err := os.MkdirAll(beads, 0o755); err != nil {
			t.Fatal(err)
		}
		stores = append(stores, brain.Store{Name: s.name, Path: beads})
	}
	return &brain.Registry{Stores: stores}
}

// brainModel is a sized model in brain mode showing the task store.
func brainModel(t *testing.T) (Model, *brain.Registry) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	reg := brainRegistry(t)
	task, _ := reg.Store("task")
	cfg := config.Config{RecentProjects: []config.RecentProject{config.RecentFromStore("task", task.Dir())}}
	m := NewModel(nil, "").
		WithBrain(reg, "task").
		WithConfig(cfg, "task", task.Dir())
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return updated.(Model), reg
}

func TestBrainProjectTableListsTheRegistrysStores(t *testing.T) {
	m, reg := brainModel(t)

	updated, cmd := m.Update(OpenProjectTableMsg{})
	m = updated.(Model)

	if cmd != nil {
		t.Error("the registry is read already; the table has nothing to load")
	}
	if !m.showProjectTable || m.projectTable.State() != ProjectTableLoaded {
		t.Fatalf("table shown = %v, state = %v; want a loaded table", m.showProjectTable, m.projectTable.State())
	}
	view := m.projectTable.View()
	for _, name := range reg.Names() {
		if !strings.Contains(view, name) {
			t.Errorf("table does not list store %s:\n%s", name, view)
		}
	}
	if !strings.Contains(view, "<1>") {
		t.Errorf("the recent store must show its number key:\n%s", view)
	}
}

func TestBrainProjectTableEnterOpensTheStore(t *testing.T) {
	m, _ := brainModel(t)
	updated, _ := m.Update(OpenProjectTableMsg{})
	m = updated.(Model)
	m.projectTable.cursor = 0 // brain, the first store by name

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.showProjectTable || cmd == nil {
		t.Fatalf("enter must close the table and request the switch (shown %v)", m.showProjectTable)
	}
	msg, ok := cmd().(SwitchProjectMsg)
	if !ok || msg.Project.Store != "brain" || msg.Project.Name != "brain" {
		t.Fatalf("switch = %+v, want the brain store", msg)
	}
}

func TestStoresInOneFolderAreDifferentHeaderProjects(t *testing.T) {
	reg := brainRegistry(t)
	brainStore, _ := reg.Store("brain")
	decisions, _ := reg.Store("decisions")
	a, b := storeProject(brainStore), storeProject(decisions)
	if a.Path != b.Path {
		t.Fatalf("fixture: stores should share a folder, got %q and %q", a.Path, b.Path)
	}
	if projectKey(a) == projectKey(b) {
		t.Errorf("both stores keyed %q; counts and reachability would be shared", projectKey(a))
	}
}

func TestBrainSwitchOpensTheStoreThroughItsNamespace(t *testing.T) {
	m, reg := brainModel(t)
	var opened datasource.OpenTarget
	m.openProject = func(target datasource.OpenTarget) (datasource.OpenedProject, *datasource.OpenFailure) {
		opened = target
		return datasource.OpenedProject{}, serverDown(target.Name)
	}
	brainStore, _ := reg.Store("brain")

	m, cmd := requestSwitch(t, m, storeProject(brainStore))
	cmdBatchMessage(t, cmd)

	if opened.Store == nil || opened.Store.Name != "brain" || opened.Dir != brainStore.Dir() {
		t.Errorf("opened %+v; a store must open as that store, not as a folder", opened)
	}
}

func TestSwitchToAStoreTheRegistryLostIsRefused(t *testing.T) {
	m, _ := brainModel(t)
	m.openProject = func(target datasource.OpenTarget) (datasource.OpenedProject, *datasource.OpenFailure) {
		t.Fatalf("opened %+v; the folder of a lost store must not be read as a plain checkout", target)
		return datasource.OpenedProject{}, nil
	}
	gone := config.Project{Name: "gone", Path: t.TempDir(), Store: "gone"}

	m, cmd := requestSwitch(t, m, gone)
	msg, ok := cmdBatchMessage(t, cmd).(projectOpenFailedMsg)
	if !ok {
		t.Fatalf("no failure message from %v", cmd)
	}
	if msg.failure.Reason != datasource.OpenNoStore || !errors.Is(msg.failure, errNoStore) {
		t.Errorf("failure = %+v", msg.failure)
	}
	updated, _ := m.Update(msg)
	if m = updated.(Model); !m.showOpenFailure {
		t.Error("the popup must say why the store did not open")
	}
}

// cmdBatchMessage runs the first command of a batch that yields a
// projectOpenFailedMsg or projectOpenedMsg.
func cmdBatchMessage(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("not a batch")
	}
	for _, c := range batch {
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case projectOpenFailedMsg, projectOpenedMsg:
			return msg
		}
	}
	t.Fatal("batch produced no open result")
	return nil
}

func TestBrainAllProjectsViewReadsTheWholeRegistryWide(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	m, reg := brainModel(t)
	for _, store := range reg.Stores {
		if err := os.WriteFile(filepath.Join(store.Path, "metadata.json"),
			[]byte(`{"dolt_mode":"server","dolt_server_host":"127.0.0.1","dolt_server_port":3399,"dolt_database":"brain_unified"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")

	dbs := m.allProjectsDBs()

	if len(dbs) != 1 || dbs[0].Database != "brain_unified" || dbs[0].Brain == nil || !dbs[0].Brain.Wide {
		t.Fatalf("databases = %+v, want brain_unified once, read wide", dbs)
	}
}

func TestStoreWriteFailureOpensARefusalPopupThatEscClears(t *testing.T) {
	m, _ := brainModel(t)
	refusal := "Error: bead not created: refused by guard hook \"no-wip\" (/x/hooks.d/no-wip.toml): titles must not start with WIP"

	updated, _ := m.Update(BdResultMsg{Operation: BdOpCreate, Error: errors.New(refusal)})
	m = updated.(Model)

	if m.brain.refusal != refusal {
		t.Fatalf("refusal = %q, want brain's text unchanged", m.brain.refusal)
	}
	view := m.renderWriteRefusalPopup()
	if !strings.Contains(view, "must not start") {
		t.Errorf("popup must show the reason:\n%s", view)
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m = updated.(Model); m.brain.refusal != "" {
		t.Error("esc must dismiss the popup")
	}
}

func TestPlainProjectWriteFailureKeepsTheFooterOnly(t *testing.T) {
	m, _, _ := switchModel(t)
	updated, _ := m.Update(BdResultMsg{Operation: BdOpCreate, Error: errors.New("boom")})
	m = updated.(Model)
	if m.brain.refusal != "" || !m.statusIsError {
		t.Errorf("a project that is not a store keeps the one-line error (refusal %q)", m.brain.refusal)
	}
}

func TestReloadOfAStoreNeverFallsBackToAFile(t *testing.T) {
	m, _ := brainModel(t)
	if _, err := m.reloadActiveStore(); err == nil {
		t.Fatal("a store without a connected server must not reload from a file export")
	}
}

func TestWebBridgeOpensAStoreThroughTheRegistry(t *testing.T) {
	reg := brainRegistry(t)
	task, _ := reg.Store("task")
	project := storeProject(task)

	target := ProjectOpenTarget(project, "reader", reg)
	if target.Store == nil || target.Store.Name != "task" || target.Dir != task.Dir() {
		t.Errorf("target = %+v, want the task store", target)
	}
	if lost := ProjectOpenTarget(project, "reader", nil); lost.Store != nil || lost.Dir != "" {
		t.Errorf("target = %+v; without the registry the store's folder must not open as a checkout", lost)
	}
	if got := CheckoutFor(target); got.Store() == nil || got.Dir() != task.Dir() {
		t.Errorf("checkout = %+v, want the store's", got)
	}
	headers := HeaderProjects([]config.RecentProject{config.RecentFromStore("task", task.Dir())}, config.RecentProject{})
	if len(headers) != 1 || headers[0].Store != "task" {
		t.Errorf("header = %+v, want the store kept", headers)
	}
}

func TestBrainProjectTableShowsTheRowsAroundTheCursor(t *testing.T) {
	m, _ := brainModel(t)
	var names []string
	for i := 0; i < 60; i++ {
		names = append(names, fmt.Sprintf("store%02d", i))
	}
	m.projectTable = NewProjectTable(m.theme)
	m.projectTable.SetSize(100, 30)
	m.projectTable.windowed = true
	m.projectTable.finishLoading(names, nil, nil)
	m.projectTable.cursor = 45

	view := m.projectTable.View()

	if !strings.Contains(view, "store45") || strings.Contains(view, "store00") {
		t.Errorf("the window must follow the cursor:\n%s", view)
	}
	if !strings.Contains(view, "of 60") {
		t.Errorf("a windowed table says how much it hides:\n%s", view)
	}
}

func TestProjectTableOfADoltServerIsStillDrawnWhole(t *testing.T) {
	m, _ := brainModel(t)
	var names []string
	for i := 0; i < 60; i++ {
		names = append(names, fmt.Sprintf("db%02d", i))
	}
	table := NewProjectTable(m.theme)
	table.SetSize(100, 30)
	table.finishLoading(names, nil, nil)
	if first, last := table.window(30); first != 0 || last != 60 {
		t.Errorf("window = %d-%d; the catalog of a server has always been drawn whole", first, last)
	}
}
