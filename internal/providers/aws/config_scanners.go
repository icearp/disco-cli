package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/configservice"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConfigRule, Service: "config", Upstream: "AWS::Config::ConfigRule"})
	registerType(restype.Descriptor{Type: TypeConfigRecorder, Service: "config", Upstream: "AWS::Config::ConfigurationRecorder"})
	registerType(restype.Descriptor{Type: TypeConfigDeliveryChannel, Service: "config", Upstream: "AWS::Config::DeliveryChannel"})
	registerType(restype.Descriptor{Type: TypeConfigAggregationAuthorization, Service: "config", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConfigConfigurationAggregator, Service: "config", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConfigConformancePack, Service: "config", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConfigOrganizationConfigRule, Service: "config", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConfigOrganizationConformancePack, Service: "config", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConfigRemediationConfiguration, Service: "config", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConfigStoredQuery, Service: "config", Leaf: true})
	registerService(serviceEntry{
		name: "aws:config",
		fn:   scanConfig,
	})
}

// configserviceAPI is the narrow set of AWS Config operations called by
// scanConfigAll.
type configserviceAPI interface {
	DescribeConfigurationRecorders(context.Context, *configservice.DescribeConfigurationRecordersInput, ...func(*configservice.Options)) (*configservice.DescribeConfigurationRecordersOutput, error)
	DescribeDeliveryChannels(context.Context, *configservice.DescribeDeliveryChannelsInput, ...func(*configservice.Options)) (*configservice.DescribeDeliveryChannelsOutput, error)
	DescribeConfigRules(context.Context, *configservice.DescribeConfigRulesInput, ...func(*configservice.Options)) (*configservice.DescribeConfigRulesOutput, error)
	DescribeAggregationAuthorizations(context.Context, *configservice.DescribeAggregationAuthorizationsInput, ...func(*configservice.Options)) (*configservice.DescribeAggregationAuthorizationsOutput, error)
	DescribeConfigurationAggregators(context.Context, *configservice.DescribeConfigurationAggregatorsInput, ...func(*configservice.Options)) (*configservice.DescribeConfigurationAggregatorsOutput, error)
	DescribeConformancePacks(context.Context, *configservice.DescribeConformancePacksInput, ...func(*configservice.Options)) (*configservice.DescribeConformancePacksOutput, error)
	DescribeOrganizationConfigRules(context.Context, *configservice.DescribeOrganizationConfigRulesInput, ...func(*configservice.Options)) (*configservice.DescribeOrganizationConfigRulesOutput, error)
	DescribeOrganizationConformancePacks(context.Context, *configservice.DescribeOrganizationConformancePacksInput, ...func(*configservice.Options)) (*configservice.DescribeOrganizationConformancePacksOutput, error)
	DescribeRemediationConfigurations(context.Context, *configservice.DescribeRemediationConfigurationsInput, ...func(*configservice.Options)) (*configservice.DescribeRemediationConfigurationsOutput, error)
	ListStoredQueries(context.Context, *configservice.ListStoredQueriesInput, ...func(*configservice.Options)) (*configservice.ListStoredQueriesOutput, error)
}

// scanConfig discovers AWS Config recorders, delivery channels, and rules.
// Recorders/delivery channels get synthesised ARNs (Describe APIs return
// none); rules carry ConfigRuleArn natively.
func scanConfig(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := configservice.NewFromConfig(acct.cfg, func(o *configservice.Options) { o.Region = region })
	t, i, ferr := scanConfigAll(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	t, i, ferr = scanConfigExtended(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

// scanConfigAll holds the testable scan body.
func scanConfigAll(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	// Configuration recorders.
	recOut, err := client.DescribeConfigurationRecorders(ctx, &configservice.DescribeConfigurationRecordersInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "config:DescribeConfigurationRecorders", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("config:DescribeConfigurationRecorders: %w", err)
	}
	var recBatch []*store.Resource
	for _, r := range recOut.ConfigurationRecorders {
		name := sv(r.Name)
		if name == "" {
			continue
		}
		arn := sv(r.Arn)
		if arn == "" {
			arn = fmt.Sprintf("arn:aws:config:%s:%s:config-recorder/%s", region, acct.ID, name)
		}
		recBatch = append(recBatch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConfigRecorder,
			NativeID:       arn,
			Name:           r.Name,
			Region:         &region,
			AttributesJSON: mustJSON(r),
			DiscoveredBy:   scanID,
		})
	}
	if len(recBatch) > 0 {
		n, err := st.UpsertResources(recBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Config recorders: %w", err)
		}
		total += len(recBatch)
		inserted += n
	}

	// Delivery channels.
	dcOut, err := client.DescribeDeliveryChannels(ctx, &configservice.DescribeDeliveryChannelsInput{})
	if err == nil {
		var dcBatch []*store.Resource
		for _, d := range dcOut.DeliveryChannels {
			name := sv(d.Name)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:config:%s:%s:delivery-channel/%s", region, acct.ID, name)
			dcBatch = append(dcBatch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeConfigDeliveryChannel,
				NativeID:       arn,
				Name:           d.Name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
		}
		if len(dcBatch) > 0 {
			n, err := st.UpsertResources(dcBatch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert Config delivery channels: %w", err)
			}
			total += len(dcBatch)
			inserted += n
		}
	} else if !isAccessDenied(err) {
		return 0, 0, fmt.Errorf("config:DescribeDeliveryChannels: %w", err)
	}

	// Config rules (paginated).
	var ruleBatch []*store.Resource
	rulePager := configservice.NewDescribeConfigRulesPaginator(client, &configservice.DescribeConfigRulesInput{})
	for rulePager.HasMorePages() {
		page, err := rulePager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				break
			}
			return 0, 0, fmt.Errorf("config:DescribeConfigRules: %w", err)
		}
		for _, r := range page.ConfigRules {
			arn := sv(r.ConfigRuleArn)
			if arn == "" {
				continue
			}
			status := string(r.ConfigRuleState)
			ruleBatch = append(ruleBatch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeConfigRule,
				NativeID:       arn,
				Name:           r.ConfigRuleName,
				Region:         &region,
				Status:         &status,
				AttributesJSON: mustJSON(r),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(ruleBatch) > 0 {
		n, err := st.UpsertResources(ruleBatch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert Config rules: %w", err)
		}
		total += len(ruleBatch)
		inserted += n
	}

	return total, inserted, nil
}
