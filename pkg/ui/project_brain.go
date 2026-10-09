package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/vanderheijden86/b9s/internal/brain"
	"github.com/vanderheijden86/b9s/internal/datasource"
	"github.com/vanderheijden86/b9s/pkg/config"
	"github.com/vanderheijden86/b9s/pkg/model"
)

// brainMode is the state of brain mode (ADR 0052): brain's registry lists the
// stores that become projects, and activeStore names the one on screen.
type brainMode struct {
	registry    *brain.Registry
	activeStore string
	// refusal is the text of the write that bd or the store refused, shown in
	// a popup until it is dismissed: a guard hook's reason can be longer than
	// the one-line footer.
	refusal string
}

// errNoStore means a project names a brain store the registry no longer lists.
var errNoStore = errors.New("store is not in the brain registry")

// WithBrain turns on brain mode: the registry's stores become the projects
// `:project` lists, and activeStore (empty when the startup project is a plain
// checkout) is the store on screen. Call it before WithConfig.
func (m Model) WithBrain(registry *brain.Registry, activeStore string) Model {
	m.brain = brainMode{registry: registry, activeStore: activeStore}
	return m
}

// store resolves a project to its brain store.
func (b brainMode) store(project config.Project) (brain.Store, bool) {
	if project.Store == "" {
		return brain.Store{}, false
	}
	return b.registry.Store(project.Store)
}

// storeProject is the project a store is shown as.
func storeProject(store brain.Store) config.Project {
	return config.Project{Name: store.Name, Path: store.Dir(), Store: store.Name}
}

// checkoutFor is the checkout writes for project run in: the store's own for a
// brain store, the folder otherwise.
func (m Model) checkoutFor(project config.Project) (Checkout, bool) {
	if project.Store != "" {
		store, ok := m.brain.store(project)
		if !ok {
			return Checkout{}, false
		}
		return NewStoreCheckout(store)
	}
	return NewCheckout(project.ResolvedPath())
}

// activeCheckout is the checkout of the project on screen.
func (m Model) activeCheckout() Checkout {
	checkout, _ := m.checkoutFor(config.Project{Name: m.activeProjectName, Path: m.activeProjectPath, Store: m.brain.activeStore})
	return checkout
}

// brainOpenTarget opens a store project as its store, or as nothing at all when
// the registry no longer lists it.
func (m Model) brainOpenTarget(project config.Project) datasource.OpenTarget {
	store, ok := m.brain.store(project)
	if !ok {
		return datasource.OpenTarget{Name: project.Name}
	}
	return datasource.OpenTarget{Name: project.Name, Dir: store.Dir(), Store: &store}
}

// projectBrainSources lists the sources of a brain store project: its server,
// scoped to its namespace.
func (m Model) projectBrainSources(project config.Project) ([]datasource.DataSource, error) {
	store, ok := m.brain.store(project)
	if !ok {
		return nil, fmt.Errorf("%s: %w", project.Name, errNoStore)
	}
	return datasource.StoreSources(store)
}

// reloadActiveStore reads the store on screen again through the scoped source
// it opened with. It never falls back to a file export: the store shares its
// database with others, and a copy would hold their beads.
func (m Model) reloadActiveStore() ([]model.Issue, error) {
	if m.doltSource.Type != datasource.SourceTypeDolt {
		return nil, fmt.Errorf("store %s is not connected to its Dolt server", m.brain.activeStore)
	}
	return datasource.LoadFromSource(m.doltSource)
}

// openBrainProjectTable lists the registry's stores in the :project table.
func (m Model) openBrainProjectTable() Model {
	m.projectTable = NewProjectTable(m.theme)
	m.projectTable.SetSize(m.width, m.height-1)
	m.projectTable.startLoading("brain stores")
	m.projectTable.finishLoading(m.brain.registry.Names(), nil, m.projectTableSlots())
	m.showProjectTable = true
	return m
}

// projectForStore is the header project for a store when there is one, and
// otherwise the store as a new project.
func (m Model) projectForStore(name string) config.Project {
	for _, p := range m.allProjects {
		if p.Store == name {
			return p
		}
	}
	if store, ok := m.brain.registry.Store(name); ok {
		return storeProject(store)
	}
	return config.Project{Name: name, Store: name}
}

// handleWriteRefusalKeys owns every key while the refusal popup is open.
func (m Model) handleWriteRefusalKeys(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "esc", "enter", "q":
		m.brain.refusal = ""
	}
	return m
}

// renderWriteRefusalPopup shows, unchanged, what the store said when it refused
// a change. The text comes from bd and from the store's hooks, so control
// characters are removed before it reaches the terminal.
func (m Model) renderWriteRefusalPopup() string {
	th := m.theme
	boxWidth := m.width - 8
	if boxWidth > 110 {
		boxWidth = 110
	}
	if boxWidth < 30 {
		boxWidth = 30
	}
	titleStyle := th.Renderer.NewStyle().Foreground(th.Primary).Bold(true)
	dimStyle := th.Renderer.NewStyle().Foreground(th.Secondary)

	text := strings.TrimSpace(sanitizeTerminalText(m.brain.refusal))
	lines := []string{
		titleStyle.Render(fmt.Sprintf("The change to %s failed", m.activeProjectName)),
		"",
		text,
		"",
		dimStyle.Render("esc close"),
	}
	box := th.Renderer.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(th.Primary).
		Padding(1, 2).
		Width(boxWidth).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height-1, lipgloss.Center, lipgloss.Center, box)
}
