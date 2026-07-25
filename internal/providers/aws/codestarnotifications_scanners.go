package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/codestarnotifications"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCodeStarNotificationsNotificationRule, Service: "codestar-notifications", Upstream: "AWS::CodeStarNotifications::NotificationRule", Leaf: true})
	registerService(serviceEntry{
		name: "aws:codestar-notifications",
		fn:   scanCodeStarNotifications,
	})
}

// scanCodeStarNotifications discovers CodeStar Notifications notification rules.
func scanCodeStarNotifications(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := codestarnotifications.NewFromConfig(acct.cfg, func(o *codestarnotifications.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListNotificationRules(ctx, &codestarnotifications.ListNotificationRulesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "codestar-notifications:ListNotificationRules", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("codestar-notifications:ListNotificationRules: %w", err)
		}
		for _, n := range out.NotificationRules {
			arn := sv(n.Arn)
			if arn == "" {
				continue
			}
			label := sv(n.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCodeStarNotificationsNotificationRule, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(n), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "codestar-notifications notification-rules")
}
