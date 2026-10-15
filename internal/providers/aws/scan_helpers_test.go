package aws

import (
	"errors"
	"net"
	"testing"
)

func TestDemoteTransient_Outcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		kept      bool // the error is returned unchanged rather than dropped
		wantWarns int
	}{
		{"transient API error is a warning", apiErr("ServiceUnavailable", "try later"), false, 1},
		{"dial error is a warning", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, false, 1},
		{"NXDOMAIN is left to the dispatcher", &net.DNSError{Err: "no such host", Name: "autoscaling.us-east-1.amazonaws.com", IsNotFound: true}, true, 0},
		{"hard error is returned", apiErr("ValidationException", "bad"), true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "op")
			got := demoteTransient(st, "op", newTestAccount(testAccountID), testRegion, tc.err)
			switch {
			case tc.kept && !errors.Is(got, tc.err):
				t.Errorf("demoteTransient(%v) = %v, want it returned unchanged", tc.err, got)
			case !tc.kept && got != nil:
				t.Errorf("demoteTransient(%v) = %v, want nil", tc.err, got)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}
