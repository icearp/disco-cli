package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

func init() {
	registerExtraEmits(
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectApprovedOrigin},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectSecurityKey},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectInstanceStorageConfig},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectIntegrationAssociation},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectNotification},
		coverage.TypeDecl{Service: "connect", DiscoType: TypeConnectRule},
	)
}

// connectIntegrationAPI is the narrow surface used by the Integration family.
type connectIntegrationAPI interface {
	ListApprovedOrigins(context.Context, *connect.ListApprovedOriginsInput, ...func(*connect.Options)) (*connect.ListApprovedOriginsOutput, error)
	ListSecurityKeys(context.Context, *connect.ListSecurityKeysInput, ...func(*connect.Options)) (*connect.ListSecurityKeysOutput, error)
	ListInstanceStorageConfigs(context.Context, *connect.ListInstanceStorageConfigsInput, ...func(*connect.Options)) (*connect.ListInstanceStorageConfigsOutput, error)
	ListIntegrationAssociations(context.Context, *connect.ListIntegrationAssociationsInput, ...func(*connect.Options)) (*connect.ListIntegrationAssociationsOutput, error)
	ListNotifications(context.Context, *connect.ListNotificationsInput, ...func(*connect.Options)) (*connect.ListNotificationsOutput, error)
	ListRules(context.Context, *connect.ListRulesInput, ...func(*connect.Options)) (*connect.ListRulesOutput, error)
	DescribeRule(context.Context, *connect.DescribeRuleInput, ...func(*connect.Options)) (*connect.DescribeRuleOutput, error)
}

// scanConnectIntegration runs Integration-family phases per instance.
func scanConnectIntegration(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanConnectApprovedOrigins(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectSecurityKeys(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectInstanceStorageConfigs(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectIntegrationAssociations(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectNotifications(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) { return scanConnectRules(ctx, client, instances, acct, region, st, scanID) },
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

// scanConnectApprovedOrigins emits one row per (instance, origin) pair.
// SDK returns []string so synthesize NativeID:
// arn:aws:connect:{r}:{a}:instance/{instID}/approved-origin/{urlencoded}.
func scanConnectApprovedOrigins(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListApprovedOriginsPaginator(client, &connect.ListApprovedOriginsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListApprovedOrigins", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListApprovedOrigins %s: %w", instID, perr)
			}
			for _, origin := range out.Origins {
				origin := origin
				arn := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/approved-origin/%s", region, acct.ID, instID, origin)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectApprovedOrigin,
					NativeID:       arn,
					Name:           &origin,
					Region:         &region,
					AttributesJSON: mustJSON(map[string]string{"InstanceId": instID, "Origin": origin}),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	return upsertConnectBatch(st, batch, "connect approved origins")
}

func scanConnectSecurityKeys(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListSecurityKeysPaginator(client, &connect.ListSecurityKeysInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListSecurityKeys", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListSecurityKeys %s: %w", instID, perr)
			}
			for _, k := range out.SecurityKeys {
				if k.AssociationId == nil {
					continue
				}
				assoc := *k.AssociationId
				arn := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/security-key/%s", region, acct.ID, instID, assoc)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectSecurityKey,
					NativeID:       arn,
					Name:           &assoc,
					Region:         &region,
					CreatedAt:      tp(k.CreationTime),
					AttributesJSON: mustJSON(k),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	return upsertConnectBatch(st, batch, "connect security keys")
}

// scanConnectInstanceStorageConfigs iterates every InstanceStorageResourceType
// per instance — the SDK requires ResourceType on each list call.
func scanConnectInstanceStorageConfigs(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		for _, rt := range cttypes.InstanceStorageResourceType("").Values() {
			rt := rt
			pager := connect.NewListInstanceStorageConfigsPaginator(client, &connect.ListInstanceStorageConfigsInput{InstanceId: &instID, ResourceType: rt})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("connect:ListInstanceStorageConfigs %s/%s: %w", instID, rt, perr)
				}
				for _, c := range out.StorageConfigs {
					if c.AssociationId == nil {
						continue
					}
					assoc := *c.AssociationId
					arn := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/storage-config/%s/%s", region, acct.ID, instID, rt, assoc)
					name := fmt.Sprintf("%s:%s", rt, assoc)
					batch = append(batch, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeConnectInstanceStorageConfig,
						NativeID:       arn,
						Name:           &name,
						Region:         &region,
						AttributesJSON: mustJSON(c),
						DiscoveredBy:   scanID,
					})
				}
			}
		}
	}
	return upsertConnectBatch(st, batch, "connect instance storage configs")
}

func scanConnectIntegrationAssociations(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListIntegrationAssociationsPaginator(client, &connect.ListIntegrationAssociationsInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListIntegrationAssociations", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListIntegrationAssociations %s: %w", instID, perr)
			}
			for _, a := range out.IntegrationAssociationSummaryList {
				arn := sv(a.IntegrationAssociationArn)
				if arn == "" {
					continue
				}
				name := string(a.IntegrationType)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectIntegrationAssociation,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
		}
	}
	return upsertConnectBatch(st, batch, "connect integration associations")
}

func scanConnectNotifications(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		var token *string
		for {
			out, perr := client.ListNotifications(ctx, &connect.ListNotificationsInput{InstanceId: &instID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:ListNotifications %s: %w", instID, perr)
			}
			for _, n := range out.NotificationSummaryList {
				arn := sv(n.Arn)
				if arn == "" {
					continue
				}
				id := sv(n.Id)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectNotification,
					NativeID:       arn,
					Name:           &id,
					Region:         &region,
					CreatedAt:      tp(n.CreatedAt),
					AttributesJSON: mustJSON(n),
					DiscoveredBy:   scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertConnectBatch(st, batch, "connect notifications")
}

// scanConnectRules iterates every EventSourceName per instance — ListRules
// is event-source-scoped.
func scanConnectRules(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var items []connectInstanceItem
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		for _, src := range cttypes.EventSourceName("").Values() {
			src := src
			pager := connect.NewListRulesPaginator(client, &connect.ListRulesInput{InstanceId: &instID, EventSourceName: src})
			for pager.HasMorePages() {
				out, perr := pager.NextPage(ctx)
				if perr != nil {
					if isAccessDenied(perr) {
						break
					}
					return 0, 0, fmt.Errorf("connect:ListRules %s/%s: %w", instID, src, perr)
				}
				for _, r := range out.RuleSummaryList {
					if r.RuleId != nil {
						items = append(items, connectInstanceItem{instID, *r.RuleId})
					}
				}
			}
		}
	}
	return connectPerInstanceFanout(ctx, items, fanoutMed, func(gctx context.Context, k connectInstanceItem) (*store.Resource, error) {
		out, derr := client.DescribeRule(gctx, &connect.DescribeRuleInput{InstanceId: &k.instanceID, RuleId: &k.id})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("connect:DescribeRule %s/%s: %w", k.instanceID, k.id, derr)
		}
		if out.Rule == nil {
			return nil, nil
		}
		arn := sv(out.Rule.RuleArn)
		if arn == "" {
			return nil, nil
		}
		name := sv(out.Rule.Name)
		return &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectRule,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(out),
			DiscoveredBy:   scanID,
		}, nil
	}, st, "connect rules")
}

func upsertConnectBatch(st *store.Store, batch []*store.Resource, label string) (int, int, error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", label, uerr)
	}
	return len(batch), n, nil
}
