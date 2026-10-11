package store

import (
	"testing"
)

func TestQueryBuilder(t *testing.T) {
	q := newQuery(`SELECT id FROM checks WHERE project_id = ?`, int64(3))
	if got := q.String(); got != `SELECT id FROM checks WHERE project_id = ?` {
		t.Errorf("base = %q", got)
	}
	if args := q.Args(); len(args) != 1 || args[0].(int64) != 3 {
		t.Fatalf("args = %v", args)
	}

	filtered := newQuery(`SELECT id FROM checks WHERE project_id = ?`, int64(3))
	filtered.in("slug", []string{"a", "b"})
	if got, want := filtered.String(),
		`SELECT id FROM checks WHERE project_id = ? AND slug IN (?,?)`; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if args := filtered.Args(); len(args) != 3 || args[1] != "a" || args[2] != "b" {
		t.Errorf("args = %v, want the column values appended in order", args)
	}

	// arg 是给 VALUES 列表用的追加器：每次补一个 ?, 占位并把值排在后面。
	values := newQuery(`INSERT INTO t(a,b) VALUES(?`, "x")
	values.arg("y")
	values.raw(")")
	if got, want := values.String(), `INSERT INTO t(a,b) VALUES(?,?)`; got != want {
		t.Errorf("VALUES build = %q, want %q", got, want)
	}
	if args := values.Args(); len(args) != 2 || args[0] != "x" || args[1] != "y" {
		t.Errorf("args = %v", args)
	}

	// 空列表不加条件，否则 SQL 会变成非法的 IN ()。
	empty := newQuery(`WHERE x = ?`, 1)
	empty.in("slug", nil)
	if empty.String() != `WHERE x = ?` {
		t.Errorf("empty IN should be a no-op, got %q", empty.String())
	}
	if len(empty.Args()) != 1 {
		t.Errorf("empty IN must not add args: %v", empty.Args())
	}
}
