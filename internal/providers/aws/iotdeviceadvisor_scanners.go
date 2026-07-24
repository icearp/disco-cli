package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/iotdeviceadvisor"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTDeviceAdvisorSuiteDefinition, Service: "iot-core-device-advisor", Upstream: "AWS::IoTCoreDeviceAdvisor::SuiteDefinition", Leaf: true})
	registerService(serviceEntry{
		name: "aws:iot-core-device-advisor",
		fn:   scanIoTDeviceAdvisor,
	})
}

// scanIoTDeviceAdvisor discovers IoT Core Device Advisor suite definitions.
// Synth ARN: arn:aws:iotdeviceadvisor:{r}:{a}:suitedefinition/{id}.
func scanIoTDeviceAdvisor(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := iotdeviceadvisor.NewFromConfig(acct.cfg, func(o *iotdeviceadvisor.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListSuiteDefinitions(ctx, &iotdeviceadvisor.ListSuiteDefinitionsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "iotdeviceadvisor:ListSuiteDefinitions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("iotdeviceadvisor:ListSuiteDefinitions: %w", err)
		}
		for _, s := range out.SuiteDefinitionInformationList {
			id := sv(s.SuiteDefinitionId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:iotdeviceadvisor:%s:%s:suitedefinition/%s", region, acct.ID, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeIoTDeviceAdvisorSuiteDefinition, NativeID: arn,
				Name: s.SuiteDefinitionName, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "iot-core-device-advisor suite-definitions")
}
