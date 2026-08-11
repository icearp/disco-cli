package store

import (
	"context"
	"strings"
	"testing"
)

// TestApplyOnePG_ExecutesBodiesTheSplitterWouldMangle pins the reason the
// Postgres runner stopped splitting on `;`.
//
// Exactly ONE subtest here discriminates: a `;` inside a single-quoted literal,
// which splitStatements does not track, so it split mid-string and failed 42601
// (measured against the split path; it shipped once in real COMMENT ON text).
// The other three PASS against the split path too and are regression guards for
// the new one, not evidence for the change — say so rather than letting a
// four-case table imply four kills. The `--`-in-literal case is the interesting
// near-miss: the splitter keeps the bytes and only fails to split, so the two
// statements merge into a chunk Postgres runs correctly either way.
func TestApplyOnePG_ExecutesBodiesTheSplitterWouldMangle(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	t.Cleanup(purge)
	ctx := context.Background()
	st, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	cases := []struct {
		name string
		body string
		// comment is what the table's COMMENT must read back as, or "" when the
		// case asserts only that the body applied.
		table   string
		comment string
	}{
		{
			name: "semicolon inside a string literal",
			body: `CREATE TABLE split_a (id INT);
			       COMMENT ON TABLE split_a IS 'one; two';`,
			table:   "split_a",
			comment: "one; two",
		},
		{
			name: "double dash inside a string literal",
			body: `CREATE TABLE split_b (id INT);
			       COMMENT ON TABLE split_b IS 'a -- not a comment';
			       CREATE TABLE split_b_sibling (id INT);`,
			table:   "split_b",
			comment: "a -- not a comment",
		},
		{
			name: "dollar quoted body carrying semicolons",
			body: `CREATE TABLE split_c (id INT);
			       DO $$ BEGIN PERFORM 1; PERFORM 2; END $$;`,
			table: "split_c",
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := st.db.BeginTxx(ctx, nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()

			if err := applyOnePG(ctx, tx, 900+i, tc.name, []byte(tc.body)); err != nil {
				t.Fatalf("applyOnePG(%s) = %v; want nil", tc.name, err)
			}
			// The table existing proves the body ran; the comment proves the
			// literal survived intact rather than being truncated at the `;`.
			var got string
			if err := tx.GetContext(ctx, &got,
				`SELECT coalesce(obj_description($1::regclass, 'pg_class'), '')`, tc.table); err != nil {
				t.Fatalf("read comment on %s: %v", tc.table, err)
			}
			if got != tc.comment {
				t.Errorf("comment on %s = %q; want %q", tc.table, got, tc.comment)
			}
		})
	}

	// The gutted placeholder migration (pg/016) carries only comments. Under the
	// split path it produced no statements at all; under the whole-file path the
	// comments are sent to the server, so "no error" is a claim worth pinning.
	t.Run("comment only body is a no-op", func(t *testing.T) {
		tx, err := st.db.BeginTxx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		if err := applyOnePG(ctx, tx, 999, "016_placeholder.sql",
			[]byte("-- superseded, intentionally empty\n-- see the tag notes\n")); err != nil {
			t.Errorf("applyOnePG(comment-only) = %v; want nil", err)
		}
	})
}

// TestApplyOnePG_ReportsTheFileOnFailure pins the error context. The split path
// quoted the first 60 characters of the offending statement, which named no
// file; a whole-file exec has no statement to quote, so the file name has to be
// in the message or a failed migration is unattributable.
func TestApplyOnePG_ReportsTheFileOnFailure(t *testing.T) {
	dsn, purge := pgTestEnv(t)
	t.Cleanup(purge)
	ctx := context.Background()
	st, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tx, err := st.db.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = applyOnePG(ctx, tx, 42, "042_broken.sql", []byte("SELECT this_is_not_valid_sql FROM;"))
	if err == nil {
		t.Fatal("applyOnePG(invalid body) = nil; want an error")
	}
	// Assert the whole prefix, not the pieces. "42" alone is satisfied by the
	// filename and by the SQLSTATE (42601) the failure itself carries, so it
	// would pass with the version dropped from the message entirely.
	if want := "apply migration 42 (042_broken.sql)"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q; want it to contain %q", err, want)
	}
	// The pgx error must survive the wrap — SQLSTATE is what identifies the
	// failure once the offending statement is no longer quoted.
	if !strings.Contains(err.Error(), "SQLSTATE") {
		t.Errorf("error = %q; want the pgx SQLSTATE preserved", err)
	}
}
