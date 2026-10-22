//go:build !slim || aws

package all_test

import (
	"encoding/json"
	"testing"

	"github.com/icearp/disco-cli/internal/redact"
)

// The AWS value shapes the aws package registers reach the classifier in a
// binary that links it: an access key id is redacted, and ARNs, CloudFormation
// dynamic references and EC2 ids are kept as redact's own tests describe.
func TestRedactUnlessSafe_AWSSignatures(t *testing.T) {
	const typ = "test:all:redact-signatures"
	redact.Register(redact.TypeRules{Type: typ, Attributes: []redact.Rule{{Path: "Settings[*].Value", Mode: redact.RedactUnlessSafe, KeyField: "Name"}}})
	const (
		accessKey  = "AKIAIOSFODNN7EXAMPLE"
		sessionKey = "ASIAIOSFODNN7EXAMPLE"
		secretARN  = "arn:aws:secretsmanager:us-east-1:123456789012:secret:db-AbCdEf"
		ssmRef     = "{{resolve:ssm:/app/db-host:1}}"
	)
	for _, tc := range []struct {
		name, key, value string
		want             any
	}{
		{"access key under innocuous key", "HOST", accessKey, redact.PlaceholderValueShape},
		{"session key under innocuous key", "HOST", "key " + sessionKey + " in prose", redact.PlaceholderValueShape},
		{"access key under sensitive key", "DB_PASSWORD", accessKey, redact.PlaceholderValueShape},
		{"access key inside an arn", "DB_PASSWORD", "arn:aws:s3:::uploads/" + accessKey, redact.PlaceholderValueShape},
		{"access key glued inside an arn", "X", "arn:aws:a:::x" + accessKey, redact.PlaceholderValueShape},
		{"access key after an arn", "X", secretARN + " " + accessKey, redact.PlaceholderValueShape},
		{"arn under sensitive key", "DB_PASSWORD", secretARN, secretARN},
		{"arn under innocuous key", "HOST", secretARN, secretARN},
		{"ssm reference under sensitive key", "DB_PASSWORD", ssmRef, ssmRef},
		{"secrets manager reference under sensitive key", "DB_PASSWORD", "{{resolve:secretsmanager:db:SecretString:password}}", "{{resolve:secretsmanager:db:SecretString:password}}"},
		{"parameter store secret path under sensitive key", "DB_PASSWORD", "/aws/reference/secretsmanager/db-creds", "/aws/reference/secretsmanager/db-creds"},
		{"ec2 id under innocuous key", "HOST", "subnet-0123456789abcdef0", "subnet-0123456789abcdef0"},
		{"ec2 id under sensitive key", "DB_PASSWORD", "sg-0123abcd", redact.PlaceholderKeyName},
		{"ec2 id list is not one id", "HOST", "sg-0123456789abcdef0,sg-0fedcba9876543210", redact.PlaceholderRule},
		{"malformed arn", "BACKUP_ROLE", "arn:aws:x:::Hunter2!pass", redact.PlaceholderRule},
		{"malformed ssm reference", "LEGACY", "{{resolve:ssm:Hunter2!pass}}", redact.PlaceholderRule},
		// Residual: ARN-shaped, no credential shape inside, so kept.
		{"token written as an arn", "X", "arn:aws:a:::ghp_abcdefghijklmnopqrstuvwxyz0123456789", "arn:aws:a:::ghp_abcdefghijklmnopqrstuvwxyz0123456789"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, err := json.Marshal(map[string]any{"Settings": []any{map[string]any{"Name": tc.key, "Value": tc.value}}})
			if err != nil {
				t.Fatal(err)
			}
			out, err := redact.Apply(typ, string(in))
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			var got struct{ Settings []struct{ Value any } }
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("unmarshal %s: %v", out, err)
			}
			if len(got.Settings) != 1 || got.Settings[0].Value != tc.want {
				t.Errorf("Apply(%s=%q) = %s; want value %v", tc.key, tc.value, out, tc.want)
			}
		})
	}
}
