package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/notificationscontacts"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:notifications-contacts",
		global: true,
		fn:     scanNotificationsContacts,
		emits: []coverage.TypeDecl{
			{Service: "notifications-contacts", DiscoType: TypeNotificationsContactsEmailContact, Leaf: true},
		},
	})
}

// scanNotificationsContacts discovers AWS User Notifications Contacts email
// contacts. Service is global; gate to us-east-1 to avoid duplicate scans
// across regions.
func scanNotificationsContacts(ctx context.Context, acct *account, _ string, st *store.Store, scanID string) (total, inserted int, err error) {
	region := "us-east-1"
	client := notificationscontacts.NewFromConfig(acct.cfg, func(o *notificationscontacts.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListEmailContacts(ctx, &notificationscontacts.ListEmailContactsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "notifications-contacts:ListEmailContacts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("notifications-contacts:ListEmailContacts: %w", err)
		}
		for _, e := range out.EmailContacts {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeNotificationsContactsEmailContact, NativeID: arn,
				Name: e.Name, Region: regionGlobal, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "notifications-contacts email-contacts")
}
