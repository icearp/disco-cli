package aws

import (
	"context"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

// bpaOptionsStub answers DescribeVpcBlockPublicAccessOptions with a per-region
// payload. The embedded ec2API supplies the rest of the (wide) interface; any
// other call nil-panics, which is the intent — this exercises one scanner.
type bpaOptionsStub struct {
	ec2API
	byRegion map[string]ec2types.InternetGatewayBlockMode
	region   string
}

func (s *bpaOptionsStub) DescribeVpcBlockPublicAccessOptions(
	context.Context, *ec2.DescribeVpcBlockPublicAccessOptionsInput, ...func(*ec2.Options),
) (*ec2.DescribeVpcBlockPublicAccessOptionsOutput, error) {
	return &ec2.DescribeVpcBlockPublicAccessOptionsOutput{
		VpcBlockPublicAccessOptions: &ec2types.VpcBlockPublicAccessOptions{
			AwsAccountId:             sdkaws.String(testAccountID),
			AwsRegion:                sdkaws.String(s.region),
			InternetGatewayBlockMode: s.byRegion[s.region],
		},
	}, nil
}

// TestScanVPCBlockPublicAccessOptions_PerRegion pins that the account's VPC
// Block Public Access setting is recorded once per region.
//
// DescribeVpcBlockPublicAccessOptions is a per-region API and its payload
// carries AwsRegion, so the settings genuinely differ between regions. A
// region-less NativeID collapses them onto one natural key: each region's
// scanner then version-splits the previous one, and the surviving current row
// is whichever region committed last. That does not merely miscount — it
// reports a security control's state for a region that may have the opposite
// setting.
func TestScanVPCBlockPublicAccessOptions_PerRegion(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	scanID := testScanID

	modes := map[string]ec2types.InternetGatewayBlockMode{
		"us-east-1": ec2types.InternetGatewayBlockModeBlockBidirectional,
		"eu-west-1": ec2types.InternetGatewayBlockModeOff,
	}
	for region := range modes {
		client := &bpaOptionsStub{byRegion: modes, region: region}
		if _, _, err := scanVPCBlockPublicAccessOptions(
			context.Background(), client, acct, region, st, scanID); err != nil {
			t.Fatalf("scan %s: %v", region, err)
		}
	}

	got, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeEC2VPCBlockPublicAccessOptions},
		// The type is registered Managed, and ListResources hides managed rows
		// by default.
		IncludeManaged: true,
		Limit:          1000,
	})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(got) != len(modes) {
		t.Fatalf("got %d current rows, want %d — one per region; a shared NativeID "+
			"makes each region supersede the last", len(got), len(modes))
	}

	seen := map[string]bool{}
	for _, r := range got {
		if r.Region == nil {
			t.Fatalf("resource %s has no region", r.NativeID)
		}
		if seen[r.NativeID] {
			t.Errorf("NativeID %q reused across regions", r.NativeID)
		}
		seen[r.NativeID] = true
	}
}
