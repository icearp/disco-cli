package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/iot"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTLogging, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTResourceSpecificLogging, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTEncryptionConfiguration, Service: "iot", Untaggable: true})
}

type iotLoggingAPI interface {
	GetV2LoggingOptions(context.Context, *iot.GetV2LoggingOptionsInput, ...func(*iot.Options)) (*iot.GetV2LoggingOptionsOutput, error)
	ListV2LoggingLevels(context.Context, *iot.ListV2LoggingLevelsInput, ...func(*iot.Options)) (*iot.ListV2LoggingLevelsOutput, error)
	DescribeEncryptionConfiguration(context.Context, *iot.DescribeEncryptionConfigurationInput, ...func(*iot.Options)) (*iot.DescribeEncryptionConfigurationOutput, error)
}

// scanIoTLogging runs the logging phases, adding each to p, and reports
// whether a write failed.
func scanIoTLogging(ctx context.Context, client iotLoggingAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	return runIoTPhases(p,
		iotCounted("iot:GetV2LoggingOptions", func() (int, int, error) {
			return scanIoTLoggingOptions(ctx, client, acct, region, st, scanID)
		}),
		iotCounted("iot:ListV2LoggingLevels", func() (int, int, error) {
			return scanIoTResourceSpecificLogging(ctx, client, acct, region, st, scanID)
		}),
		iotCounted("iot:DescribeEncryptionConfiguration", func() (int, int, error) {
			return scanIoTEncryptionConfiguration(ctx, client, acct, region, st, scanID)
		}),
	)
}

// scanIoTLoggingOptions emits one row per region — V2 logging is a
// per-region singleton (DefaultLogLevel + RoleArn).
func scanIoTLoggingOptions(ctx context.Context, client iotLoggingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetV2LoggingOptions(ctx, &iot.GetV2LoggingOptionsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "iot:GetV2LoggingOptions", acct.ID, region, err)
		}
		// NotConfiguredException = SetV2LoggingOptions never called for this
		// account/region (default state); treat as no-op.
		if isAPIErrorCode(err, "NotConfiguredException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("iot:GetV2LoggingOptions: %w", err)
	}
	arn := arnFor(acct.partition(), "iot", region, acct.ID, "logging")
	name := "logging"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeIoTLogging,
		NativeID:       arn,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert iot logging: %w", uerr)
	}
	return 1, n, nil
}

// scanIoTResourceSpecificLogging emits one row per (target type, target name)
// pair returned by ListV2LoggingLevels.
func scanIoTResourceSpecificLogging(ctx context.Context, client iotLoggingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListV2LoggingLevelsPaginator(client, &iot.ListV2LoggingLevelsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListV2LoggingLevels", acct.ID, region, perr)
				return 0, 0, nil
			}
			// NotConfiguredException = SetV2LoggingOptions never called
			// (default state). No levels to enumerate.
			if isAPIErrorCode(perr, "NotConfiguredException") {
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListV2LoggingLevels: %w", perr)
		}
		for _, l := range out.LogTargetConfigurations {
			if l.LogTarget == nil {
				continue
			}
			tt := string(l.LogTarget.TargetType)
			tn := sv(l.LogTarget.TargetName)
			if tt == "" {
				continue
			}
			arn := arnFor(acct.partition(), "iot", region, acct.ID, "resource-specific-logging/"+tt+"/"+tn)
			label := fmt.Sprintf("%s:%s", tt, tn)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTResourceSpecificLogging,
				NativeID:       arn,
				Name:           &label,
				Region:         &region,
				AttributesJSON: mustJSON(l),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert iot resource-specific-logging: %w", uerr)
	}
	return len(batch), n, nil
}

// scanIoTEncryptionConfiguration emits one row per region — encryption
// config is a per-region singleton.
func scanIoTEncryptionConfiguration(ctx context.Context, client iotLoggingAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeEncryptionConfiguration(ctx, &iot.DescribeEncryptionConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "iot:DescribeEncryptionConfiguration", acct.ID, region, err)
		}
		return 0, 0, fmt.Errorf("iot:DescribeEncryptionConfiguration: %w", err)
	}
	arn := arnFor(acct.partition(), "iot", region, acct.ID, "encryption-configuration")
	name := "encryption-configuration"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeIoTEncryptionConfiguration,
		NativeID:       arn,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert iot encryption-configuration: %w", uerr)
	}
	return 1, n, nil
}
