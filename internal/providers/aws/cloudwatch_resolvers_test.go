package aws

import (
	"fmt"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/store"
)

const testCWRegion = "us-east-1"

// --- resolveAlarmSNSActions ---

func TestResolveAlarmSNSActions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:my-alarm", testCWRegion, testAccountID)
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:my-topic", testCWRegion, testAccountID)
	attrsJSON := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"my-alarm","AlarmActions":[%q],"OKActions":[],"InsufficientDataActions":[]}`, alarmARN, topicARN)

	alarmID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, attrsJSON)
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testCWRegion, "{}")

	if err := resolveAlarmSNSActions(acct, st); err != nil {
		t.Fatalf("resolveAlarmSNSActions: %v", err)
	}

	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, topicID, "uses")
}

func TestResolveAlarmSNSActions_MultipleActions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:multi-alarm", testCWRegion, testAccountID)
	topic1ARN := fmt.Sprintf("arn:aws:sns:%s:%s:topic-1", testCWRegion, testAccountID)
	topic2ARN := fmt.Sprintf("arn:aws:sns:%s:%s:topic-2", testCWRegion, testAccountID)
	// Same topic in both AlarmActions and OKActions — dedup should produce one edge.
	attrsJSON := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"multi-alarm","AlarmActions":[%q,%q],"OKActions":[%q],"InsufficientDataActions":[]}`,
		alarmARN, topic1ARN, topic2ARN, topic1ARN)

	alarmID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, attrsJSON)
	topic1ID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topic1ARN, testCWRegion, "{}")
	topic2ID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topic2ARN, testCWRegion, "{}")

	if err := resolveAlarmSNSActions(acct, st); err != nil {
		t.Fatalf("resolveAlarmSNSActions: %v", err)
	}

	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, topic1ID, "uses")
	assertRelationship(t, rels, alarmID, topic2ID, "uses")
}

func TestResolveAlarmSNSActions_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:empty-alarm", testCWRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, "{}")

	if err := resolveAlarmSNSActions(acct, st); err != nil {
		t.Fatalf("resolveAlarmSNSActions with empty attrs: %v", err)
	}
}

// --- resolveCompositeAlarmChildren ---

func TestResolveCompositeAlarmChildren_ByARN(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	childARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:child-alarm", testCWRegion, testAccountID)
	compositeARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:composite-alarm", testCWRegion, testAccountID)
	alarmRule := fmt.Sprintf(`ALARM(%q)`, childARN)
	compositeAttrs := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"composite-alarm","AlarmRule":%q}`, compositeARN, alarmRule)

	childID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, childARN, testCWRegion,
		fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"child-alarm"}`, childARN))
	compositeID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchCompositeAlarm, compositeARN, testCWRegion, compositeAttrs)

	if err := resolveCompositeAlarmChildren(acct, st); err != nil {
		t.Fatalf("resolveCompositeAlarmChildren: %v", err)
	}

	rels, err := st.RelationshipsFrom(compositeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, compositeID, childID, "contains")
}

func TestResolveCompositeAlarmChildren_ByName(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	childARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:named-child", testCWRegion, testAccountID)
	compositeARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:named-composite", testCWRegion, testAccountID)
	// AlarmRule references the child by name, not ARN.
	compositeAttrs := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"named-composite","AlarmRule":"ALARM(\"named-child\")"}`, compositeARN)

	// Insert the child alarm with Name set (upsertTestResource doesn't) so the
	// resolver's name-based lookup can find it.
	childName := "named-child"
	region := testCWRegion
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID,
		Type:           TypeCloudWatchAlarm,
		NativeID:       childARN,
		Name:           &childName,
		Region:         &region,
		AttributesJSON: fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"named-child"}`, childARN),
		DiscoveredBy:   testScanID,
	}); err != nil {
		t.Fatalf("UpsertResource child: %v", err)
	}
	childID := store.ResourceID("aws", acct.ID, childARN)
	compositeID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchCompositeAlarm, compositeARN, testCWRegion, compositeAttrs)

	if err := resolveCompositeAlarmChildren(acct, st); err != nil {
		t.Fatalf("resolveCompositeAlarmChildren: %v", err)
	}

	rels, err := st.RelationshipsFrom(compositeID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, compositeID, childID, "contains")
}

func TestResolveCompositeAlarmChildren_NoRule(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	compositeARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:empty-composite", testCWRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchCompositeAlarm, compositeARN, testCWRegion, "{}")

	if err := resolveCompositeAlarmChildren(acct, st); err != nil {
		t.Fatalf("resolveCompositeAlarmChildren with empty attrs: %v", err)
	}
}

// --- resolveAlarmDimensions ---

// alarmDimHelper seeds an alarm with (namespace, dim name, dim value) and
// returns the alarm resource ID.
func alarmDimHelper(t *testing.T, st *store.Store, acct *account, alarmName, namespace string, dims ...[2]string) string {
	t.Helper()
	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:%s", testCWRegion, testAccountID, alarmName)
	parts := make([]string, 0, len(dims))
	for _, d := range dims {
		parts = append(parts, fmt.Sprintf(`{"Name":%q,"Value":%q}`, d[0], d[1]))
	}
	dimJSON := "[" + strings.Join(parts, ",") + "]"
	attrs := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":%q,"Namespace":%q,"Dimensions":%s}`,
		alarmARN, alarmName, namespace, dimJSON)
	return upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, attrs)
}

// upsertNamedTestResource inserts a resource with Name set (upsertTestResource
// doesn't). Needed for dimension types keyed on (region, Name).
func upsertNamedTestResource(t *testing.T, st *store.Store, rtype, nativeID, name, region string) string {
	t.Helper()
	n := name
	r := region
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: testAccountID,
		Type: rtype, NativeID: nativeID,
		Name: &n, Region: &r,
		AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("UpsertResource %s/%s: %v", rtype, nativeID, err)
	}
	return store.ResourceID("aws", testAccountID, nativeID)
}

func TestResolveAlarmDimension_EC2Instance(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instanceARN := ec2ARN(testCWRegion, acct.ID, "instance", "i-abc123")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, testCWRegion, "{}")
	alarmID := alarmDimHelper(t, st, acct, "cpu-high", "AWS/EC2", [2]string{"InstanceId", "i-abc123"})

	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, instID, "uses")
}

func TestResolveAlarmDimension_RDSInstance(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dbARN := fmt.Sprintf("arn:aws:rds:%s:%s:db:mydb", testCWRegion, testAccountID)
	dbID := upsertNamedTestResource(t, st, TypeRDSDBInstance, dbARN, "mydb", testCWRegion)
	alarmID := alarmDimHelper(t, st, acct, "rds-cpu", "AWS/RDS", [2]string{"DBInstanceIdentifier", "mydb"})

	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, dbID, "uses")
}

func TestResolveAlarmDimension_LambdaFunction(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:myfn", testCWRegion, testAccountID)
	fnID := upsertNamedTestResource(t, st, TypeLambdaFunction, fnARN, "myfn", testCWRegion)
	alarmID := alarmDimHelper(t, st, acct, "fn-errors", "AWS/Lambda", [2]string{"FunctionName", "myfn"})

	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, fnID, "uses")
}

func TestResolveAlarmDimension_ALB(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	lbARN := fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/app/my-lb/abc123", testCWRegion, testAccountID)
	lbID := upsertTestResource(t, st, "aws", acct.ID, TypeELBv2LoadBalancer, lbARN, testCWRegion, "{}")
	alarmID := alarmDimHelper(t, st, acct, "alb-5xx", "AWS/ApplicationELB", [2]string{"LoadBalancer", "app/my-lb/abc123"})

	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, lbID, "uses")
}

func TestResolveAlarmDimension_EKSCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusARN := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/prod", testCWRegion, testAccountID)
	clusID := upsertNamedTestResource(t, st, TypeEKSCluster, clusARN, "prod", testCWRegion)
	alarmID := alarmDimHelper(t, st, acct, "eks-health", "AWS/EKS", [2]string{"ClusterName", "prod"})

	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, clusID, "uses")
}

func TestResolveAlarmDimension_UnknownNamespace(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmID := alarmDimHelper(t, st, acct, "kafka-alarm", "AWS/Kafka", [2]string{"Cluster Name", "my-msk"})
	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveAlarmDimension_UnmatchedDimension(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmID := alarmDimHelper(t, st, acct, "no-match", "AWS/EC2", [2]string{"InstanceId", "i-doesnotexist"})
	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Fatalf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveAlarmDimension_MissingDimensions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:bare", testCWRegion, testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, `{"Namespace":"AWS/EC2"}`)
	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions with missing Dimensions: %v", err)
	}
}

func TestResolveAlarmDimension_MetricMath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fnARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:mathfn", testCWRegion, testAccountID)
	fnID := upsertNamedTestResource(t, st, TypeLambdaFunction, fnARN, "mathfn", testCWRegion)

	alarmARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:math", testCWRegion, testAccountID)
	attrs := fmt.Sprintf(`{"AlarmArn":%q,"AlarmName":"math","Metrics":[{"MetricStat":{"Metric":{"Namespace":"AWS/Lambda","Dimensions":[{"Name":"FunctionName","Value":"mathfn"}]}}}]}`, alarmARN)
	alarmID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchAlarm, alarmARN, testCWRegion, attrs)

	if err := resolveAlarmDimensions(acct, st); err != nil {
		t.Fatalf("resolveAlarmDimensions: %v", err)
	}
	rels, err := st.RelationshipsFrom(alarmID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, alarmID, fnID, "uses")
}

func TestResolveCWMetricStreamRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fhARN := fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/cw-stream", testRegion, acct.ID)
	fhID := upsertTestResource(t, st, "aws", acct.ID, TypeFirehoseDeliveryStream, fhARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/cw-ms", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	msARN := fmt.Sprintf("arn:aws:cloudwatch:%s:%s:metric-stream/ms-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"FirehoseArn":"%s","RoleArn":"%s"}`, fhARN, roleARN)
	msID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudWatchMetricStream, msARN, testRegion, attrs)
	if err := resolveCWMetricStreamRefs(acct, st); err != nil {
		t.Fatalf("resolveCWMetricStreamRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(msID)
	assertRelationship(t, rels, msID, fhID, store.RelUses)
	assertRelationship(t, rels, msID, roleID, store.RelUses)
}
