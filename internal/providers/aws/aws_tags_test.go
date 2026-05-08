package aws

import (
	"encoding/json"
	"testing"

	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
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

// TestAWSTagsJSON_EC2Tags verifies that EC2 SDK tag slices are converted to
// the expected JSON map format stored in resources.tags.
func TestAWSTagsJSON_EC2Tags(t *testing.T) {
	k1, v1, k2, v2 := "Name", "web-server", "env", "prod"
	tags := []ec2types.Tag{
		{Key: &k1, Value: &v1},
		{Key: &k2, Value: &v2},
	}

	result := awsTagsJSON(tags)
	if result == nil {
		t.Fatal("awsTagsJSON: got nil, want non-nil")
	}

	var m map[string]string
	if err := json.Unmarshal([]byte(*result), &m); err != nil {
		t.Fatalf("awsTagsJSON: result is not valid JSON: %v", err)
	}
	if m["Name"] != "web-server" {
		t.Errorf("Name tag: got %q, want %q", m["Name"], "web-server")
	}
	if m["env"] != "prod" {
		t.Errorf("env tag: got %q, want %q", m["env"], "prod")
	}
}

// TestAWSTagsJSON_Empty verifies that nil is returned for an empty tag slice,
// matching the nullable column expectation in the database.
func TestAWSTagsJSON_Empty(t *testing.T) {
	if got := awsTagsJSON([]ec2types.Tag{}); got != nil {
		t.Errorf("awsTagsJSON(empty): got %q, want nil", *got)
	}
}

// TestEC2TagName_Found verifies that the "Name" tag value is extracted.
func TestEC2TagName_Found(t *testing.T) {
	k1, v1 := "env", "prod"
	k2, v2 := "Name", "my-instance"
	tags := []ec2types.Tag{
		{Key: &k1, Value: &v1},
		{Key: &k2, Value: &v2},
	}
	got := ec2TagName(tags)
	if got == nil || *got != "my-instance" {
		t.Errorf("ec2TagName: got %v, want %q", got, "my-instance")
	}
}

// TestEC2TagName_Absent verifies that nil is returned when no "Name" tag exists.
func TestEC2TagName_Absent(t *testing.T) {
	k1, v1 := "env", "prod"
	tags := []ec2types.Tag{{Key: &k1, Value: &v1}}
	if got := ec2TagName(tags); got != nil {
		t.Errorf("ec2TagName (no Name tag): got %q, want nil", *got)
	}
}

// TestEC2TagName_EmptySlice verifies that nil is returned for an empty tag slice.
func TestEC2TagName_EmptySlice(t *testing.T) {
	if got := ec2TagName(nil); got != nil {
		t.Errorf("ec2TagName(nil): got %q, want nil", *got)
	}
}
