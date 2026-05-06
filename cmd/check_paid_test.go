//go:build paid

package cmd

import (
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// TestCheckCmd_Persist round-trips one --persist invocation: seed an
// unencrypted EBS volume, run aws-waf, assert a check_run + finding row
// land in the DB.
func TestCheckCmd_Persist(t *testing.T) {
	st := seedCheckDB(t)
	resetCheckFlags()
	checkPersist = true
	t.Cleanup(func() { checkPersist = false })

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--packs", "aws-waf", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("check --persist: %v", err)
	}

	runs, err := st.ListCheckRuns()
	if err != nil {
		t.Fatalf("ListCheckRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 check_run, got %d", len(runs))
	}
	rows, err := st.ListFindings(store.FindingFilter{CheckRunID: runs[0].ID})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.FindingID == "waf-sec-ebs-encryption-at-rest" {
			found = true
		}
	}
	if !found {
		t.Errorf("waf-sec-ebs-encryption-at-rest not persisted: %+v", rows)
	}
}

// TestCheckCmd_PersistRejectsReadOnly confirms --persist rejects when
// the global --db-readonly flag is on.
func TestCheckCmd_PersistRejectsReadOnly(t *testing.T) {
	seedCheckDB(t)
	resetCheckFlags()
	checkPersist = true
	dbReadOnly = true
	t.Cleanup(func() { checkPersist, dbReadOnly = false, false })

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"check", "--packs", "aws-waf", "-o", "json"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("want read-only rejection, got %v", err)
	}
}
