package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTAccountAuditConfiguration, Service: "iot", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeIoTScheduledAudit, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTMitigationAction, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTSecurityProfile, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTCustomMetric, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTDimension, Service: "iot"})
}

// iotDefenderAPI is the narrow surface used by the Defender family — IoT
// Device Defender audits, mitigation actions, security profiles, custom
// metrics, dimensions.
type iotDefenderAPI interface {
	iotTagsAPI
	DescribeAccountAuditConfiguration(context.Context, *iot.DescribeAccountAuditConfigurationInput, ...func(*iot.Options)) (*iot.DescribeAccountAuditConfigurationOutput, error)
	ListScheduledAudits(context.Context, *iot.ListScheduledAuditsInput, ...func(*iot.Options)) (*iot.ListScheduledAuditsOutput, error)
	DescribeScheduledAudit(context.Context, *iot.DescribeScheduledAuditInput, ...func(*iot.Options)) (*iot.DescribeScheduledAuditOutput, error)
	ListMitigationActions(context.Context, *iot.ListMitigationActionsInput, ...func(*iot.Options)) (*iot.ListMitigationActionsOutput, error)
	DescribeMitigationAction(context.Context, *iot.DescribeMitigationActionInput, ...func(*iot.Options)) (*iot.DescribeMitigationActionOutput, error)
	ListSecurityProfiles(context.Context, *iot.ListSecurityProfilesInput, ...func(*iot.Options)) (*iot.ListSecurityProfilesOutput, error)
	DescribeSecurityProfile(context.Context, *iot.DescribeSecurityProfileInput, ...func(*iot.Options)) (*iot.DescribeSecurityProfileOutput, error)
	ListCustomMetrics(context.Context, *iot.ListCustomMetricsInput, ...func(*iot.Options)) (*iot.ListCustomMetricsOutput, error)
	DescribeCustomMetric(context.Context, *iot.DescribeCustomMetricInput, ...func(*iot.Options)) (*iot.DescribeCustomMetricOutput, error)
	ListDimensions(context.Context, *iot.ListDimensionsInput, ...func(*iot.Options)) (*iot.ListDimensionsOutput, error)
	DescribeDimension(context.Context, *iot.DescribeDimensionInput, ...func(*iot.Options)) (*iot.DescribeDimensionOutput, error)
}

// scanIoTDefender runs Defender-family phases, adding each to p, and reports
// whether a write failed.
func scanIoTDefender(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	return runIoTPhases(p,
		iotCounted("iot:DescribeAccountAuditConfiguration", func() (int, int, error) {
			return scanIoTAccountAuditConfiguration(ctx, client, acct, region, st, scanID)
		}),
		func(p *phaseTotals) bool {
			return scanIoTScheduledAudits(ctx, client, acct, region, st, scanID, p, tags)
		},
		func(p *phaseTotals) bool {
			return scanIoTMitigationActions(ctx, client, acct, region, st, scanID, p, tags)
		},
		func(p *phaseTotals) bool {
			return scanIoTSecurityProfiles(ctx, client, acct, region, st, scanID, p, tags)
		},
		func(p *phaseTotals) bool { return scanIoTCustomMetrics(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTDimensions(ctx, client, acct, region, st, scanID, p, tags) },
	)
}

// scanIoTAccountAuditConfiguration emits a single per-region row — the
// audit config is account/region-scoped with no list API. NativeID synth.
func scanIoTAccountAuditConfiguration(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeAccountAuditConfiguration(ctx, &iot.DescribeAccountAuditConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "iot:DescribeAccountAuditConfiguration", acct.ID, region, err)
		}
		// Regions without IoT Device Defender 404 with "No method found matching
		// route" (generic UnknownError code) — per-region gap, silent-skip.
		if isHTTP404(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("iot:DescribeAccountAuditConfiguration: %w", err)
	}
	arn := fmt.Sprintf("arn:aws:iot:%s:%s:account-audit-configuration", region, acct.ID)
	name := "audit-configuration"
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeIoTAccountAuditConfiguration,
		NativeID:       arn,
		Name:           &name,
		Region:         &region,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}
	n, uerr := st.UpsertResources([]*store.Resource{r})
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert iot account-audit-configuration: %w", uerr)
	}
	return 1, n, nil
}

func scanIoTScheduledAudits(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListScheduledAudits", "iot scheduled audits", "ScheduledAuditName",
		func(ctx context.Context, token *string) ([]iottypes.ScheduledAuditMetadata, *string, error) {
			out, err := client.ListScheduledAudits(ctx, &iot.ListScheduledAuditsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			// Device Defender is not routed in every region: the first page
			// there is a bare 404 "No method found matching route".
			if token == nil && isHTTP404(err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.ScheduledAudits, out.NextToken, nil
		},
		func(x iottypes.ScheduledAuditMetadata) *string { return x.ScheduledAuditName },
		func(x iottypes.ScheduledAuditMetadata) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTScheduledAudit, x.ScheduledAuditArn, x.ScheduledAuditName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeScheduledAudit", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeScheduledAudit(ctx, &iot.DescribeScheduledAuditInput{ScheduledAuditName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.ScheduledAuditArn)
			if arn == "" {
				return nil, nil
			}
			sname := sv(out.ScheduledAuditName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTScheduledAudit,
				NativeID:       arn,
				Name:           &sname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTMitigationActions(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListMitigationActions", "iot mitigation actions", "ActionName",
		func(ctx context.Context, token *string) ([]iottypes.MitigationActionIdentifier, *string, error) {
			out, err := client.ListMitigationActions(ctx, &iot.ListMitigationActionsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			// Device Defender is not routed in every region: the first page
			// there is a bare 404 "No method found matching route".
			if token == nil && isHTTP404(err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.ActionIdentifiers, out.NextToken, nil
		},
		func(x iottypes.MitigationActionIdentifier) *string { return x.ActionName },
		func(x iottypes.MitigationActionIdentifier) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTMitigationAction, x.ActionArn, x.ActionName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeMitigationAction", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeMitigationAction(ctx, &iot.DescribeMitigationActionInput{ActionName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.ActionArn)
			if arn == "" {
				return nil, nil
			}
			mname := sv(out.ActionName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTMitigationAction,
				NativeID:       arn,
				Name:           &mname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTSecurityProfiles(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListSecurityProfiles", "iot security profiles", "Name",
		func(ctx context.Context, token *string) ([]iottypes.SecurityProfileIdentifier, *string, error) {
			out, err := client.ListSecurityProfiles(ctx, &iot.ListSecurityProfilesInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			// Device Defender is not routed in every region: the first page
			// there is a bare 404 "No method found matching route".
			if token == nil && isHTTP404(err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.SecurityProfileIdentifiers, out.NextToken, nil
		},
		func(x iottypes.SecurityProfileIdentifier) *string { return x.Name },
		func(x iottypes.SecurityProfileIdentifier) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTSecurityProfile, x.Arn, x.Name, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeSecurityProfile", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeSecurityProfile(ctx, &iot.DescribeSecurityProfileInput{SecurityProfileName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.SecurityProfileArn)
			if arn == "" {
				return nil, nil
			}
			pname := sv(out.SecurityProfileName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTSecurityProfile,
				NativeID:       arn,
				Name:           &pname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTCustomMetrics(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListCustomMetrics", "iot custom metrics", "MetricName",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListCustomMetrics(ctx, &iot.ListCustomMetricsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			// Device Defender is not routed in every region: the first page
			// there is a bare 404 "No method found matching route".
			if token == nil && isHTTP404(err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.MetricNames, out.NextToken, nil
		},
		iotName, nil)
	d := list.describe(ctx, st, acct, region, "iot:DescribeCustomMetric", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeCustomMetric(ctx, &iot.DescribeCustomMetricInput{MetricName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.MetricArn)
			if arn == "" {
				return nil, nil
			}
			mname := sv(out.MetricName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTCustomMetric,
				NativeID:       arn,
				Name:           &mname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTDimensions(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListDimensions", "iot dimensions", "DimensionName",
		func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListDimensions(ctx, &iot.ListDimensionsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			// Device Defender is not routed in every region: the first page
			// there is a bare 404 "No method found matching route".
			if token == nil && isHTTP404(err) {
				return nil, nil, nil
			}
			if err != nil {
				return nil, nil, err
			}
			return out.DimensionNames, out.NextToken, nil
		},
		iotName, nil)
	d := list.describe(ctx, st, acct, region, "iot:DescribeDimension", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeDimension(ctx, &iot.DescribeDimensionInput{Name: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.Arn)
			if arn == "" {
				return nil, nil
			}
			dname := sv(out.Name)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTDimension,
				NativeID:       arn,
				Name:           &dname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}
