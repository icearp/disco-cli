package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/securityhub"
	securityhubtypes "github.com/aws/aws-sdk-go-v2/service/securityhub/types"
)

func shStandardsHandler(standards ...securityhubtypes.Standard) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"DescribeStandards": func(any) (any, error) {
			return &securityhub.DescribeStandardsOutput{Standards: standards}, nil
		},
	}
}

func shStandard(arn, name string) securityhubtypes.Standard {
	return securityhubtypes.Standard{StandardsArn: ptrStr(arn), Name: ptrStr(name)}
}

// A standard's Region is its ARN's region segment, never the scanned region,
// so a region-less standard listed by every region leaves Region unset
// whichever region scans it last.
func TestScanSHStandards_RegionFromARN(t *testing.T) {
	const notARN = "cis-without-arn-shape"
	for _, tc := range []struct {
		name    string
		regions []string
	}{
		{"one region", []string{"us-east-1"}},
		{"two regions", []string{"us-east-1", "eu-west-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			for _, region := range tc.regions {
				client := shClient(shStandardsHandler(
					shStandard(shStdFSBP(region), "FSBP"),
					shStandard(shStdCIS, "CIS"),
					shStandard(notARN, "odd"),
				))
				total, _, err := scanSHStandards(context.Background(), client, acct, region, st, testScanID)
				if err != nil || total != 3 {
					t.Fatalf("%s: total = %d, err = %v; want 3 rows", region, total, err)
				}
			}
			rows := rowsByNativeID(t, st, TypeSecurityHubStandard)
			if len(rows) != len(tc.regions)+2 {
				t.Fatalf("rows = %d, want %d", len(rows), len(tc.regions)+2)
			}
			for _, region := range tc.regions {
				if got := sv(rows[shStdFSBP(region)].Region); got != region {
					t.Errorf("FSBP %s Region = %q, want %q", region, got, region)
				}
			}
			for _, id := range []string{shStdCIS, notARN} {
				if r := rows[id].Region; r != nil {
					t.Errorf("%s Region = %q, want unset", id, *r)
				}
			}
		})
	}
}

// A listed standard with no StandardsArn is not stored, and the phase says so
// once, however many there are.
func TestScanSHStandards_NoARNWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "securityhub:DescribeStandards")
	client := shClient(shStandardsHandler(
		securityhubtypes.Standard{Name: ptrStr("no-arn-1")},
		shStandard(shStdCIS, "CIS"),
		securityhubtypes.Standard{Name: ptrStr("no-arn-2")},
	))
	total, _, err := scanSHStandards(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 1 {
		t.Fatalf("total = %d, err = %v; want 1 row", total, err)
	}
	if n := len(rowsByNativeID(t, st, TypeSecurityHubStandard)); n != 1 {
		t.Errorf("rows = %d, want 1", n)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d, want 1", *warnings)
	}
}
