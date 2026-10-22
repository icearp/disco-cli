package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/mwaa"
	mwaatypes "github.com/aws/aws-sdk-go-v2/service/mwaa/types"
	"github.com/icearp/disco-cli/store"
)

var mwaaDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: airflow:GetEnvironment")

func mwaaTestARN(name string) string {
	return "arn:aws:airflow:us-east-1:123456789012:environment/" + name
}

// mwaaHandlers lists the environments "tagged" and "untagged" and answers
// GetEnvironment with getErr for "tagged" when it is non-nil.
func mwaaHandlers(getErr error) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListEnvironments": func(any) (any, error) {
			return &mwaa.ListEnvironmentsOutput{Environments: []string{"tagged", "untagged"}}, nil
		},
		"GetEnvironment": func(in any) (any, error) {
			name := sdkaws.ToString(in.(*mwaa.GetEnvironmentInput).Name)
			if name == "tagged" && getErr != nil {
				return nil, getErr
			}
			env := &mwaatypes.Environment{Arn: sdkaws.String(mwaaTestARN(name)), Name: sdkaws.String(name), Status: mwaatypes.EnvironmentStatusAvailable}
			if name == "tagged" {
				env.Tags = map[string]string{"env": "prod"}
			}
			return &mwaa.GetEnvironmentOutput{Environment: env}, nil
		},
	}
}

func runScanMWAA(st *store.Store, h map[string]func(any) (any, error)) (int, int, error) {
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, mwaaDenied)}
	return scanMWAA(context.Background(), acct, testRegion, st, testScanID)
}

func TestScanMWAA_ReadsTagsFromGetEnvironment(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := runScanMWAA(st, mwaaHandlers(nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeMWAAEnvironment)
	assertTagsMap(t, rows[mwaaTestARN("tagged")].TagsJSON, map[string]string{"env": "prod"})
	if got := rows[mwaaTestARN("untagged")].TagsJSON; got != nil {
		t.Errorf("untagged environment TagsJSON = %q, want nil", *got)
	}
}

// ListEnvironments returns only names, so an environment GetEnvironment
// fails for is never stored: a denial warns once, a vanished environment is
// skipped silently, and any other error, a throttle included, is returned
// after the other environment is stored.
func TestScanMWAA_GetEnvironmentFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantWarns int
		wantCode  string
	}{
		{"denied", mwaaDenied, 1, ""},
		{"throttled", apiErr("ThrottlingException", "slow down"), 0, "ThrottlingException"},
		{"gone", apiErr("ResourceNotFoundException", "no env"), 0, ""},
		{"other", apiErr("ValidationException", "bad"), 0, "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, mwaaGetEnvironmentOp)
			total, _, err := runScanMWAA(st, mwaaHandlers(tc.err))
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("GetEnvironment warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeMWAAEnvironment)
			if _, ok := rows[mwaaTestARN("untagged")]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %d, untagged stored = %v; want 1, 1, true", total, len(rows), ok)
			}
		})
	}
}

// An environment GetEnvironment returns without an ARN is warned about once
// and not stored.
func TestScanMWAA_NoARNWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, mwaaGetEnvironmentOp)
	h := mwaaHandlers(nil)
	get := h["GetEnvironment"]
	h["GetEnvironment"] = func(in any) (any, error) {
		out, err := get(in)
		if err != nil {
			return nil, err
		}
		if env := out.(*mwaa.GetEnvironmentOutput).Environment; sdkaws.ToString(env.Name) == "tagged" {
			env.Arn = nil
		}
		return out, nil
	}
	total, _, err := runScanMWAA(st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || *warns != 1 {
		t.Errorf("total = %d, GetEnvironment warnings = %d; want 1, 1", total, *warns)
	}
}
