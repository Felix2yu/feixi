package store

import "strings"

// query is a tiny SQL builder: enough to assemble optional filters without
// concatenating values into the statement.
type query struct {
	sb   strings.Builder
	args []any
}

func newQuery(base string, args ...any) *query {
	q := &query{}
	q.sb.WriteString(base)
	q.args = append(q.args, args...)
	return q
}

func (q *query) raw(sql string) { q.sb.WriteString(sql) }

func (q *query) arg(v any) {
	q.raw(",?")
	q.args = append(q.args, v)
}

// in appends `AND column IN (?,?,…)`. An empty value list matches everything.
func (q *query) in(column string, values []string) {
	if len(values) == 0 {
		return
	}
	q.raw(" AND " + column + " IN (")
	for i, v := range values {
		if i > 0 {
			q.raw(",")
		}
		q.raw("?")
		q.args = append(q.args, v)
	}
	q.raw(")")
}

func (q *query) String() string { return q.sb.String() }
func (q *query) Args() []any    { return q.args }
