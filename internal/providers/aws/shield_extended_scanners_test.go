package aws

import (
	"context"
	"errors"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/shield"
	shieldtypes "github.com/aws/aws-sdk-go-v2/service/shield/types"
	"github.com/icearp/disco-cli/store"
)

// The Shield singletons record the region the scanner was called with (Shield's
// home in the account's partition), not a fixed commercial region.
func TestShieldSingletons_RecordCallerRegion(t *testing.T) {
	cfg := opStubConfig(map[string]func(any) (any, error){
		"DescribeDRTAccess": func(any) (any, error) {
			return &shield.DescribeDRTAccessOutput{RoleArn: sdkaws.String("arn:aws-us-gov:iam::111111111111:role/drt")}, nil
		},
		"DescribeEmergencyContactSettings": func(any) (any, error) {
			return &shield.DescribeEmergencyContactSettingsOutput{EmergencyContactList: []shieldtypes.EmergencyContact{
				{EmailAddress: sdkaws.String("ops@example.com")},
			}}, nil
		},
	}, errors.New("unexpected call"))
	client := shield.NewFromConfig(cfg)
	st := newTestStore(t)
	acct := testAccountIn("aws-us-gov")
	const region = "us-gov-west-1"

	if _, _, err := scanShieldDRTAccess(context.Background(), client, acct, region, st, testScanID); err != nil {
		t.Fatalf("scanShieldDRTAccess: %v", err)
	}
	if _, _, err := scanShieldProactiveEngagement(context.Background(), client, acct, region, st, testScanID); err != nil {
		t.Fatalf("scanShieldProactiveEngagement: %v", err)
	}
	for _, typ := range []string{TypeShieldDRTAccess, TypeShieldProactiveEngagement} {
		rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ}, Limit: 10})
		if err != nil {
			t.Fatalf("ListResources(%s): %v", typ, err)
		}
		if len(rows) != 1 || rows[0].Region == nil || *rows[0].Region != region {
			t.Errorf("%s rows = %+v; want one row in %s", typ, rows, region)
		}
	}
}
