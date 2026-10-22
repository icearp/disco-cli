package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/networkmonitor"
	networkmonitortypes "github.com/aws/aws-sdk-go-v2/service/networkmonitor/types"
)

// TestScanNetworkMonitor_ReadsTags pins that monitor and probe rows carry the
// Tags of their own listed element, and that an untagged element stores none.
func TestScanNetworkMonitor_ReadsTags(t *testing.T) {
	const (
		monTagged   = "arn:aws:networkmonitor:us-east-1:123456789012:monitor/m1"
		monUntagged = "arn:aws:networkmonitor:us-east-1:123456789012:monitor/m2"
		probeTagged = "arn:aws:networkmonitor:us-east-1:123456789012:probe/p1"
		probeBare   = "arn:aws:networkmonitor:us-east-1:123456789012:probe/p2"
	)
	handlers := map[string]func(in any) (any, error){
		"ListMonitors": func(any) (any, error) {
			return &networkmonitor.ListMonitorsOutput{Monitors: []networkmonitortypes.MonitorSummary{
				{MonitorArn: ptrStr(monTagged), MonitorName: ptrStr("m1"), Tags: map[string]string{"team": "net"}},
				{MonitorArn: ptrStr(monUntagged), MonitorName: ptrStr("m2")},
			}}, nil
		},
		"GetMonitor": func(in any) (any, error) {
			if sv(in.(*networkmonitor.GetMonitorInput).MonitorName) != "m1" {
				return &networkmonitor.GetMonitorOutput{MonitorArn: ptrStr(monUntagged)}, nil
			}
			return &networkmonitor.GetMonitorOutput{MonitorArn: ptrStr(monTagged), Probes: []networkmonitortypes.Probe{
				{ProbeArn: ptrStr(probeTagged), ProbeId: ptrStr("p1"), Tags: map[string]string{"tier": "gold"}},
				{ProbeArn: ptrStr(probeBare), ProbeId: ptrStr("p2")},
			}}, nil
		},
	}
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, errors.New("unexpected op"))
	total, _, err := scanNetworkMonitor(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanNetworkMonitor: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}

	monitors := rowsByNativeID(t, st, TypeNetworkMonitorMonitor)
	assertTagsMap(t, monitors[monTagged].TagsJSON, map[string]string{"team": "net"})
	if got := monitors[monUntagged].TagsJSON; got != nil {
		t.Errorf("untagged monitor TagsJSON = %q, want nil", *got)
	}
	probes := rowsByNativeID(t, st, TypeNetworkMonitorProbe)
	assertTagsMap(t, probes[probeTagged].TagsJSON, map[string]string{"tier": "gold"})
	if got := probes[probeBare].TagsJSON; got != nil {
		t.Errorf("untagged probe TagsJSON = %q, want nil", *got)
	}
}
