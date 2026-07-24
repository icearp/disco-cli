package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/pinpoint"
)

// scanPinpointTemplates lists all templates via ListTemplates (single
// account-scoped op covering Email/InApp/Push/Sms/Voice). Per-row
// TemplateType picks the disco type.
func scanPinpointTemplates(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var token *string
	for {
		out, err := client.ListTemplates(ctx, &pinpoint.ListTemplatesInput{NextToken: token})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pinpoint:ListTemplates", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pinpoint:ListTemplates: %w", err)
		}
		if out.TemplatesResponse == nil {
			break
		}
		for _, t := range out.TemplatesResponse.Item {
			arn := sv(t.Arn)
			name := sv(t.TemplateName)
			if arn == "" || name == "" {
				continue
			}
			var dtype string
			switch string(t.TemplateType) {
			case "EMAIL":
				dtype = TypePinpointEmailTemplate
			case "INAPP":
				dtype = TypePinpointInAppTemplate
			case "PUSH":
				dtype = TypePinpointPushTemplate
			case "SMS":
				dtype = TypePinpointSmsTemplate
			case "VOICE":
				continue // VoiceTemplate is not a CFN resource — skip
			default:
				continue
			}
			n := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: dtype, NativeID: arn,
				Name: &n, Region: &region, AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
		if out.TemplatesResponse.NextToken == nil || *out.TemplatesResponse.NextToken == "" {
			break
		}
		token = out.TemplatesResponse.NextToken
	}
	return upsertBatch(st, batch, "pinpoint templates")
}
