package aws

import (
	"context"
	"testing"
)

// Greengrass V2 makes no call in a region its SDK table omits, where AWS would
// answer with retried 429s, and does call in one the table lists.
func TestScanGreengrassV2_GatesOnSDKTable(t *testing.T) {
	for region, wantCalls := range map[string]bool{"eu-west-3": false, "us-gov-west-1": true} {
		t.Run(region, func(t *testing.T) {
			var regions parentCalls
			acct := testAccountIn("aws-us-gov")
			if region == "eu-west-3" {
				acct = testAccountIn("aws")
			}
			acct.cfg = recordCallRegions(opStubConfig(nil, apiErr("AccessDeniedException", "denied")), &regions)
			if _, _, err := scanGreengrassV2(context.Background(), acct, region, newTestStore(t), testScanID); err != nil {
				t.Fatalf("scanGreengrassV2(%s): %v", region, err)
			}
			if got := len(regions.distinct()) > 0; got != wantCalls {
				t.Errorf("scanGreengrassV2(%s) made calls = %v; want %v", region, got, wantCalls)
			}
		})
	}
}
