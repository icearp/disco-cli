package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTLogging, Leaf: true},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTResourceSpecificLogging, Leaf: true},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTEncryptionConfiguration, Leaf: true},
	)
}

type iotLoggingAPI interface {
	GetV2LoggingOptions(context.Context, *iot.GetV2LoggingOptionsInput, ...func(*iot.Options)) (*iot.GetV2LoggingOptionsOutput, error)
	ListV2LoggingLevels(context.Context, *iot.ListV2LoggingLevelsInput, ...func(*iot.Options)) (*iot.ListV2LoggingLevelsOutput, error)
	DescribeEncryptionConfiguration(context.Context, *iot.DescribeEncryptionConfigurationInput, ...func(*iot.Options)) (*iot.DescribeEncryptionConfigurationOutput, error)
}

func scanIoTLogging(ctx context.Context, client iotLoggingAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTLoggingOptions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTResourceSpecificLogging(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanIoTEncryptionConfiguration(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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
	arn := fmt.Sprintf("arn:aws:iot:%s:%s:logging", region, acct.ID)
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
			arn := fmt.Sprintf("arn:aws:iot:%s:%s:resource-specific-logging/%s/%s", region, acct.ID, tt, tn)
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
	arn := fmt.Sprintf("arn:aws:iot:%s:%s:encryption-configuration", region, acct.ID)
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
		// Per-(acct, region) AWS-managed singleton config row.
		ManagedByProvider: true,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert iot encryption-configuration: %w", uerr)
	}
	return 1, n, nil
}
