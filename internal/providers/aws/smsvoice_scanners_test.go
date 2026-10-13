package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2"
	svtypes "github.com/aws/aws-sdk-go-v2/service/pinpointsmsvoicev2/types"
	"github.com/icearp/disco-cli/store"
)

var svCreated = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func svARN(kind, id string) string {
	return fmt.Sprintf("arn:aws:sms-voice:%s:%s:%s/%s", testRegion, testAccountID, kind, id)
}

func svNotifyConfig(id string) svtypes.NotifyConfigurationInformation {
	return svtypes.NotifyConfigurationInformation{
		NotifyConfigurationArn: sdkaws.String(svARN("notify-configuration", id)), NotifyConfigurationId: sdkaws.String(id),
		DisplayName: sdkaws.String("disp-" + id), Status: svtypes.NotifyConfigurationStatusActive,
		CreatedTimestamp: &svCreated, PoolId: sdkaws.String("pool-1"),
	}
}

func svRcsAgent(id string) svtypes.RcsAgentInformation {
	return svtypes.RcsAgentInformation{
		RcsAgentArn: sdkaws.String(svARN("rcs-agent", id)), RcsAgentId: sdkaws.String(id),
		Status: svtypes.RcsAgentStatusTesting, CreatedTimestamp: &svCreated, PoolId: sdkaws.String("pool-1"),
	}
}

// svNewOps drives each region-wide sub-scanner through one shape: page builds
// a page whose elements are the given ids ("" = an element without an ARN).
var svNewOps = []struct {
	name, op, sdkOp, rowType, kind, wantStatus, attrKey string
	wantName                                            func(id string) string
	page                                                func(next *string, ids ...string) any
	scan                                                func(context.Context, smsVoiceAPI, *account, string, *store.Store, string) (int, int, error)
}{
	{
		name: "notify-configurations", op: "sms-voice:DescribeNotifyConfigurations", sdkOp: "DescribeNotifyConfigurations",
		rowType: TypeSMSVoiceNotifyConfiguration, kind: "notify-configuration", wantStatus: "ACTIVE", attrKey: "PoolId",
		wantName: func(id string) string { return "disp-" + id },
		page: func(next *string, ids ...string) any {
			out := &pinpointsmsvoicev2.DescribeNotifyConfigurationsOutput{NextToken: next}
			for _, id := range ids {
				if id == "" {
					out.NotifyConfigurations = append(out.NotifyConfigurations, svtypes.NotifyConfigurationInformation{DisplayName: sdkaws.String("no-arn")})
					continue
				}
				out.NotifyConfigurations = append(out.NotifyConfigurations, svNotifyConfig(id))
			}
			return out
		},
		scan: scanSVNotifyConfigurations,
	},
	{
		name: "rcs-agents", op: "sms-voice:DescribeRcsAgents", sdkOp: "DescribeRcsAgents",
		rowType: TypeSMSVoiceRcsAgent, kind: "rcs-agent", wantStatus: "TESTING", attrKey: "PoolId",
		wantName: func(id string) string { return id },
		page: func(next *string, ids ...string) any {
			out := &pinpointsmsvoicev2.DescribeRcsAgentsOutput{NextToken: next}
			for _, id := range ids {
				if id == "" {
					out.RcsAgents = append(out.RcsAgents, svtypes.RcsAgentInformation{RcsAgentId: sdkaws.String("no-arn")})
					continue
				}
				out.RcsAgents = append(out.RcsAgents, svRcsAgent(id))
			}
			return out
		},
		scan: scanSVRcsAgents,
	},
}

func svClient(t *testing.T, calls map[string][]stubCall) *pinpointsmsvoicev2.Client {
	t.Helper()
	return pinpointsmsvoicev2.NewFromConfig(cloud9CfgWithStub(stubResponses(t, calls), testRegion))
}

func TestScanSVNewPhases_PaginatesAndStoresRows(t *testing.T) {
	for _, op := range svNewOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			client := svClient(t, map[string][]stubCall{op.sdkOp: {
				{Output: op.page(sdkaws.String("p2"), "a", "")},
				{Output: op.page(nil, "b")},
			}})
			total, _, err := op.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := qsRows(t, st, op.rowType)
			if total != 2 || len(rows) != 2 {
				t.Fatalf("total = %d, stored %d rows; want 2 and 2 (the ARN-less element skipped)", total, len(rows))
			}
			for _, id := range []string{"a", "b"} {
				r, ok := rows[svARN(op.kind, id)]
				if !ok {
					t.Fatalf("no row with NativeID %s; got %v", svARN(op.kind, id), rows)
				}
				if sv(r.Region) != testRegion || sv(r.Name) != op.wantName(id) || sv(r.Status) != op.wantStatus {
					t.Errorf("row %s: region=%q name=%q status=%q; want %q %q %q", id, sv(r.Region), sv(r.Name), sv(r.Status), testRegion, op.wantName(id), op.wantStatus)
				}
				if got, err := time.Parse(time.RFC3339, sv(r.CreatedAt)); err != nil || !got.Equal(svCreated) {
					t.Errorf("row %s: CreatedAt = %q; want %s", id, sv(r.CreatedAt), svCreated)
				}
				var attrs map[string]any
				if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[op.attrKey] != "pool-1" {
					t.Errorf("row %s: attrs[%s] = %v (err %v); want pool-1", id, op.attrKey, attrs[op.attrKey], err)
				}
			}
		})
	}
}

func TestScanSVNewPhases_ErrorHandling(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"empty result", nil, false, 0},
		{"access denied warns and returns nil", apiErr("AccessDeniedException", "User: x is not authorized to perform: sms-voice:Describe"), false, 1},
		{"other error propagates", apiErr("InternalServerException", "boom"), true, 0},
	}
	for _, op := range svNewOps {
		for _, tc := range cases {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				var warned []string
				st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
				call := stubCall{Err: tc.err}
				if tc.err == nil {
					call.Output = op.page(nil)
				}
				client := svClient(t, map[string][]stubCall{op.sdkOp: {call}})
				total, _, err := op.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
				if tc.wantErr {
					if !isAPIErrorCode(err, "InternalServerException") {
						t.Fatalf("err = %v; want the InternalServerException propagated", err)
					}
				} else if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
				if got := len(qsRows(t, st, op.rowType)); total != 0 || got != 0 {
					t.Errorf("total = %d, stored %d rows; want 0 and 0", total, got)
				}
				if len(warned) != tc.wantWarns {
					t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
				}
				for _, svc := range warned {
					if svc != op.op {
						t.Errorf("warning names %q; want %q", svc, op.op)
					}
				}
			})
		}
	}
}

var svDestNumberARN = svARN("verified-destination-number", "vdn-1")

// svServiceStub serves every op scanSMSVoice calls: one verified destination
// number (the last pre-existing phase), one row of each new kind, and override
// replacing an op's queue.
func svServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	calls := map[string][]stubCall{
		"DescribeConfigurationSets":       one(&pinpointsmsvoicev2.DescribeConfigurationSetsOutput{}),
		"DescribeOptOutLists":             one(&pinpointsmsvoicev2.DescribeOptOutListsOutput{}),
		"DescribePhoneNumbers":            one(&pinpointsmsvoicev2.DescribePhoneNumbersOutput{}),
		"DescribePools":                   one(&pinpointsmsvoicev2.DescribePoolsOutput{}),
		"DescribeProtectConfigurations":   one(&pinpointsmsvoicev2.DescribeProtectConfigurationsOutput{}),
		"DescribeSenderIds":               one(&pinpointsmsvoicev2.DescribeSenderIdsOutput{}),
		"DescribeRegistrations":           one(&pinpointsmsvoicev2.DescribeRegistrationsOutput{}),
		"DescribeRegistrationAttachments": one(&pinpointsmsvoicev2.DescribeRegistrationAttachmentsOutput{}),
		"DescribeVerifiedDestinationNumbers": one(&pinpointsmsvoicev2.DescribeVerifiedDestinationNumbersOutput{
			VerifiedDestinationNumbers: []svtypes.VerifiedDestinationNumberInformation{{
				VerifiedDestinationNumberArn: sdkaws.String(svDestNumberARN), VerifiedDestinationNumberId: sdkaws.String("vdn-1"),
			}},
		}),
	}
	for _, op := range svNewOps {
		calls[op.sdkOp] = one(op.page(nil, "x"))
	}
	for op, c := range override {
		calls[op] = c
	}
	return cloud9CfgWithStub(stubResponses(t, calls), testRegion)
}

func TestScanSMSVoice_StoresNewPhaseRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = svServiceStub(t, nil)
	total, _, err := scanSMSVoice(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSMSVoice: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3 (destination number, notify configuration, rcs agent)", total)
	}
	for _, op := range svNewOps {
		if _, ok := qsRows(t, st, op.rowType)[svARN(op.kind, "x")]; !ok {
			t.Errorf("no %s row stored by the service scan", op.rowType)
		}
	}
}

// TestScanSMSVoice_NewPhaseErrorPropagates pins that each new phase's hard
// error reaches the service result and that every pre-existing phase,
// including the last (verified destination numbers), ran before it.
func TestScanSMSVoice_NewPhaseErrorPropagates(t *testing.T) {
	for _, op := range svNewOps {
		t.Run(op.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = svServiceStub(t, map[string][]stubCall{op.sdkOp: {{Err: apiErr("InternalServerException", "boom")}}})
			_, _, err := scanSMSVoice(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "InternalServerException") {
				t.Fatalf("err = %v; want the %s InternalServerException", err, op.sdkOp)
			}
			if _, ok := qsRows(t, st, TypeSMSVoiceVerifiedDestinationNumber)[svDestNumberARN]; !ok {
				t.Errorf("verified-destination-number phase did not run before the failing %s", op.sdkOp)
			}
		})
	}
}
