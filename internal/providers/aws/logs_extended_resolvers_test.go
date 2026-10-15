package aws

import (
	"fmt"
	"testing"

	logsTypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/icearp/disco-cli/store"
)

func TestLogGroupARNFromChild(t *testing.T) {
	cases := []struct{ in, want string }{
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo:*/stream/2024-01-01", "arn:aws:logs:us-east-1:123:log-group:/svc/foo:*"},
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo/filter/my-filter", "arn:aws:logs:us-east-1:123:log-group:/svc/foo"},
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo/subscription/sub-1", "arn:aws:logs:us-east-1:123:log-group:/svc/foo"},
		{"arn:aws:logs:us-east-1:123:log-group:/svc/foo", ""},
	}
	for _, c := range cases {
		if got := logGroupARNFromChild(c.in); got != c.want {
			t.Errorf("logGroupARNFromChild(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveLogsLogStreamParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/svc/foo")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	lsARN := lgARN + "/stream/2024-01-01"
	lsID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogStream, lsARN, testRegion, "{}")
	if err := resolveLogsLogStreamParent(acct, st); err != nil {
		t.Fatalf("resolveLogsLogStreamParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lsID)
	assertRelationship(t, rels, lsID, lgID, store.RelAttachedTo)
}

func TestResolveLogsSubscriptionFilterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/svc/bar")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	fnID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, fnARN, testRegion, "{}")

	subARN := lgARN + "/subscription/sub-1"
	attrs := fmt.Sprintf(`{"DestinationArn":%q}`, fnARN)
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsSubscriptionFilter, subARN, testRegion, attrs)

	if err := resolveLogsSubscriptionFilterRefs(acct, st); err != nil {
		t.Fatalf("resolveLogsSubscriptionFilterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(subID)
	assertRelationship(t, rels, subID, lgID, store.RelAttachedTo)
	assertRelationship(t, rels, subID, fnID, store.RelRoutesTo)
}

func TestResolveLogsDestinationTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	streamARN := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/my-stream", testRegion, acct.ID)
	streamID := upsertTestResource(t, st, "aws", acct.ID, TypeKinesisStream, streamARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dest-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	destARN := fmt.Sprintf("arn:aws:logs:%s:%s:destination:my-dest", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"TargetArn":%q,"RoleArn":%q}`, streamARN, roleARN)
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsDestination, destARN, testRegion, attrs)
	if err := resolveLogsDestinationTargets(acct, st); err != nil {
		t.Fatalf("resolveLogsDestinationTargets: %v", err)
	}
	rels, _ := st.RelationshipsFrom(destID)
	assertRelationship(t, rels, destID, streamID, store.RelRoutesTo)
	assertRelationship(t, rels, destID, roleID, store.RelAssumes)
}

func TestResolveLogsQueryDefinitionLogGroups(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lgARN := logGroupNativeIDFromName(acct.ID, testRegion, "/svc/q")
	lgID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsLogGroup, lgARN, testRegion, "{}")
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsQueryDefinition, "qd-1", testRegion, `{"LogGroupNames":["/svc/q"]}`)
	if err := resolveLogsQueryDefinitionLogGroups(acct, st); err != nil {
		t.Fatalf("resolveLogsQueryDefinitionLogGroups: %v", err)
	}
	rels, _ := st.RelationshipsFrom(qID)
	assertRelationship(t, rels, qID, lgID, store.RelUses)
}

func TestResolveLogsSyslogVPCEndpoints_LinksOnlyScannedEndpoints(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-1"), testRegion, "{}")
	add := func(vpce string) string {
		return upsertTestResource(t, st, "aws", acct.ID, TypeLogsSyslogConfig,
			lgTestARN("a")+":syslog-configuration:"+vpce, testRegion, mustJSON(syslogConfig(lgTestARN("a"), vpce)))
	}
	scanned, unscanned := add("vpce-1"), add("vpce-2")
	noAttrs := upsertTestResource(t, st, "aws", acct.ID, TypeLogsSyslogConfig, lgTestARN("a")+":syslog-configuration:vpce-3", testRegion, "{}")
	if err := resolveLogsSyslogVPCEndpoints(acct, st); err != nil {
		t.Fatalf("resolveLogsSyslogVPCEndpoints: %v", err)
	}
	rels, err := st.RelationshipsFrom(scanned, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, scanned, vpceID, store.RelUses)
	for _, id := range []string{unscanned, noAttrs} {
		if rels, err := st.RelationshipsFrom(id); err != nil || len(rels) != 0 {
			t.Errorf("edges from %s = %v, %v; want none", id, rels, err)
		}
	}
}

func TestResolveLogsSyslogVPCEndpoints_UndecodableAttrsFail(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeLogsSyslogConfig, lgTestARN("a")+":syslog-configuration:vpce-1", testRegion, `{"VpcEndpointId":7}`)
	if err := resolveLogsSyslogVPCEndpoints(acct, st); err == nil {
		t.Error("err = nil; want the decode error")
	}
}

func TestResolveLogsSourceIntegrations_ContainsUnderScannedIntegration(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	intARN := s3tIntegrationARN("int-1")
	upsertTestResource(t, st, "aws", acct.ID, TypeObservabilityAdminS3TableIntegration, intARN, testRegion, "{}")
	src := logsSourceNativeID(intARN, "src-1")
	orphan := logsSourceNativeID(s3tIntegrationARN("int-gone"), "src-2")
	attrs := func(id string) string { return mustJSON(logsTypes.S3TableIntegrationSource{Identifier: &id}) }
	upsertTestResource(t, st, "aws", acct.ID, TypeLogsSource, src, testRegion, attrs("src-1"))
	orphanID := upsertTestResource(t, st, "aws", acct.ID, TypeLogsSource, orphan, testRegion, attrs("src-2"))
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	if err := resolveLogsSourceIntegrations(acct, st); err != nil {
		t.Fatalf("resolveLogsSourceIntegrations: %v", err)
	}
	assertContainsNative(t, st, intARN, src)
	if rels, err := st.RelationshipsTo(orphanID, store.RelContains); err != nil || len(rels) != 0 {
		t.Errorf("contains edges into %s = %v, %v; want none (integration not scanned)", orphan, rels, err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v; want none (no pair is recorded for an unscanned integration)", warnings)
	}
}

func TestLogsSourceIntegrationARN_RoundTrips(t *testing.T) {
	intARN := s3tIntegrationARN("int-1")
	if got := logsSourceIntegrationARN(logsSourceNativeID(intARN, "a/source/b")); got != intARN {
		t.Errorf("logsSourceIntegrationARN(...) = %q, want %q", got, intARN)
	}
}
