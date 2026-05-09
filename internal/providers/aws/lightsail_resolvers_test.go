package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
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
	cID := upsertLightsailRow(t, st, acct, TypeLightsailLoadBalancerTLSCertificate, cARN, "c-1", testRegion, `{"LoadBalancerName":"my-lb"}`)
	if err := resolveLightsailLoadBalancerTLSCertParent(acct, st); err != nil {
		t.Fatalf("resolveLightsailLoadBalancerTLSCertParent: %v", err)
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

func TestResolveLightsailInstanceDisks(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Disk/d-1", testRegion, acct.ID)
	dID := upsertLightsailRow(t, st, acct, TypeLightsailDisk, dARN, "data-disk", testRegion, "{}")
	iARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Instance/i-1", testRegion, acct.ID)
	iID := upsertLightsailRow(t, st, acct, TypeLightsailInstance, iARN, "i-1", testRegion,
		`{"Hardware":{"Disks":[{"Name":"data-disk"}]}}`)
	if err := resolveLightsailInstanceDisks(acct, st); err != nil {
		t.Fatalf("resolveLightsailInstanceDisks: %v", err)
	}
	rels, _ := st.RelationshipsFrom(iID)
	assertRelationship(t, rels, iID, dID, store.RelAttachedTo)
}

func TestResolveLightsailDiskAttachedInstance(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Instance/i-1", testRegion, acct.ID)
	iID := upsertLightsailRow(t, st, acct, TypeLightsailInstance, iARN, "host-1", testRegion, "{}")
	dARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Disk/d-1", testRegion, acct.ID)
	dID := upsertLightsailRow(t, st, acct, TypeLightsailDisk, dARN, "d-1", testRegion, `{"AttachedTo":"host-1"}`)
	if err := resolveLightsailDiskAttachedInstance(acct, st); err != nil {
		t.Fatalf("resolveLightsailDiskAttachedInstance: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, iID, store.RelAttachedTo)
}

func TestResolveLightsailStaticIPAttachedInstance(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Instance/i-1", testRegion, acct.ID)
	iID := upsertLightsailRow(t, st, acct, TypeLightsailInstance, iARN, "host-1", testRegion, "{}")
	sARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:StaticIp/sip-1", testRegion, acct.ID)
	sID := upsertLightsailRow(t, st, acct, TypeLightsailStaticIP, sARN, "ip-a", testRegion, `{"AttachedTo":"host-1"}`)
	if err := resolveLightsailStaticIPAttachedInstance(acct, st); err != nil {
		t.Fatalf("resolveLightsailStaticIPAttachedInstance: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, iID, store.RelAttachedTo)
}

func TestResolveLightsailLoadBalancerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Instance/i-1", testRegion, acct.ID)
	iID := upsertLightsailRow(t, st, acct, TypeLightsailInstance, iARN, "be-1", testRegion, "{}")
	cARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Certificate/c-1", testRegion, acct.ID)
	cID := upsertLightsailRow(t, st, acct, TypeLightsailCertificate, cARN, "cert-x", testRegion, "{}")
	lbARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:LoadBalancer/lb-1", testRegion, acct.ID)
	lbAttrs := `{"InstanceHealthSummary":[{"InstanceName":"be-1"}],"TlsCertificateSummaries":[{"Name":"cert-x","IsAttached":true}]}`
	lbID := upsertLightsailRow(t, st, acct, TypeLightsailLoadBalancer, lbARN, "lb-x", testRegion, lbAttrs)
	if err := resolveLightsailLoadBalancerRefs(acct, st); err != nil {
		t.Fatalf("resolveLightsailLoadBalancerRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lbID)
	assertRelationship(t, rels, lbID, iID, store.RelAttachedTo)
	assertRelationship(t, rels, lbID, cID, store.RelUses)
}

func TestResolveLightsailDistributionOrigin(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Instance/i-1", "us-west-2", acct.ID)
	iID := upsertLightsailRow(t, st, acct, TypeLightsailInstance, iARN, "origin-i", "us-west-2", "{}")
	cARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Certificate/c-1", "us-east-1", acct.ID)
	cID := upsertLightsailRow(t, st, acct, TypeLightsailCertificate, cARN, "cert-x", "us-east-1", "{}")
	dARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Distribution/d-1", "us-east-1", acct.ID)
	dAttrs := `{"Origin":{"Name":"origin-i","ResourceType":"Instance","RegionName":"us-west-2"},"CertificateName":"cert-x"}`
	dID := upsertLightsailRow(t, st, acct, TypeLightsailDistribution, dARN, "d-1", "us-east-1", dAttrs)
	if err := resolveLightsailDistributionOrigin(acct, st); err != nil {
		t.Fatalf("resolveLightsailDistributionOrigin: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, iID, store.RelUses)
	assertRelationship(t, rels, dID, cID, store.RelUses)
}

func TestResolveLightsailCertificateDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := fmt.Sprintf("arn:aws:lightsail:us-east-1:%s:Domain/d-1", acct.ID)
	dID := upsertLightsailRow(t, st, acct, TypeLightsailDomain, dARN, "example.com", "us-east-1", "{}")
	cARN := fmt.Sprintf("arn:aws:lightsail:%s:%s:Certificate/c-1", testRegion, acct.ID)
	cID := upsertLightsailRow(t, st, acct, TypeLightsailCertificate, cARN, "cert-x", testRegion, `{"DomainName":"example.com"}`)
	if err := resolveLightsailCertificateDomain(acct, st); err != nil {
		t.Fatalf("resolveLightsailCertificateDomain: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, dID, store.RelUses)
}
