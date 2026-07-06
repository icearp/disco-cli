package store

import (
	"strings"
	"testing"
)

func TestSplitStatements_DollarQuotedFunction(t *testing.T) {
	script := `
CREATE OR REPLACE FUNCTION foo() RETURNS TRIGGER AS $fn$
BEGIN
    IF NEW.status IN ('a', 'b') THEN
        PERFORM pg_notify('chan', 'msg');
    END IF;
    RETURN NEW;
END;
$fn$ LANGUAGE plpgsql;

CREATE TRIGGER bar AFTER INSERT ON t FOR EACH ROW EXECUTE FUNCTION foo();
`
	stmts := splitStatements(script)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d:\n---\n%s\n---", len(stmts), strings.Join(stmts, "\n=====\n"))
	}
	if !strings.Contains(stmts[0], "LANGUAGE plpgsql") {
		t.Errorf("first stmt missing LANGUAGE plpgsql: %s", stmts[0])
	}
	if !strings.Contains(stmts[0], "RETURN NEW") {
		t.Errorf("function body got truncated at internal `;`: %s", stmts[0])
	}
	if !strings.HasPrefix(stmts[1], "CREATE TRIGGER bar") {
		t.Errorf("second stmt = %q", stmts[1])
	}
}

func TestSplitStatements_AnonymousDollarQuote(t *testing.T) {
	stmts := splitStatements(`SELECT 1; DO $$ BEGIN PERFORM 1; PERFORM 2; END $$; SELECT 2;`)
	if len(stmts) != 3 {
		t.Fatalf("expected 3 statements, got %d: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[1], "PERFORM 2") {
		t.Errorf("middle stmt truncated: %q", stmts[1])
	}
}

func TestSplitStatements_LineCommentSemicolon(t *testing.T) {
	stmts := splitStatements(`CREATE TABLE t (id INT); -- one ; two
CREATE TABLE u (id INT);`)
	if len(stmts) != 2 {
		t.Fatalf("comment ; broke split: got %d stmts: %v", len(stmts), stmts)
	}
}

func TestSplitStatements_PreservesOriginalBehavior(t *testing.T) {
	// Simple cases must still split on plain ;.
	stmts := splitStatements(`SELECT 1; SELECT 2; SELECT 3;`)
	if len(stmts) != 3 {
		t.Errorf("got %d stmts: %v", len(stmts), stmts)
	}
}
