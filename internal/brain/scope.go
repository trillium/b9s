package brain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// Scope selects which beads of a brain database a read returns.
//
// Brain keeps every store in one database, brain_unified, and a store reads
// only its own namespace unless asked for --wide. Scope is the same choice:
// Store alone is brain's narrow read, Wide is its wide read.
type Scope struct {
	Store string
	Wide  bool
}

// unattributedStore is the name brain gives prefixes that no store claimed.
const unattributedStore = "__unattributed__"

// Querier is the part of *sql.DB that Resolve needs.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Namespace is a Scope resolved against the connected database.
type Namespace struct {
	// Unified is false for a database that is not brain's unified shape. Its
	// beads all belong to the one store that owns it, so nothing is scoped.
	Unified bool
	// Wide carries the scope's choice: no restriction.
	Wide bool
	// Prefixes are the id prefixes the store owns (narrow reads).
	Prefixes []string

	owners []prefixOwner // wide reads: who owns each prefix, longest first
}

type prefixOwner struct {
	prefix string
	store  string
}

// Resolve reads the namespace from the database the way brain's narrow read
// does (internal/storage/dolt/unified_namespace.go in brain): the store's
// prefixes are the rows of brain_store_prefixes recorded for the store, after
// brain_store_aliases maps the wrapper's name to the store the database
// records it under, and the store's own name stands in when the table has no
// row. A database without brain_unified_config is a legacy one-store database
// and is read whole.
//
// A failure to find out is an error, never an unscoped read: showing another
// store's beads as this store's is worse than showing none.
func Resolve(ctx context.Context, q Querier, scope Scope) (Namespace, error) {
	var unified int
	err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'brain_unified_config'",
	).Scan(&unified)
	if err != nil {
		return Namespace{}, fmt.Errorf("checking for a brain database: %w", err)
	}
	ns := Namespace{Unified: unified > 0, Wide: scope.Wide}
	if !ns.Unified {
		return ns, nil
	}
	if scope.Wide {
		ns.owners, err = loadOwners(ctx, q)
		return ns, err
	}
	if scope.Store == "" {
		return Namespace{}, errors.New("a narrow brain read needs a store name")
	}

	store := scope.Store
	var canonical string
	err = q.QueryRowContext(ctx, "SELECT `store` FROM brain_store_aliases WHERE `alias` = ?", store).Scan(&canonical)
	switch {
	case err == nil && strings.TrimSpace(canonical) != "":
		store = canonical
	case err == nil, errors.Is(err, sql.ErrNoRows), isMissingTable(err):
	default:
		return Namespace{}, fmt.Errorf("resolving store %s: %w", scope.Store, err)
	}

	rows, err := q.QueryContext(ctx, "SELECT prefix FROM brain_store_prefixes WHERE `store` = ?", store)
	switch {
	case err == nil:
		defer rows.Close()
		for rows.Next() {
			var prefix string
			if err := rows.Scan(&prefix); err != nil {
				return Namespace{}, fmt.Errorf("reading prefixes of %s: %w", store, err)
			}
			ns.Prefixes = append(ns.Prefixes, prefix)
		}
		if err := rows.Err(); err != nil {
			return Namespace{}, fmt.Errorf("reading prefixes of %s: %w", store, err)
		}
	case isMissingTable(err):
	default:
		return Namespace{}, fmt.Errorf("reading prefixes of %s: %w", store, err)
	}
	if len(ns.Prefixes) == 0 {
		ns.Prefixes = []string{store}
	}
	return ns, nil
}

func loadOwners(ctx context.Context, q Querier) ([]prefixOwner, error) {
	rows, err := q.QueryContext(ctx, "SELECT `store`, prefix FROM brain_store_prefixes")
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading store prefixes: %w", err)
	}
	defer rows.Close()
	var owners []prefixOwner
	for rows.Next() {
		var o prefixOwner
		if err := rows.Scan(&o.store, &o.prefix); err != nil {
			return nil, fmt.Errorf("reading store prefixes: %w", err)
		}
		owners = append(owners, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading store prefixes: %w", err)
	}
	// Longest prefix first, so "nightshift-tasks" is tried before "nightshift".
	sort.SliceStable(owners, func(i, j int) bool { return len(owners[i].prefix) > len(owners[j].prefix) })
	return owners, nil
}

// Clause is the SQL predicate that restricts a query on the issues table to
// the namespace, with its arguments. It is empty for a wide or legacy read.
// Each prefix contributes id LIKE '<prefix>-%', as brain's does, so the two
// readings of one store agree row for row.
func (n Namespace) Clause() (string, []any) {
	if !n.Unified || n.Wide || len(n.Prefixes) == 0 {
		return "", nil
	}
	ors := make([]string, len(n.Prefixes))
	args := make([]any, len(n.Prefixes))
	for i, prefix := range n.Prefixes {
		ors[i] = "id LIKE ?"
		args[i] = strings.TrimSuffix(prefix, "-") + "-%"
	}
	return " AND (" + strings.Join(ors, " OR ") + ")", args
}

// Owner names the store a bead belongs to in a wide read, or "" for a bead
// whose prefix no store claimed.
func (n Namespace) Owner(id string) string {
	for _, o := range n.owners {
		if strings.HasPrefix(id, o.prefix+"-") {
			if o.store == unattributedStore {
				return ""
			}
			return o.store
		}
	}
	return ""
}

const mysqlErrNoSuchTable = 1146

// isMissingTable reports whether err says a table does not exist. Dolt words
// it "table not found" under its generic error code, so the number alone does
// not tell.
func isMissingTable(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		if mysqlErr.Number == mysqlErrNoSuchTable {
			return true
		}
		msg := strings.ToLower(mysqlErr.Message)
		return strings.Contains(msg, "table not found") || strings.Contains(msg, "doesn't exist")
	}
	return false
}
