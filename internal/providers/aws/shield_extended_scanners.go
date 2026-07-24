package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/shield"
)

// scanShieldDRTAccess captures the SRT (DDoS Response Team) access config
// as a per-account singleton. Synth ARN: arn:aws:shield::{a}:drt-access.
func scanShieldDRTAccess(ctx context.Context, client shieldAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeDRTAccess(ctx, &shield.DescribeDRTAccessInput{})
	if err != nil {
		if isAccessDenied(err) || isShieldNotSubscribed(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("shield:DescribeDRTAccess: %w", err)
	}
	if sv(out.RoleArn) == "" && len(out.LogBucketList) == 0 {
		return 0, 0, nil
	}
	region := "us-east-1"
	arn := fmt.Sprintf("arn:aws:shield::%s:drt-access", acct.ID)
	label := acct.ID
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeShieldDRTAccess, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "shield drt-access")
}

// scanShieldProactiveEngagement captures the proactive-engagement
// emergency-contact list as a per-account singleton. Synth ARN:
// arn:aws:shield::{a}:proactive-engagement.
func scanShieldProactiveEngagement(ctx context.Context, client shieldAPI, acct *account, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeEmergencyContactSettings(ctx, &shield.DescribeEmergencyContactSettingsInput{})
	if err != nil {
		if isAccessDenied(err) || isShieldNotSubscribed(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("shield:DescribeEmergencyContactSettings: %w", err)
	}
	if len(out.EmergencyContactList) == 0 {
		return 0, 0, nil
	}
	region := "us-east-1"
	arn := fmt.Sprintf("arn:aws:shield::%s:proactive-engagement", acct.ID)
	label := acct.ID
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeShieldProactiveEngagement, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "shield proactive-engagement")
}
