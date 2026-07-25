package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/ssmguiconnect"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSMGuiConnectPreferences, Service: "ssm-gui-connect", Upstream: "AWS::SSMGuiConnect::Preferences", Leaf: true})
	registerService(serviceEntry{
		name: "aws:ssm-gui-connect",
		fn:   scanSSMGuiConnect,
	})
}

// scanSSMGuiConnect captures the per-(account,region) SSM GUI Connect
// connection recording preferences (singleton). Synth ARN:
// arn:aws:ssm-guiconnect:{r}:{a}:preferences.
func scanSSMGuiConnect(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := ssmguiconnect.NewFromConfig(acct.cfg, func(o *ssmguiconnect.Options) { o.Region = region })

	out, err := client.GetConnectionRecordingPreferences(ctx, &ssmguiconnect.GetConnectionRecordingPreferencesInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "ssm-guiconnect:GetConnectionRecordingPreferences", acct.ID, region, err)
		}
		if isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("ssm-guiconnect:GetConnectionRecordingPreferences: %w", err)
	}
	if out.ConnectionRecordingPreferences == nil {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:ssm-guiconnect:%s:%s:preferences", region, acct.ID)
	label := acct.ID
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeSSMGuiConnectPreferences, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "ssm-gui-connect preferences")
}
