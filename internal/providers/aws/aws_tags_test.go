package aws

import (
	"encoding/json"
	"testing"

	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	firehosetypes "github.com/aws/aws-sdk-go-v2/service/firehose/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// assertTagsJSON unmarshals a tag-JSON pointer and asserts a single-entry map.
func assertTagsJSON(t *testing.T, label string, got *string, wantKey, wantVal string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: nil result", label)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*got), &m); err != nil {
		t.Fatalf("%s: invalid JSON %q: %v", label, *got, err)
	}
	if m[wantKey] != wantVal {
		t.Errorf("%s: %q = %q, want %q", label, wantKey, m[wantKey], wantVal)
	}
}

// TestAWSTagsJSON_AllUnionMembers exercises every type in the awsTag union so a
// missing case in the type-switch (which silently drops tags) is caught at
// test time. Per aws/CLAUDE.md "Tag JSON helpers": new SDK service tag types
// must be added to both the union AND the case block — both edits or helper
// drops tags silently. EC2 + ec2types is already covered by aws_test.go.
func TestAWSTagsJSON_AllUnionMembers(t *testing.T) {
	k, v := "K", "V"

	assertTagsJSON(t, "acm", awsTagsJSON([]acmtypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "cloudfront", awsTagsJSON([]cloudfronttypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "ecr", awsTagsJSON([]ecrtypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "ecs", awsTagsJSON([]ecstypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "elasticache", awsTagsJSON([]elasticachetypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "firehose", awsTagsJSON([]firehosetypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "iam", awsTagsJSON([]iamtypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "kinesis", awsTagsJSON([]kinesistypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "rds", awsTagsJSON([]rdstypes.Tag{{Key: &k, Value: &v}}), k, v)
	assertTagsJSON(t, "route53", awsTagsJSON([]route53types.Tag{{Key: &k, Value: &v}}), k, v)
}

// TestAWSTagsJSON_NilPointersSkipped asserts that tags with nil Key or Value
// are dropped rather than panicking or producing a bogus "" entry.
func TestAWSTagsJSON_NilPointersSkipped(t *testing.T) {
	k, v := "Real", "Value"
	tags := []iamtypes.Tag{
		{Key: nil, Value: &v},
		{Key: &k, Value: nil},
		{Key: &k, Value: &v},
	}
	got := awsTagsJSON(tags)
	if got == nil {
		t.Fatal("nil result")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*got), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(m) != 1 || m["Real"] != "Value" {
		t.Errorf("got %v, want single Real=Value entry", m)
	}
}

// TestMapTagsJSON covers the alternative helper for services whose SDK returns
// tags as map[string]string rather than typed slices.
func TestMapTagsJSON(t *testing.T) {
	if got := mapTagsJSON(nil); got != nil {
		t.Errorf("nil map: got %q, want nil", *got)
	}
	if got := mapTagsJSON(map[string]string{}); got != nil {
		t.Errorf("empty map: got %q, want nil", *got)
	}
	got := mapTagsJSON(map[string]string{"env": "prod", "team": "core"})
	if got == nil {
		t.Fatal("nil result")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(*got), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["env"] != "prod" || m["team"] != "core" {
		t.Errorf("round-trip: %v", m)
	}
}
