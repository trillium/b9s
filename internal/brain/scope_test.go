package brain

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// fakeDB answers the queries Resolve sends from a table of canned results, so
// the scoping rules are tested without a Dolt server.
type fakeDB struct {
	// answers maps a substring of the query to its result.
	answers map[string]fakeResult
}

type fakeResult struct {
	cols []string
	rows [][]driver.Value
	err  error
}

var (
	fakeMu  sync.Mutex
	fakeSeq int
)

func openFake(t *testing.T, f fakeDB) *sql.DB {
	t.Helper()
	fakeMu.Lock()
	fakeSeq++
	name := fmt.Sprintf("brainfake%d", fakeSeq)
	fakeMu.Unlock()
	sql.Register(name, fakeDriver{f})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type fakeDriver struct{ db fakeDB }

func (d fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{d.db}, nil }

type fakeConn struct{ db fakeDB }

func (c fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (c fakeConn) Close() error                        { return nil }
func (c fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("unsupported") }

func (c fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	for needle, res := range c.db.answers {
		if strings.Contains(query, needle) {
			if res.err != nil {
				return nil, res.err
			}
			return &fakeRows{res: res}, nil
		}
	}
	return nil, fmt.Errorf("fake: no answer for %q", query)
}

type fakeRows struct {
	res fakeResult
	i   int
}

func (r *fakeRows) Columns() []string { return r.res.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.i >= len(r.res.rows) {
		return io.EOF
	}
	copy(dest, r.res.rows[r.i])
	r.i++
	return nil
}

func unifiedAnswers() map[string]fakeResult {
	return map[string]fakeResult{
		"brain_unified_config": {cols: []string{"n"}, rows: [][]driver.Value{{int64(1)}}},
	}
}

func prefixRows(pairs ...string) fakeResult {
	res := fakeResult{cols: []string{"store", "prefix"}}
	for i := 0; i < len(pairs); i += 2 {
		res.rows = append(res.rows, []driver.Value{pairs[i], pairs[i+1]})
	}
	return res
}

func TestResolveNarrowTakesThePrefixesRecordedForTheStore(t *testing.T) {
	answers := unifiedAnswers()
	answers["brain_store_aliases"] = fakeResult{cols: []string{"store"}}
	answers["FROM brain_store_prefixes WHERE"] = fakeResult{cols: []string{"prefix"}, rows: [][]driver.Value{{"proto"}, {"task"}}}
	ns, err := Resolve(context.Background(), openFake(t, fakeDB{answers}), Scope{Store: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if !ns.Unified || !reflect.DeepEqual(ns.Prefixes, []string{"proto", "task"}) {
		t.Fatalf("namespace = %+v", ns)
	}
	clause, args := ns.Clause()
	if clause != " AND (id LIKE ? OR id LIKE ?)" || !reflect.DeepEqual(args, []any{"proto-%", "task-%"}) {
		t.Errorf("clause = %q %v; brain scopes with id LIKE '<prefix>-%%', OR'd", clause, args)
	}
}

func TestResolveNarrowFollowsAnAlias(t *testing.T) {
	answers := unifiedAnswers()
	answers["brain_store_aliases"] = fakeResult{cols: []string{"store"}, rows: [][]driver.Value{{"decisions"}}}
	answers["FROM brain_store_prefixes WHERE"] = fakeResult{cols: []string{"prefix"}, rows: [][]driver.Value{{"decide"}, {"decision"}}}
	ns, err := Resolve(context.Background(), openFake(t, fakeDB{answers}), Scope{Store: "decide"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ns.Prefixes, []string{"decide", "decision"}) {
		t.Errorf("prefixes = %v", ns.Prefixes)
	}
}

func TestResolveNarrowFallsBackToTheStoreNameWhenNothingIsRecorded(t *testing.T) {
	answers := unifiedAnswers()
	answers["brain_store_aliases"] = fakeResult{cols: []string{"store"}}
	answers["FROM brain_store_prefixes WHERE"] = fakeResult{cols: []string{"prefix"}}
	ns, err := Resolve(context.Background(), openFake(t, fakeDB{answers}), Scope{Store: "boop"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ns.Prefixes, []string{"boop"}) {
		t.Errorf("prefixes = %v", ns.Prefixes)
	}
}

func TestResolveToleratesMissingPrefixAndAliasTables(t *testing.T) {
	answers := unifiedAnswers()
	missing := &mysql.MySQLError{Number: 1105, Message: "table not found: brain_store_prefixes"}
	answers["brain_store_aliases"] = fakeResult{err: missing}
	answers["FROM brain_store_prefixes WHERE"] = fakeResult{err: missing}
	ns, err := Resolve(context.Background(), openFake(t, fakeDB{answers}), Scope{Store: "task"})
	if err != nil || !reflect.DeepEqual(ns.Prefixes, []string{"task"}) {
		t.Errorf("namespace = %+v, err = %v", ns, err)
	}
}

func TestResolveFailsClosedWhenThePrefixesCannotBeRead(t *testing.T) {
	answers := unifiedAnswers()
	answers["brain_store_aliases"] = fakeResult{cols: []string{"store"}}
	answers["FROM brain_store_prefixes WHERE"] = fakeResult{err: errors.New("connection reset")}
	if _, err := Resolve(context.Background(), openFake(t, fakeDB{answers}), Scope{Store: "task"}); err == nil {
		t.Fatal("an error reading the prefixes must stop the read; guessing would show another store's beads")
	}
}

func TestResolveLegacyDatabaseIsNotScoped(t *testing.T) {
	answers := map[string]fakeResult{
		"brain_unified_config": {cols: []string{"n"}, rows: [][]driver.Value{{int64(0)}}},
	}
	ns, err := Resolve(context.Background(), openFake(t, fakeDB{answers}), Scope{Store: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if clause, args := ns.Clause(); ns.Unified || clause != "" || args != nil {
		t.Errorf("a one-store database is read whole, got %+v %q", ns, clause)
	}
}

func TestResolveWideReadsEveryNamespaceAndNamesOwners(t *testing.T) {
	answers := unifiedAnswers()
	answers["FROM brain_store_prefixes"] = prefixRows(
		"nightshift-tasks", "nightshift-tasks",
		"__unattributed__", "nightshift",
		"task", "task",
		"task", "proto",
	)
	ns, err := Resolve(context.Background(), openFake(t, fakeDB{answers}), Scope{Wide: true})
	if err != nil {
		t.Fatal(err)
	}
	if clause, _ := ns.Clause(); clause != "" {
		t.Errorf("a wide read has no restriction, got %q", clause)
	}
	for id, want := range map[string]string{
		"task-12":            "task",
		"proto-3":            "task",
		"nightshift-tasks-9": "nightshift-tasks", // not the shorter, unattributed "nightshift"
		"nightshift-77":      "",                 // claimed by nobody
		"mystery-1":          "",
		"taskforce-1":        "",
	} {
		if got := ns.Owner(id); got != want {
			t.Errorf("Owner(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestResolveNarrowWithoutAStoreIsAnError(t *testing.T) {
	if _, err := Resolve(context.Background(), openFake(t, fakeDB{unifiedAnswers()}), Scope{}); err == nil {
		t.Fatal("a narrow read of no store must not fall through to everything")
	}
}
