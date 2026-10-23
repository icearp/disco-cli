package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/pinpoint"
	pptypes "github.com/aws/aws-sdk-go-v2/service/pinpoint/types"
	"github.com/icearp/disco-cli/store"
)

// pinpointTemplateTypes maps each TemplateType disco stores to its type.
var pinpointTemplateTypes = map[pptypes.TemplateType]string{
	pptypes.TemplateTypeEmail: TypePinpointEmailTemplate,
	pptypes.TemplateTypeInapp: TypePinpointInAppTemplate,
	pptypes.TemplateTypePush:  TypePinpointPushTemplate,
	pptypes.TemplateTypeSms:   TypePinpointSmsTemplate,
	pptypes.TemplateTypeVoice: TypePinpointVoiceTemplate,
}

// scanPinpointTemplates stores the region's message templates from
// ListTemplates, typed by TemplateType. A TemplateType disco does not know is
// warned about. The listed TemplateResponse carries no tags (its model says
// only the Get*Template ops return them), so none are stored.
func scanPinpointTemplates(ctx context.Context, client pinpointAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return storeTokenList(ctx, st, acct, region, pinpointTemplatesOp,
		func(ctx context.Context, token *string) ([]pptypes.TemplateResponse, *string, error) {
			out, err := client.ListTemplates(ctx, &pinpoint.ListTemplatesInput{NextToken: token})
			if err != nil || out.TemplatesResponse == nil {
				return nil, nil, err
			}
			return out.TemplatesResponse.Item, out.TemplatesResponse.NextToken, nil
		},
		func(t pptypes.TemplateResponse) (*store.Resource, string, string) {
			rtype, ok := pinpointTemplateTypes[t.TemplateType]
			if !ok {
				return nil, "templates have a TemplateType disco declares no type for", sv(t.TemplateName) + " (" + string(t.TemplateType) + ")"
			}
			return pinpointListedRow(acct, region, scanID, rtype, "templates", t.Arn, t.TemplateName, t.TemplateName, t, nil)
		})
}
