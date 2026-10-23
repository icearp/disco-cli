package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	emrctypes "github.com/aws/aws-sdk-go-v2/service/emrcontainers/types"
	"github.com/icearp/disco-cli/store"
)

// emrcRelsFrom returns the edges from id, failing the test on a read error.
func emrcRelsFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom(%s): %v", id, err)
	}
	return rels
}

func TestResolveEMRCEndpointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vcARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/vc-1", testRegion, acct.ID)
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, vcARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/emr-exec", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	epARN := vcARN + "/endpoints/ep-1"
	attrs := mustJSON(emrctypes.Endpoint{Arn: &epARN, ExecutionRoleArn: &roleARN})
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersEndpoint, epARN, testRegion, attrs)
	if err := resolveEMRCEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveEMRCEndpointRefs: %v", err)
	}
	rels := emrcRelsFrom(t, st, epID)
	assertRelationship(t, rels, epID, vcID, store.RelAttachedTo)
	assertRelationship(t, rels, epID, roleID, store.RelUses)
	if len(rels) != 2 {
		t.Errorf("edges = %+v, want the two above", rels)
	}
}

// A row whose attributes do not decode, or whose NativeID has no endpoint
// segment, is returned as an error after the other rows are wired.
func TestResolveEMRCEndpointRefs_BadRowsReturnedAfterOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vcARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/vc-1", testRegion, acct.ID)
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, vcARN, testRegion, "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersEndpoint, vcARN+"/endpoints/ep-bad", testRegion, `{"ExecutionRoleArn":7}`)
	upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersEndpoint, vcARN+"/ep-noseg", testRegion, "{}")
	goodID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersEndpoint, vcARN+"/endpoints/ep-good", testRegion, "{}")
	err := resolveEMRCEndpointRefs(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the attribute decode error", err)
	}
	if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 2 {
		t.Errorf("err = %v, want 2 joined row errors, got %d", err, n)
	}
	assertRelationship(t, emrcRelsFrom(t, st, goodID), goodID, vcID, store.RelAttachedTo)
}

func TestResolveEMRCJobTemplateRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/emr-tmpl", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	jtARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/jobtemplates/jt-1", testRegion, acct.ID)
	attrs := mustJSON(emrctypes.JobTemplate{Arn: &jtARN, KmsKeyArn: &keyARN, JobTemplateData: &emrctypes.JobTemplateData{ExecutionRoleArn: &roleARN}})
	jtID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersJobTemplate, jtARN, testRegion, attrs)
	if err := resolveEMRCJobTemplateRefs(acct, st); err != nil {
		t.Fatalf("resolveEMRCJobTemplateRefs: %v", err)
	}
	rels := emrcRelsFrom(t, st, jtID)
	assertRelationship(t, rels, jtID, roleID, store.RelUses)
	assertRelationship(t, rels, jtID, keyID, store.RelUses)
}

func TestResolveEMRCJobTemplateRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	jtARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/jobtemplates/jt-1", testRegion, acct.ID)
	jtID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersJobTemplate, jtARN, testRegion, "{}")
	if err := resolveEMRCJobTemplateRefs(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels := emrcRelsFrom(t, st, jtID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

// A row whose attributes do not decode is returned as an error after the
// other rows are wired.
func TestResolveEMRCJobTemplateRefs_DecodeErrorReturnedAfterOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/emr-tmpl", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersJobTemplate, fmt.Sprintf("arn:aws:emr-containers:%s:%s:/jobtemplates/jt-bad", testRegion, acct.ID), testRegion, `{"KmsKeyArn":7}`)
	goodARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/jobtemplates/jt-good", testRegion, acct.ID)
	goodID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersJobTemplate, goodARN, testRegion,
		mustJSON(emrctypes.JobTemplate{JobTemplateData: &emrctypes.JobTemplateData{ExecutionRoleArn: &roleARN}}))
	err := resolveEMRCJobTemplateRefs(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the attribute decode error", err)
	}
	assertRelationship(t, emrcRelsFrom(t, st, goodID), goodID, roleID, store.RelUses)
}

// Each cluster wires to the security configuration of its
// SecurityConfigurationId in its own region, never to the one with the same
// id in another region; an id naming no stored configuration gets no edge.
func TestResolveEMRCVirtualClusterToSecConfig(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	const otherRegion = "eu-west-1"
	scARN := func(region, id string) string {
		return fmt.Sprintf("arn:aws:emr-containers:%s:%s:/securityconfigurations/%s", region, acct.ID, id)
	}
	vc := func(region, id, scID string) string {
		arn := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/%s", region, acct.ID, id)
		attrs := mustJSON(emrctypes.VirtualCluster{Arn: &arn, SecurityConfigurationId: &scID})
		return upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, arn, region, attrs)
	}
	here := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersSecurityConfig, scARN(testRegion, "sc-1"), testRegion, "{}")
	there := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersSecurityConfig, scARN(otherRegion, "sc-1"), otherRegion, "{}")
	vcHere, vcThere, vcMissing := vc(testRegion, "vc-1", "sc-1"), vc(otherRegion, "vc-2", "sc-1"), vc(testRegion, "vc-3", "sc-missing")
	if err := resolveEMRCVirtualClusterToSecConfig(acct, st); err != nil {
		t.Fatalf("resolveEMRCVirtualClusterToSecConfig: %v", err)
	}
	for _, tc := range []struct{ from, to string }{{vcHere, here}, {vcThere, there}} {
		rels := emrcRelsFrom(t, st, tc.from)
		assertRelationship(t, rels, tc.from, tc.to, store.RelUses)
		if len(rels) != 1 {
			t.Errorf("edges from %s = %+v, want only the same-region one", tc.from, rels)
		}
	}
	if rels := emrcRelsFrom(t, st, vcMissing); len(rels) != 0 {
		t.Errorf("edges from the cluster naming an unstored configuration = %+v, want none", rels)
	}
}

func TestResolveEMRCVirtualClusterToSecConfig_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersSecurityConfig, fmt.Sprintf("arn:aws:emr-containers:%s:%s:/securityconfigurations/sc-1", testRegion, acct.ID), testRegion, "{}")
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/vc-1", testRegion, acct.ID), testRegion, "{}")
	if err := resolveEMRCVirtualClusterToSecConfig(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels := emrcRelsFrom(t, st, vcID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

// A row whose attributes do not decode, or whose NativeID is not a virtual
// cluster ARN, is returned as an error after the other rows are wired.
func TestResolveEMRCVirtualClusterToSecConfig_BadRowsReturnedAfterOthers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	scARN := fmt.Sprintf("arn:aws:emr-containers:%s:%s:/securityconfigurations/sc-1", testRegion, acct.ID)
	scID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersSecurityConfig, scARN, testRegion, "{}")
	withSC := mustJSON(emrctypes.VirtualCluster{SecurityConfigurationId: ptrStr("sc-1")})
	upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, "vc-noarn", testRegion, withSC)
	upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/vc-bad", testRegion, acct.ID), testRegion, `{"SecurityConfigurationId":7}`)
	goodID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRContainersVirtualCluster, fmt.Sprintf("arn:aws:emr-containers:%s:%s:/virtualclusters/vc-good", testRegion, acct.ID), testRegion, withSC)
	err := resolveEMRCVirtualClusterToSecConfig(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want the attribute decode error", err)
	}
	if n := len(err.(interface{ Unwrap() []error }).Unwrap()); n != 2 {
		t.Errorf("err = %v, want 2 joined row errors, got %d", err, n)
	}
	assertRelationship(t, emrcRelsFrom(t, st, goodID), goodID, scID, store.RelUses)
}

func TestEMRCSecurityConfigARN(t *testing.T) {
	for _, tc := range []struct {
		vcARN, want string
		ok          bool
	}{
		{"arn:aws:emr-containers:us-east-1:123456789012:/virtualclusters/vc-1", "arn:aws:emr-containers:us-east-1:123456789012:/securityconfigurations/sc-1", true},
		{"vc-1", "", false},
		{"", "", false},
	} {
		got, ok := emrcSecurityConfigARN(tc.vcARN, "sc-1")
		if got != tc.want || ok != tc.ok {
			t.Errorf("emrcSecurityConfigARN(%q, sc-1) = %q, %v; want %q, %v", tc.vcARN, got, ok, tc.want, tc.ok)
		}
	}
}
