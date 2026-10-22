package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectApprovedOrigin, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectSecurityKey, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectInstanceStorageConfig, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectIntegrationAssociation, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectNotification, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectRule, Service: "connect"})
}

// connectIntegrationAPI is the narrow surface for the Integration family.
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
	return connectRunPhases(st, acct, region,
		connectPhase{"connect approved origins", func() (int, int, error) {
			return scanConnectApprovedOrigins(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect security keys", func() (int, int, error) {
			return scanConnectSecurityKeys(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect instance storage configs", func() (int, int, error) {
			return scanConnectInstanceStorageConfigs(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect integration associations", func() (int, int, error) {
			return scanConnectIntegrationAssociations(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect notifications", func() (int, int, error) {
			return scanConnectNotifications(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect rules", func() (int, int, error) {
			return scanConnectRules(ctx, client, instances, acct, region, st, scanID)
		}},
	)
}

// scanConnectApprovedOrigins emits one row per (instance, origin) pair. SDK
// returns []string, so NativeID is synthesized:
// arn:aws:connect:{r}:{a}:instance/{instID}/approved-origin/{urlencoded}.
func scanConnectApprovedOrigins(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListApprovedOriginsPaginator(client, &connect.ListApprovedOriginsInput{InstanceId: &instID},
			func(o *connect.ListApprovedOriginsPaginatorOptions) { o.Limit = 25 })
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
		pager := connect.NewListSecurityKeysPaginator(client, &connect.ListSecurityKeysInput{InstanceId: &instID},
			func(o *connect.ListSecurityKeysPaginatorOptions) { o.Limit = 2 })
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
			pager := connect.NewListInstanceStorageConfigsPaginator(client, &connect.ListInstanceStorageConfigsInput{InstanceId: &instID, ResourceType: rt},
				func(o *connect.ListInstanceStorageConfigsPaginatorOptions) { o.Limit = 10 })
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
		pager := connect.NewListIntegrationAssociationsPaginator(client, &connect.ListIntegrationAssociationsInput{InstanceId: &instID},
			func(o *connect.ListIntegrationAssociationsPaginatorOptions) { o.Limit = 100 })
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

// scanConnectNotifications stores each listed notification with its tags.
func scanConnectNotifications(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:ListNotifications", instances,
		func(instID string) connectPager[*connect.ListNotificationsOutput] {
			return &connectTokenPager[*connect.ListNotificationsOutput]{
				call: func(ctx context.Context, token *string) (*connect.ListNotificationsOutput, error) {
					return client.ListNotifications(ctx, &connect.ListNotificationsInput{InstanceId: &instID, NextToken: token, MaxResults: sdkaws.Int32(100)})
				},
				next: func(o *connect.ListNotificationsOutput) *string { return o.NextToken },
			}
		},
		func(o *connect.ListNotificationsOutput) []cttypes.Notification { return o.NotificationSummaryList },
		func(n cttypes.Notification) (string, string) { return sv(n.Id), sv(n.Arn) })
	return connectStoreListed(st, acct, region, scanID, TypeConnectNotification, "connect notifications", listed,
		func(r *store.Resource, it connectListed[cttypes.Notification]) {
			r.Name = &it.id
			r.CreatedAt = tp(it.summary.CreatedAt)
			r.TagsJSON = mapTagsJSON(it.summary.Tags)
			r.AttributesJSON = mustJSON(it.summary)
		})
}

// scanConnectRules iterates every EventSourceName per instance — ListRules
// is event-source-scoped.
func scanConnectRules(ctx context.Context, client connectIntegrationAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := &connectListing[cttypes.RuleSummary]{op: "connect:ListRules"}
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		for _, src := range cttypes.EventSourceName("").Values() {
			err := connectReadPages(ctx, instID, listed,
				connect.NewListRulesPaginator(client, &connect.ListRulesInput{InstanceId: &instID, EventSourceName: src},
					func(o *connect.ListRulesPaginatorOptions) { o.Limit = 200 }),
				func(o *connect.ListRulesOutput) []cttypes.RuleSummary { return o.RuleSummaryList },
				func(s cttypes.RuleSummary) (string, string) { return sv(s.RuleId), sv(s.RuleArn) })
			if err != nil {
				listed.failed(st, acct, region, instID+"/"+string(src), err)
			}
		}
	}
	return connectDescribe[cttypes.RuleSummary]{
		rtype: TypeConnectRule, op: "connect:DescribeRule", label: "connect rules",
		describe: func(ctx context.Context, it connectListed[cttypes.RuleSummary], r *store.Resource) error {
			out, err := client.DescribeRule(ctx, &connect.DescribeRuleInput{InstanceId: &it.instanceID, RuleId: &it.id})
			if err != nil {
				return err
			}
			rule := out.Rule
			if rule == nil {
				return errConnectNoBody
			}
			name := sv(rule.Name)
			r.Name = &name
			r.TagsJSON = mapTagsJSON(rule.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.RuleSummary) {
			name := sv(s.Name)
			r.Name = &name
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
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
