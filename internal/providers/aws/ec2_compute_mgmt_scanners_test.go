package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// A volume another account shares with the scanned one is stored under the
// scanned account, keyed by the owner's ARN; an own volume and one with no
// OwnerId keep the scanned account's ARN.
func TestScanVolumes_KeysByOwner(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	page := &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{
		{VolumeId: ptrStr("vol-shared"), OwnerId: ptrStr(iamTestPartnerAccount)},
		{VolumeId: ptrStr("vol-own"), OwnerId: ptrStr(testAccountID)},
		{VolumeId: ptrStr("vol-noowner")},
	}}
	client := ec2.NewFromConfig(cfgWithStub(stubResponses(t, map[string][]stubCall{
		"DescribeVolumes": {{Output: page}},
	}), testRegion))

	total, inserted, err := scanVolumes(context.Background(), client, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanVolumes: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("scanVolumes = (%d, %d), want (3, 3)", total, inserted)
	}

	rows := rowsByNativeID(t, st, TypeEC2Volume)
	want := []string{
		"arn:aws:ec2:" + testRegion + ":" + iamTestPartnerAccount + ":volume/vol-shared",
		"arn:aws:ec2:" + testRegion + ":" + testAccountID + ":volume/vol-own",
		"arn:aws:ec2:" + testRegion + ":" + testAccountID + ":volume/vol-noowner",
	}
	if len(rows) != len(want) {
		t.Fatalf("volume rows = %d %v, want %d", len(rows), rows, len(want))
	}
	for _, nativeID := range want {
		r, ok := rows[nativeID]
		if !ok {
			t.Errorf("no volume row %s", nativeID)
			continue
		}
		if r.AccountID != testAccountID || sv(r.Region) != testRegion {
			t.Errorf("%s: account %s region %s, want %s %s", nativeID, r.AccountID, sv(r.Region), testAccountID, testRegion)
		}
	}
}
