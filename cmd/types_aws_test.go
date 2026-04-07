package cmd

import (
	"testing"
)

func TestCFNToDiscoName(t *testing.T) {
	tests := []struct {
		cfn  string
		want string
	}{
		{"AWS::EC2::Instance", "aws:ec2:instance"},
		{"AWS::EC2::VPC", "aws:ec2:vpc"},
		{"AWS::EC2::SecurityGroup", "aws:ec2:security-group"},
		{"AWS::EC2::InternetGateway", "aws:ec2:internet-gateway"},
		{"AWS::EC2::Volume", "aws:ec2:volume"},
		{"AWS::EC2::Subnet", "aws:ec2:subnet"},
		{"AWS::S3::Bucket", "aws:s3:bucket"},
		{"AWS::RDS::DBInstance", "aws:rds:db-instance"},
		{"AWS::ElasticLoadBalancingV2::LoadBalancer", "aws:elasticloadbalancingv2:load-balancer"},
		{"AWS::DynamoDB::Table", "aws:dynamodb:table"},
		{"AWS::Lambda::Function", "aws:lambda:function"},
		{"AWS::IAM::Role", "aws:iam:role"},
		{"AWS::SNS::Topic", "aws:sns:topic"},
		{"AWS::SQS::Queue", "aws:sqs:queue"},
		// third-party vendors
		{"TF::Module::Resource", "tf:module:resource"},
		{"TrendMicro::DeepSecurity::Policy", "trendmicro:deepsecurity:policy"},
		{"Datadog::Monitors::Monitor", "datadog:monitors:monitor"},
		// malformed: no :: separators
		{"notatype", "notatype"},
	}
	for _, tt := range tests {
		got := cfnToDiscoName(tt.cfn)
		if got != tt.want {
			t.Errorf("cfnToDiscoName(%q) = %q, want %q", tt.cfn, got, tt.want)
		}
	}
}

func TestPascalToKebab(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Instance", "instance"},
		{"LoadBalancer", "load-balancer"},
		{"DBInstance", "db-instance"},
		{"InternetGateway", "internet-gateway"},
		{"SecurityGroup", "security-group"},
		{"VPC", "vpc"},
		{"Table", "table"},
		{"Function", "function"},
		{"", ""},
	}
	for _, tt := range tests {
		got := pascalToKebab(tt.input)
		if got != tt.want {
			t.Errorf("pascalToKebab(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildRows(t *testing.T) {
	cfnNames := []string{
		"AWS::EC2::Instance",       // → aws:ec2:instance  (covered)
		"AWS::EC2::SecurityGroup",  // → aws:ec2:security-group  (covered)
		"AWS::ACM::Certificate",    // → aws:acm:certificate  (not covered)
	}
	knownSet := map[string]bool{
		"aws:ec2:instance":       true,
		"aws:ec2:security-group": true,
	}

	all := buildRows(cfnNames, knownSet, "all", nil)
	if len(all) != 3 {
		t.Errorf("filter=all: got %d rows, want 3", len(all))
	}

	covered := buildRows(cfnNames, knownSet, "covered", nil)
	if len(covered) != 2 {
		t.Errorf("filter=covered: got %d rows, want 2", len(covered))
	}
	for _, r := range covered {
		if !r.Covered {
			t.Errorf("filter=covered: row %q has Covered=false", r.CFNName)
		}
	}

	uncovered := buildRows(cfnNames, knownSet, "uncovered", nil)
	if len(uncovered) != 1 {
		t.Errorf("filter=uncovered: got %d rows, want 1", len(uncovered))
	}
	if uncovered[0].CFNName != "AWS::ACM::Certificate" {
		t.Errorf("filter=uncovered: got %q, want AWS::ACM::Certificate", uncovered[0].CFNName)
	}

	// --services filter: only EC2 types (2 rows), ACM excluded.
	ec2only := buildRows(cfnNames, knownSet, "all", []string{"ec2"})
	if len(ec2only) != 2 {
		t.Errorf("services=ec2: got %d rows, want 2", len(ec2only))
	}
	for _, r := range ec2only {
		if r.DiscoName[:7] != "aws:ec2" {
			t.Errorf("services=ec2: unexpected type %q", r.DiscoName)
		}
	}
}
