package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTAccountAuditConfiguration},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTScheduledAudit},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTMitigationAction},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTSecurityProfile},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTCustomMetric},
		coverage.TypeDecl{Service: "iot", DiscoType: TypeIoTDimension},
	)
}

// iotDefenderAPI is the narrow surface used by the Defender family —
// IoT Device Defender resources (audits, mitigation actions, security
// profiles, custom metrics, dimensions).
type iotDefenderAPI interface {
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

// scanIoTDefender runs Defender-family phases.
func scanIoTDefender(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanIoTAccountAuditConfiguration(ctx, client, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanIoTScheduledAudits(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTMitigationActions(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTSecurityProfiles(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTCustomMetrics(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTDimensions(ctx, client, acct, region, st, scanID) },
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

// scanIoTAccountAuditConfiguration emits a single per-region row — the
// audit config is account/region-scoped with no list API. NativeID synth.
func scanIoTAccountAuditConfiguration(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeAccountAuditConfiguration(ctx, &iot.DescribeAccountAuditConfigurationInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, "iot:DescribeAccountAuditConfiguration", acct.ID, region, err)
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

func scanIoTScheduledAudits(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListScheduledAuditsPaginator(client, &iot.ListScheduledAuditsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListScheduledAudits", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListScheduledAudits: %w", perr)
		}
		for _, s := range out.ScheduledAudits {
			if s.ScheduledAuditName != nil {
				names = append(names, *s.ScheduledAuditName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeScheduledAudit(gctx, &iot.DescribeScheduledAuditInput{ScheduledAuditName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeScheduledAudit %s: %w", name, derr)
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
	}, st, "iot scheduled audits")
}

func scanIoTMitigationActions(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListMitigationActionsPaginator(client, &iot.ListMitigationActionsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListMitigationActions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListMitigationActions: %w", perr)
		}
		for _, m := range out.ActionIdentifiers {
			if m.ActionName != nil {
				names = append(names, *m.ActionName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeMitigationAction(gctx, &iot.DescribeMitigationActionInput{ActionName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeMitigationAction %s: %w", name, derr)
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
	}, st, "iot mitigation actions")
}

func scanIoTSecurityProfiles(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListSecurityProfilesPaginator(client, &iot.ListSecurityProfilesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListSecurityProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListSecurityProfiles: %w", perr)
		}
		for _, p := range out.SecurityProfileIdentifiers {
			if p.Name != nil {
				names = append(names, *p.Name)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeSecurityProfile(gctx, &iot.DescribeSecurityProfileInput{SecurityProfileName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeSecurityProfile %s: %w", name, derr)
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
	}, st, "iot security profiles")
}

func scanIoTCustomMetrics(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListCustomMetricsPaginator(client, &iot.ListCustomMetricsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListCustomMetrics", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListCustomMetrics: %w", perr)
		}
		names = append(names, out.MetricNames...)
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeCustomMetric(gctx, &iot.DescribeCustomMetricInput{MetricName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeCustomMetric %s: %w", name, derr)
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
	}, st, "iot custom metrics")
}

func scanIoTDimensions(ctx context.Context, client iotDefenderAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListDimensionsPaginator(client, &iot.ListDimensionsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListDimensions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListDimensions: %w", perr)
		}
		names = append(names, out.DimensionNames...)
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeDimension(gctx, &iot.DescribeDimensionInput{Name: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeDimension %s: %w", name, derr)
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
	}, st, "iot dimensions")
}
