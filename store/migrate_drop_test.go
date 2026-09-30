package store

import "testing"

// 020 drops scan_checkpoints on both dialects; a query against it must fail.
func TestMigration020_DropsScanCheckpoints(t *testing.T) {
	withDialects(t, func(t *testing.T, st *Store) {
		var n int
		if err := st.get(&n, `SELECT count(*) FROM scan_checkpoints`); err == nil {
			t.Errorf("scan_checkpoints still exists after migrations")
		}
	})
}
