package aws

import (
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/icearp/disco-cli/store"
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

// A ListScramSecrets listing that ends after its first page, with an error or
// a repeated token, keeps the secrets that page listed and returns the error.
func TestScanKafka_ScramLaterPageFailureKeepsEarlierPages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		page2   func() (any, error)
		wantErr func(*testing.T, error)
	}{
		{
			"error",
			func() (any, error) { return nil, kafkaUnexpected },
			func(t *testing.T, err error) { assertAPICode(t, err, "ConflictException") },
		},
		{
			"repeated token",
			func() (any, error) { return &kafka.ListScramSecretsOutput{NextToken: sdkaws.String("page-2")}, nil },
			func(t *testing.T, err error) {
				if !errors.Is(err, errRepeatedNextToken) {
					t.Errorf("err = %v, want errRepeatedNextToken", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := map[string]*parentCalls{}
			h := kafkaHandlers(calls)
			var later atomic.Int32
			h["ListScramSecrets"] = func(in any) (any, error) {
				p := in.(*kafka.ListScramSecretsInput)
				arn := sdkaws.ToString(p.ClusterArn)
				calls["ListScramSecrets"].add(arn)
				switch {
				case arn != kafkaTestClusterA:
					return &kafka.ListScramSecretsOutput{SecretArnList: []string{kafkaTestScramSecret}}, nil
				case p.NextToken == nil:
					return &kafka.ListScramSecretsOutput{SecretArnList: []string{kafkaTestScramSecret}, NextToken: sdkaws.String("page-2")}, nil
				case later.Add(1) > 2:
					return nil, kafkaGone // a stop that did not happen
				default:
					return tc.page2()
				}
			}
			_, err := scanKafkaTopLevel(t, st, h)
			tc.wantErr(t, err)
			if n := later.Load(); n != 1 {
				t.Errorf("later ListScramSecrets calls for %s = %d, want 1", kafkaTestClusterA, n)
			}
			// The siblings are not asserted: the error cancels those not yet run.
			secrets := rowsByNativeID(t, st, TypeMSKBatchScramSecret)
			if _, ok := secrets[kafkaTestClusterA+"/batch-scram-secret/"+kafkaTestScramSecret]; !ok {
				t.Errorf("first-page scram secret of %s not stored; stored %d", kafkaTestClusterA, len(secrets))
			}
		})
	}
}

// kafkaScramSecondPage serves ListScramSecrets with one secret per cluster;
// cluster A's listing continues to a second page that fails with err.
func kafkaScramSecondPage(calls map[string]*parentCalls, err error) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*kafka.ListScramSecretsInput)
		arn := sdkaws.ToString(p.ClusterArn)
		calls["ListScramSecrets"].add(arn)
		switch {
		case arn != kafkaTestClusterA:
			return &kafka.ListScramSecretsOutput{SecretArnList: []string{kafkaTestScramSecret}}, nil
		case p.NextToken == nil:
			return &kafka.ListScramSecretsOutput{SecretArnList: []string{kafkaTestScramSecret}, NextToken: sdkaws.String("page-2")}, nil
		default:
			return nil, err
		}
	}
}

// A cluster deleted while its SCRAM secrets were listed (NotFoundException,
// in the op's model) took its associations with it: the rows of its earlier
// pages are not stored, and the scan succeeds.
func TestScanKafka_ScramClusterGoneMidListingDropsItsRows(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := kafkaHandlers(calls)
	h["ListScramSecrets"] = kafkaScramSecondPage(calls, kafkaGone)
	if _, err := scanKafkaTopLevel(t, st, h); err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	secrets := rowsByNativeID(t, st, TypeMSKBatchScramSecret)
	if _, ok := secrets[kafkaTestClusterA+"/batch-scram-secret/"+kafkaTestScramSecret]; ok {
		t.Errorf("scram secret of the deleted %s stored", kafkaTestClusterA)
	}
	if _, ok := secrets[kafkaTestClusterX+"/batch-scram-secret/"+kafkaTestScramSecret]; !ok {
		t.Errorf("scram secret of %s not stored", kafkaTestClusterX)
	}
}

// A cluster whose SCRAM listing fails transiently after its first page is
// warned about once, as partially read, and only after its first page is
// stored.
func TestScanKafka_ScramPartialReadWarnsAfterStore(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	h := kafkaHandlers(calls)
	h["ListScramSecrets"] = kafkaScramSecondPage(calls, kafkaThrottled)
	var warned []string
	storedAtWarning := -1
	st.OnWarn = func(w store.ScanWarning) {
		if w.Service != "kafka:ListScramSecrets" {
			return
		}
		warned = append(warned, w.Message)
		storedAtWarning = len(rowsByNativeID(t, st, TypeMSKBatchScramSecret))
	}
	if _, err := scanKafkaTopLevel(t, st, h); err != nil {
		t.Fatalf("scanKafka: %v", err)
	}
	// The message is the contract here: "skipped" would deny the stored page.
	if len(warned) != 1 || !strings.Contains(warned[0], "partially read") {
		t.Fatalf("ListScramSecrets warnings = %q, want one saying the cluster was partially read", warned)
	}
	if want := len(rowsByNativeID(t, st, TypeMSKBatchScramSecret)); storedAtWarning != want || want == 0 {
		t.Errorf("rows stored when warned = %d, want all %d, stored first", storedAtWarning, want)
	}
}
