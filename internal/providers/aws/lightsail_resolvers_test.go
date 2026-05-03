package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func upsertLightsailRow(t *testing.T, st *store.Store, acct *account, rtype, arn, name, region, attrs string) string {
	t.Helper()
	n := name
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: rtype,
		NativeID: arn, Region: &region, Name: &n,
		AttributesJSON: attrs, DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsert %s: %v", rtype, err)
	}
	return store.ResourceID("aws", acct.ID, rtype, arn)
}

func TestResolveLightsailDatabaseSnapshotParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dbARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:RelationalDatabase/db-1", testRegion, acct.ID)
	dbID := upsertLightsailRow(t, st, acct, TypeLightsailDatabase, dbARN, "my-db", testRegion, "{}")
	snapARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:RelationalDatabaseSnapshot/snap-1", testRegion, acct.ID)
	snapID := upsertLightsailRow(t, st, acct, TypeLightsailDatabaseSnapshot, snapARN, "snap-1", testRegion, `{"FromRelationalDatabaseName":"my-db"}`)
	if err := resolveLightsailDatabaseSnapshotParent(acct, st); err != nil {
		t.Fatalf("resolveLightsailDatabaseSnapshotParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snapID)
	assertRelationship(t, rels, snapID, dbID, store.RelAttachedTo)
}

func TestResolveLightsailDiskSnapshotParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Disk/d-1", testRegion, acct.ID)
	dID := upsertLightsailRow(t, st, acct, TypeLightsailDisk, dARN, "my-disk", testRegion, "{}")
	snapARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:DiskSnapshot/ds-1", testRegion, acct.ID)
	snapID := upsertLightsailRow(t, st, acct, TypeLightsailDiskSnapshot, snapARN, "ds-1", testRegion, `{"FromDiskName":"my-disk"}`)
	if err := resolveLightsailDiskSnapshotParent(acct, st); err != nil {
		t.Fatalf("resolveLightsailDiskSnapshotParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snapID)
	assertRelationship(t, rels, snapID, dID, store.RelAttachedTo)
}

func TestResolveLightsailInstanceSnapshotParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Instance/i-1", testRegion, acct.ID)
	iID := upsertLightsailRow(t, st, acct, TypeLightsailInstance, iARN, "my-inst", testRegion, "{}")
	snapARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:InstanceSnapshot/is-1", testRegion, acct.ID)
	snapID := upsertLightsailRow(t, st, acct, TypeLightsailInstanceSnapshot, snapARN, "is-1", testRegion, `{"FromInstanceName":"my-inst"}`)
	if err := resolveLightsailInstanceSnapshotParent(acct, st); err != nil {
		t.Fatalf("resolveLightsailInstanceSnapshotParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(snapID)
	assertRelationship(t, rels, snapID, iID, store.RelAttachedTo)
}

func TestResolveLightsailLoadBalancerTlsCertParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lbARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:LoadBalancer/lb-1", testRegion, acct.ID)
	lbID := upsertLightsailRow(t, st, acct, TypeLightsailLoadBalancer, lbARN, "my-lb", testRegion, "{}")
	cARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:LoadBalancerTlsCertificate/c-1", testRegion, acct.ID)
	cID := upsertLightsailRow(t, st, acct, TypeLightsailLoadBalancerTlsCertificate, cARN, "c-1", testRegion, `{"LoadBalancerName":"my-lb"}`)
	if err := resolveLightsailLoadBalancerTlsCertParent(acct, st); err != nil {
		t.Fatalf("resolveLightsailLoadBalancerTlsCertParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, lbID, store.RelAttachedTo)
}

func TestResolveLightsailAlarmTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Instance/i-2", testRegion, acct.ID)
	iID := upsertLightsailRow(t, st, acct, TypeLightsailInstance, iARN, "alarmed", testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Alarm/a-1", testRegion, acct.ID)
	aID := upsertLightsailRow(t, st, acct, TypeLightsailAlarm, aARN, "cpu-alarm", testRegion, `{"MonitoredResourceInfo":{"Name":"alarmed","ResourceType":"Instance"}}`)
	if err := resolveLightsailAlarmTarget(acct, st); err != nil {
		t.Fatalf("resolveLightsailAlarmTarget: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, iID, store.RelUses)
}
