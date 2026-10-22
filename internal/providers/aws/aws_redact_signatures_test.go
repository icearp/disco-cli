package aws

import (
	"strings"
	"testing"
)

func TestAccessKeyRE(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"AKIAIOSFODNN7EXAMPLE", true},
		{"key ASIAIOSFODNN7EXAMPLE in prose", true},
		{"arn:aws:s3:::xAKIAIOSFODNN7EXAMPLE", true},
		{"AKIA-not-a-key", false},
		{"t3.micro", false},
	} {
		if got := accessKeyRE.MatchString(tc.in); got != tc.want {
			t.Errorf("accessKeyRE.MatchString(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestIsARNOrSecretRef(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"arn:aws:secretsmanager:us-east-1:123456789012:secret:db-AbCdEf", true},
		{"arn:aws:s3:::my-bucket", true},
		{"arn:aws:iam::aws:policy/ReadOnlyAccess", true},
		{"arn:aws-us-gov:ssm:us-gov-west-1:123456789012:parameter/app/db", true},
		{"{{resolve:ssm:/app/db-host:1}}", true},
		{"{{resolve:ssm-secure:/app/db-pass:3}}", true},
		{"{{resolve:secretsmanager:db:SecretString:password}}", true},
		{"/aws/reference/secretsmanager/db-creds", true},
		{"arn:aws-cn:s3:::my-bucket", true},
		{"arn:aws-eusc:s3:eusc-de-east-1:123456789012:b", true},
		{"arn:aws:lambda:us-east-1:123456789012:function:f:$LATEST", true},
		{"{{resolve:ssm:arn:aws:ssm:us-east-1:123456789012:parameter/app/db:2}}", true},
		{"arn:aws-iso-b:s3:::my-bucket", true},
		{"{{resolve:ssm:/app/db-host}}", true},
		{"arn:aws:iam::12345:role/x", false},
		{"arn:awsHUNTER:a:::b", false},
		{"arn:awshunter:a:::b", false},
		{"arn:aws:s3:hunter2:123456789012:b", false},
		{"arn:aws:lambda:us-east-1:123456789012:function:f$LATEST", false},
		{"arn:aws:lambda:us-east-1:123456789012:function:$LATEST:f", false},
		{"{{resolve:ssm:arn:aws:ssm:us-east-1:123456789012:parameter/x$y}}", false},
		{"arn:aws:x:::Hunter2!pass", false},
		{"arn:aws:s3:::bucket/x#password:hunter2", false},
		{"arn:aws:s3:Hunter2:123456789012:b", false},
		{"arn:aws:s3:us-east-1:hunter2:b", false},
		{"arn:aws:s3:::" + strings.Repeat("a", 2048-len("arn:aws:s3:::")), true},
		{"arn:aws:s3:::" + strings.Repeat("a", 2049-len("arn:aws:s3:::")), false},
		{"{{resolve:ssm:Hunter2!pass}}", false},
		{"{{resolve:ssm:/a:latest}}", false},
		{"{{resolve:ssm:a\nhunter2 is the password}}", false},
		{"{{resolve:secretsmanager:x:SecretString:pw}}\n", false},
		{"{{resolve:secretsmanager:a b}}", false},
		{"/aws/reference/secretsmanager/a b", false},
		{"see arn:aws:s3:::my-bucket", false},
		{"{{resolve:other:x}}", false},
		{"/app/db-host", false},
		{"", false},
	} {
		if got := isARNOrSecretRef(tc.in); got != tc.want {
			t.Errorf("isARNOrSecretRef(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}

func TestEC2ResourceIDRE(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"sg-0123abcd", true},
		{"subnet-0123456789abcdef0", true},
		{"eipalloc-0123456789abcdef0", true},
		{"i-0123456789abcdef0", true},
		{"sg-0123abc", false},
		{"sg-0123456789ABCDEF0", false},
		{"sg-0123456789abcdef01", false},
		{"my-secret-password", false},
		{"app-cafe1234", false},
		{"token-0123456789abcdef0", false},
		{"tgw-attach-0123456789abcdef0", true},
		{"vpce-0123abcd", true},
		{"9f8e7d6c-5b4a-4321-8fed-cba987654321", false},
	} {
		if got := ec2ResourceIDRE.MatchString(tc.in); got != tc.want {
			t.Errorf("ec2ResourceIDRE.MatchString(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}
