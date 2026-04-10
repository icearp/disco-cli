package aws

import (
	"encoding/json"
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// TestEC2ARN verifies the ARN format used as NativeID for EC2 resources.
// The NativeID feeds into store.ResourceID, so any format change silently
// breaks all relationships that reference EC2 resources.
func TestEC2ARN(t *testing.T) {
	cases := []struct {
		region, account, rtype, id, want string
	}{
		{
			"us-east-1", "123456789012", "instance", "i-abc123",
			"arn:aws:ec2:us-east-1:123456789012:instance/i-abc123",
		},
		{
			"eu-west-1", "999999999999", "vpc", "vpc-xyz",
			"arn:aws:ec2:eu-west-1:999999999999:vpc/vpc-xyz",
		},
		{
			"us-west-2", "111111111111", "security-group", "sg-001",
			"arn:aws:ec2:us-west-2:111111111111:security-group/sg-001",
		},
	}
	for _, tc := range cases {
		got := ec2ARN(tc.region, tc.account, tc.rtype, tc.id)
		if got != tc.want {
			t.Errorf("ec2ARN(%q,%q,%q,%q) = %q, want %q",
				tc.region, tc.account, tc.rtype, tc.id, got, tc.want)
		}
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
