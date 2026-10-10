package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vanderheijden86/b9s/internal/brain"
)

// brainHome points HOME at an empty directory and returns a brain store whose
// folder holds a .beads directory.
func brainHome(t *testing.T, name string) (home string, store brain.Store) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake wrappers need a POSIX shell")
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	beads := filepath.Join(t.TempDir(), name, ".beads")
	if err := os.MkdirAll(beads, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, brain.Store{Name: name, Path: beads}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runWrite(t *testing.T, w *IssueWriter) BdResultMsg {
	t.Helper()
	result, ok := w.CreateIssue(map[string]string{"title": "from b9s"})().(BdResultMsg)
	if !ok {
		t.Fatal("not a BdResultMsg")
	}
	return result
}

func TestStoreWriteRunsThroughTheStoresWrapper(t *testing.T) {
	home, store := brainHome(t, "task")
	// The wrapper reports the environment it pins and the arguments it got,
	// as brain's wrapper would hand them to bd.
	writeExecutable(t, filepath.Join(home, ".local/bin/task"),
		"#!/bin/sh\nexport BD_NAME=\"task\"\nexport BEADS_DIR=\"/pinned/.beads\"\necho \"wrapper:$BD_NAME:$BEADS_DIR:$*\"\n")
	checkout, ok := NewStoreCheckout(store)
	if !ok {
		t.Fatal("no checkout for the store")
	}
	// bd is not on PATH at all: the wrapper is enough.
	w := &IssueWriter{}
	w.SetCheckout(checkout)

	result := runWrite(t, w)
	if !result.Success {
		t.Fatalf("write failed: %v", result.Error)
	}
	if want := "wrapper:task:/pinned/.beads:create --title=from b9s"; result.Output != want {
		t.Errorf("output = %q, want %q", result.Output, want)
	}
}

func TestStoreWriteShowsABrainRefusalUnchanged(t *testing.T) {
	home, store := brainHome(t, "task")
	refusal := "Error: guard hook 'no-wip' refused the write: title must not start with WIP"
	writeExecutable(t, filepath.Join(home, ".local/bin/task"),
		"#!/bin/sh\nexport BD_NAME=\"task\"\necho \""+refusal+"\" >&2\nexit 2\n")
	checkout, _ := NewStoreCheckout(store)
	w := &IssueWriter{}
	w.SetCheckout(checkout)

	result := runWrite(t, w)
	if result.Success || result.Error == nil {
		t.Fatal("a refused write must fail")
	}
	if !strings.Contains(result.Error.Error(), refusal) {
		t.Errorf("error = %q, want it to carry brain's refusal verbatim", result.Error)
	}
}

func TestStoreWriteWithoutAWrapperPinsTheStoreOnBrainsBd(t *testing.T) {
	_, store := brainHome(t, "task")
	bd := filepath.Join(t.TempDir(), "bd")
	writeExecutable(t, bd,
		"#!/bin/sh\ncase \"$1\" in --help) echo '      --wide  Wide view'; exit 0;; esac\necho \"bd:$BD_NAME:$BEADS_DIR:$*\"\n")
	checkout, _ := NewStoreCheckout(store)
	w := &IssueWriter{bdPath: bd, available: true}
	w.SetCheckout(checkout)

	result := runWrite(t, w)
	if !result.Success {
		t.Fatalf("write failed: %v", result.Error)
	}
	if want := "bd:task:" + store.Path + ":create --title=from b9s"; result.Output != want {
		t.Errorf("output = %q, want %q", result.Output, want)
	}
}

func TestStoreWriteWithoutAWrapperRefusesAnUpstreamBd(t *testing.T) {
	_, store := brainHome(t, "task")
	marker := filepath.Join(t.TempDir(), "ran")
	bd := filepath.Join(t.TempDir(), "bd")
	// An upstream bd: no --wide flag. If it were run it would leave the marker.
	writeExecutable(t, bd, "#!/bin/sh\ncase \"$1\" in --help) echo 'Usage: bd'; exit 0;; esac\ntouch "+marker+"\n")
	checkout, _ := NewStoreCheckout(store)
	w := &IssueWriter{bdPath: bd, available: true}
	w.SetCheckout(checkout)

	result := runWrite(t, w)
	if result.Success || result.Error == nil || !strings.Contains(result.Error.Error(), "not brain's bd") {
		t.Fatalf("result = %+v, want a refusal that names the problem", result)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("upstream bd ran a write for a brain store")
	}
}

func TestPlainCheckoutStillRunsPlainBd(t *testing.T) {
	checkout := testCheckout(t)
	w := &IssueWriter{bdPath: "/bin/echo", available: true}
	w.SetCheckout(checkout)
	if result := runWrite(t, w); !result.Success {
		t.Fatalf("plain write failed: %v", result.Error)
	}
	if checkout.Store() != nil {
		t.Error("a plain checkout has no store")
	}
}

func TestStoreCheckoutNeedsTheStoresBeadsDirectory(t *testing.T) {
	if _, ok := NewStoreCheckout(brain.Store{Name: "gone", Path: filepath.Join(t.TempDir(), "nope", ".beads")}); ok {
		t.Error("a store whose folder is missing has no checkout")
	}
}
