package aws

import (
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
)

const kafkaTestScramSecret = "arn:aws:secretsmanager:us-east-1:123456789012:secret:AmazonMSK_x-AbCdEf"

// A per-cluster GetClusterPolicy or ListScramSecrets failure costs only the
// failing clusters: NotFoundException (modelled for both ops) is skipped
// silently; a denial, a transient error or a rejection is skipped with one
// warning however many clusters hit it; the other clusters are still scanned.
// Any other code fails the scan, naming the cluster.
func TestScanKafka_PerClusterPhaseErrors(t *testing.T) {
	both := []string{kafkaTestClusterA, kafkaTestClusterX}
	onlyA := []string{kafkaTestClusterA}
	for _, tc := range []struct {
		name      string
		op        string
		opLabel   string
		failed    []string // clusters whose call fails with err
		err       error
		wantCode  string // "" means the scan returns nil
		wantWarns int
	}{
		{"policy not found", "GetClusterPolicy", "kafka:GetClusterPolicy", onlyA, kafkaGone, "", 0},
		{"policy denied", "GetClusterPolicy", "kafka:GetClusterPolicy", onlyA, kafkaDenied, "", 1},
		{"policy denied twice", "GetClusterPolicy", "kafka:GetClusterPolicy", both, kafkaDenied, "", 1},
		{"policy throttled", "GetClusterPolicy", "kafka:GetClusterPolicy", onlyA, kafkaThrottled, "", 1},
		{"policy rejected twice", "GetClusterPolicy", "kafka:GetClusterPolicy", both, kafkaBadRequest, "", 1},
		{"policy other", "GetClusterPolicy", "kafka:GetClusterPolicy", onlyA, kafkaUnexpected, "ConflictException", 0},
		{"scram not found", "ListScramSecrets", "kafka:ListScramSecrets", onlyA, kafkaGone, "", 0},
		{"scram denied", "ListScramSecrets", "kafka:ListScramSecrets", onlyA, kafkaDenied, "", 1},
		{"scram denied twice", "ListScramSecrets", "kafka:ListScramSecrets", both, kafkaDenied, "", 1},
		{"scram throttled", "ListScramSecrets", "kafka:ListScramSecrets", onlyA, kafkaThrottled, "", 1},
		{"scram rejected twice", "ListScramSecrets", "kafka:ListScramSecrets", both, kafkaBadRequest, "", 1},
		{"scram other", "ListScramSecrets", "kafka:ListScramSecrets", onlyA, kafkaUnexpected, "ConflictException", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.opLabel)
			calls := map[string]*parentCalls{}
			h := kafkaHandlers(calls)
			h["GetClusterPolicy"] = func(in any) (any, error) {
				arn := sdkaws.ToString(in.(*kafka.GetClusterPolicyInput).ClusterArn)
				calls["GetClusterPolicy"].add(arn)
				if tc.op == "GetClusterPolicy" && slices.Contains(tc.failed, arn) {
					return nil, tc.err
				}
				return &kafka.GetClusterPolicyOutput{Policy: sdkaws.String(`{"Version":"2012-10-17"}`)}, nil
			}
			h["ListScramSecrets"] = func(in any) (any, error) {
				arn := sdkaws.ToString(in.(*kafka.ListScramSecretsInput).ClusterArn)
				calls["ListScramSecrets"].add(arn)
				if tc.op == "ListScramSecrets" && slices.Contains(tc.failed, arn) {
					return nil, tc.err
				}
				return &kafka.ListScramSecretsOutput{SecretArnList: []string{kafkaTestScramSecret}}, nil
			}
			_, err := scanKafkaTopLevel(t, st, h)
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
				if err != nil && !strings.Contains(err.Error(), kafkaTestClusterA) {
					t.Errorf("err = %v, want it to name cluster %s", err, kafkaTestClusterA)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			policies := rowsByNativeID(t, st, TypeMSKClusterPolicy)
			secrets := rowsByNativeID(t, st, TypeMSKBatchScramSecret)
			for _, cluster := range []string{kafkaTestClusterA, kafkaTestClusterX, kafkaTestClusterS} {
				failed := slices.Contains(tc.failed, cluster)
				policyFailed := failed && tc.op == "GetClusterPolicy"
				scramFailed := failed && tc.op == "ListScramSecrets"
				_, got := policies[cluster+"/cluster-policy"]
				if want := !policyFailed; got != want {
					t.Errorf("policy row for %s present = %v, want %v", cluster, got, want)
				}
				_, got = secrets[cluster+"/batch-scram-secret/"+kafkaTestScramSecret]
				if want := !scramFailed; got != want {
					t.Errorf("scram secret row for %s present = %v, want %v", cluster, got, want)
				}
			}
		})
	}
}
