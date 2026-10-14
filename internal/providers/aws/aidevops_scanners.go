package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	devopsagenttypes "github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAidevopsAgentSpace, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsService, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsAssociations, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsPrivateConnection, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsAsset, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsTrigger, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsWebhook, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsGoal, Service: "aidevops"})
	registerService(serviceEntry{
		name: "aws:aidevops",
		fn:   scanAidevops,
	})
}

// aidevopsAPI is the narrow surface the aidevops scanners use;
// *devopsagent.Client (the DevOps Agent / aidevops service) satisfies it.
type aidevopsAPI interface {
	ListAgentSpaces(context.Context, *devopsagent.ListAgentSpacesInput, ...func(*devopsagent.Options)) (*devopsagent.ListAgentSpacesOutput, error)
	ListServices(context.Context, *devopsagent.ListServicesInput, ...func(*devopsagent.Options)) (*devopsagent.ListServicesOutput, error)
	ListAssociations(context.Context, *devopsagent.ListAssociationsInput, ...func(*devopsagent.Options)) (*devopsagent.ListAssociationsOutput, error)
	ListPrivateConnections(context.Context, *devopsagent.ListPrivateConnectionsInput, ...func(*devopsagent.Options)) (*devopsagent.ListPrivateConnectionsOutput, error)
	ListAssets(context.Context, *devopsagent.ListAssetsInput, ...func(*devopsagent.Options)) (*devopsagent.ListAssetsOutput, error)
	ListTriggers(context.Context, *devopsagent.ListTriggersInput, ...func(*devopsagent.Options)) (*devopsagent.ListTriggersOutput, error)
	ListWebhooks(context.Context, *devopsagent.ListWebhooksInput, ...func(*devopsagent.Options)) (*devopsagent.ListWebhooksOutput, error)
	ListGoals(context.Context, *devopsagent.ListGoalsInput, ...func(*devopsagent.Options)) (*devopsagent.ListGoalsOutput, error)
}

// aidevops resources carry no AWS-issued ARN; NativeIDs are synthesized in the
// canonical arn:aws:aidevops:{region}:{account}:{kind}/{id} shape.
func aidevopsARN(region, acctID, kind, id string) string {
	return fmt.Sprintf("arn:aws:aidevops:%s:%s:%s/%s", region, acctID, kind, id)
}

func scanAidevops(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := devopsagent.NewFromConfig(acct.cfg, func(o *devopsagent.Options) { o.Region = region })
	return scanAidevopsAll(ctx, client, acct, region, st, scanID)
}

// scanAidevopsAll runs every phase even when an earlier one fails. Assets,
// triggers and goals are served by the data-plane host (dp.), the rest by the control
// plane (cp.), so only the first phase's unreachable endpoint speaks for the
// whole service; a later phase's transient error is warned about on its own.
func scanAidevopsAll(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var (
		spaces       []childParent
		associations []aidevopsAssociation
		errs         []error
	)
	for n, phase := range []struct {
		name string
		run  func() (int, int, error)
	}{
		{"aidevops:ListAgentSpaces", func() (int, int, error) {
			spaceIDs, t, i, err := scanAidevopsAgentSpaces(ctx, client, acct, region, st, scanID)
			for _, id := range spaceIDs {
				spaces = append(spaces, childParent{id: id, arn: aidevopsARN(region, acct.ID, "agent-space", id)})
			}
			return t, i, err
		}},
		{"aidevops:ListServices", func() (int, int, error) { return scanAidevopsServices(ctx, client, acct, region, st, scanID) }},
		{"aidevops:ListAssociations", func() (int, int, error) {
			var (
				t, i int
				err  error
			)
			associations, t, i, err = scanAidevopsAssociations(ctx, client, acct, region, st, scanID, spaces)
			return t, i, err
		}},
		{"aidevops:ListPrivateConnections", func() (int, int, error) {
			return scanAidevopsPrivateConnections(ctx, client, acct, region, st, scanID)
		}},
		{"aidevops:ListAssets", func() (int, int, error) { return scanAidevopsAssets(ctx, client, acct, region, st, scanID, spaces) }},
		{"aidevops:ListTriggers", func() (int, int, error) {
			return scanAidevopsTriggers(ctx, client, acct, region, st, scanID, spaces)
		}},
		{"aidevops:ListGoals", func() (int, int, error) { return scanAidevopsGoals(ctx, client, acct, region, st, scanID, spaces) }},
		{"aidevops:ListWebhooks", func() (int, int, error) {
			return scanAidevopsWebhooks(ctx, client, acct, region, st, scanID, associations)
		}},
	} {
		t, i, perr := phase.run()
		if n == 0 && isEndpointUnreachable(perr) {
			return t, i, perr
		}
		total += t
		inserted += i
		if isTransientNetworkError(perr) {
			_ = skipIfTransient(st, phase.name, acct.ID, region, perr)
			continue
		}
		errs = append(errs, perr)
	}
	return total, inserted, errors.Join(errs...)
}

func scanAidevopsAgentSpaces(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (spaceIDs []string, total, inserted int, err error) {
	p := devopsagent.NewListAgentSpacesPaginator(client, &devopsagent.ListAgentSpacesInput{})
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return spaceIDs, total, inserted, skipIfAccessDenied(st, "aidevops:ListAgentSpaces", acct.ID, region, perr)
			}
			return spaceIDs, total, inserted, fmt.Errorf("aidevops:ListAgentSpaces: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(page.AgentSpaces))
		for _, a := range page.AgentSpaces {
			id := sv(a.AgentSpaceId)
			if id == "" {
				continue
			}
			spaceIDs = append(spaceIDs, id)
			name := sv(a.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAidevopsAgentSpace,
				NativeID:       aidevopsARN(region, acct.ID, "agent-space", id),
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(a.CreatedAt),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
		}
		t, i, uerr := upsertBatch(st, batch, "aidevops agent-spaces")
		total, inserted = total+t, inserted+i
		if uerr != nil {
			return spaceIDs, total, inserted, uerr
		}
	}
	return spaceIDs, total, inserted, nil
}

func scanAidevopsServices(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	p := devopsagent.NewListServicesPaginator(client, &devopsagent.ListServicesInput{})
	for p.HasMorePages() {
		page, perr := p.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "aidevops:ListServices", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("aidevops:ListServices: %w", perr)
		}
		batch := make([]*store.Resource, 0, len(page.Services))
		for _, s := range page.Services {
			id := sv(s.ServiceId)
			if id == "" {
				continue
			}
			name := sv(s.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAidevopsService,
				NativeID:       aidevopsARN(region, acct.ID, "service", id),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(s),
				DiscoveredBy:   scanID,
			})
		}
		t, i, uerr := upsertBatch(st, batch, "aidevops services")
		total, inserted = total+t, inserted+i
		if uerr != nil {
			return total, inserted, uerr
		}
	}
	return total, inserted, nil
}

// aidevopsAssociation is an association listed this scan: ListWebhooks keys
// on both its agent space and its own id.
type aidevopsAssociation struct {
	spaceID string
	parent  childParent
	config  devopsagenttypes.ServiceConfiguration
}

// aidevopsHasWebhooks reports whether an association's configuration kind can
// carry webhooks, so ListWebhooks is called only where one can exist. The
// model ties webhooks to event channels (EventChannelType WEBHOOK), to
// PagerDuty and GitLab (WebhookType pagerduty / gitlab), and to the Datadog,
// New Relic and Splunk MCP servers (each documented "Mixin for webhook update
// support"). Every other kind, and a member newer than the pinned SDK, is not
// queried.
func aidevopsHasWebhooks(cfg devopsagenttypes.ServiceConfiguration) bool {
	switch cfg.(type) {
	case *devopsagenttypes.ServiceConfigurationMemberEventChannel,
		*devopsagenttypes.ServiceConfigurationMemberPagerduty,
		*devopsagenttypes.ServiceConfigurationMemberGitlab,
		*devopsagenttypes.ServiceConfigurationMemberMcpserverdatadog,
		*devopsagenttypes.ServiceConfigurationMemberMcpservernewrelic,
		*devopsagenttypes.ServiceConfigurationMemberMcpserversplunk:
		return true
	}
	return false
}

// scanAidevopsAssociations fans out ListAssociations per agent space — an
// association binds an agent space to a registered service.
func scanAidevopsAssociations(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (associations []aidevopsAssociation, total, inserted int, err error) {
	for _, space := range spaces {
		p := devopsagent.NewListAssociationsPaginator(client, &devopsagent.ListAssociationsInput{AgentSpaceId: &space.id})
		for p.HasMorePages() {
			page, perr := p.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "aidevops:ListAssociations", acct.ID, region, perr)
					break
				}
				return associations, total, inserted, fmt.Errorf("aidevops:ListAssociations %s: %w", space.id, perr)
			}
			batch := make([]*store.Resource, 0, len(page.Associations))
			listed := make([]aidevopsAssociation, 0, len(page.Associations))
			for _, a := range page.Associations {
				id := sv(a.AssociationId)
				if id == "" {
					continue
				}
				nativeID := aidevopsARN(region, acct.ID, "association", id)
				listed = append(listed, aidevopsAssociation{spaceID: space.id, parent: childParent{id: id, arn: nativeID}, config: a.Configuration})
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeAidevopsAssociations,
					NativeID:       nativeID,
					Name:           &id,
					Region:         &region,
					CreatedAt:      tp(a.CreatedAt),
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			t, i, uerr := upsertBatch(st, batch, "aidevops associations")
			total, inserted = total+t, inserted+i
			if uerr != nil {
				return associations, total, inserted, uerr
			}
			associations = append(associations, listed...)
		}
	}
	return associations, total, inserted, nil
}

// scanAidevopsPrivateConnections: ListPrivateConnections is a single-call list
// (no paginator / NextToken in the SDK).
func scanAidevopsPrivateConnections(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	out, perr := client.ListPrivateConnections(ctx, &devopsagent.ListPrivateConnectionsInput{})
	if perr != nil {
		if isAccessDenied(perr) {
			return 0, 0, skipIfAccessDenied(st, "aidevops:ListPrivateConnections", acct.ID, region, perr)
		}
		return 0, 0, fmt.Errorf("aidevops:ListPrivateConnections: %w", perr)
	}
	batch := make([]*store.Resource, 0, len(out.PrivateConnections))
	for _, c := range out.PrivateConnections {
		name := sv(c.Name)
		if name == "" {
			continue
		}
		n := name
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeAidevopsPrivateConnection,
			NativeID:       aidevopsARN(region, acct.ID, "private-connection", name),
			Name:           &n,
			Region:         &region,
			AttributesJSON: mustJSON(c),
			DiscoveredBy:   scanID,
		})
	}
	return upsertBatch(st, batch, "aidevops private-connections")
}

// aidevopsPageCap is the documented maxResults ceiling of the aidevops list ops.
const aidevopsPageCap = 100

// isAidevopsParentGone matches the ResourceNotFoundException ListTriggers and
// ListWebhooks model for a parent deleted after it was listed.
func isAidevopsParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// smithyDocumentJSON renders a Smithy document through its own marshaler:
// encoding/json renders one as {} because its value is unexported.
func smithyDocumentJSON(d document.Interface) (json.RawMessage, error) {
	if d == nil {
		return nil, nil
	}
	return d.MarshalSmithyDocument()
}

// aidevopsAssetAttrs is the SDK Asset with its Metadata document rendered.
type aidevopsAssetAttrs struct {
	devopsagenttypes.Asset
	Metadata json.RawMessage `json:"Metadata,omitempty"`
}

// scanAidevopsAssets lists the assets (skills, artifacts) uploaded to each
// agent space, under the space. ListAssets models no not-found error, so no
// failed space is skipped silently.
func scanAidevopsAssets(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "aidevops:ListAssets", spaces, func(error) bool { return false },
		func(gctx context.Context, space childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := devopsagent.NewListAssetsPaginator(client, &devopsagent.ListAssetsInput{AgentSpaceId: &space.id},
				func(o *devopsagent.ListAssetsPaginatorOptions) { o.Limit = aidevopsPageCap })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, a := range out.Items {
					id := sv(a.AssetId)
					if id == "" {
						continue
					}
					metadata, err := smithyDocumentJSON(a.Metadata)
					if err != nil {
						return nil, fmt.Errorf("asset %s Metadata: %w", id, err)
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeAidevopsAsset, NativeID: space.arn + "/asset/" + id,
						Name: &id, Region: &region, CreatedAt: tp(a.CreatedAt),
						AttributesJSON: mustJSON(aidevopsAssetAttrs{Asset: a, Metadata: metadata}), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// aidevopsTriggerAttrs is the SDK Trigger with its Action document rendered
// and its Condition union, which marshals as an unlabelled {"Value": ...},
// keyed by member name. A member newer than the pinned SDK is kept under
// UnknownMember with its tag (precedent: acmAcmeEndpointAttrs).
type aidevopsTriggerAttrs struct {
	devopsagenttypes.Trigger
	Action    json.RawMessage `json:"Action,omitempty"`
	Condition aidevopsTriggerConditionAttrs
}

type aidevopsTriggerConditionAttrs struct {
	Schedule      *devopsagenttypes.ScheduleCondition  `json:",omitempty"`
	UnknownMember *devopsagenttypes.UnknownUnionMember `json:",omitempty"`
}

func flattenAidevopsTriggerCondition(c devopsagenttypes.TriggerCondition) aidevopsTriggerConditionAttrs {
	var out aidevopsTriggerConditionAttrs
	switch m := c.(type) {
	case *devopsagenttypes.TriggerConditionMemberSchedule:
		out.Schedule = &m.Value
	case *devopsagenttypes.UnknownUnionMember:
		out.UnknownMember = m
	}
	return out
}

// scanAidevopsTriggers lists each agent space's triggers (scheduled agent
// invocations), under the space.
func scanAidevopsTriggers(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "aidevops:ListTriggers", spaces, isAidevopsParentGone,
		func(gctx context.Context, space childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := devopsagent.NewListTriggersPaginator(client, &devopsagent.ListTriggersInput{AgentSpaceId: &space.id},
				func(o *devopsagent.ListTriggersPaginatorOptions) { o.Limit = aidevopsPageCap })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, tr := range out.Items {
					id := sv(tr.TriggerId)
					if id == "" {
						continue
					}
					action, err := smithyDocumentJSON(tr.Action)
					if err != nil {
						return nil, fmt.Errorf("trigger %s Action: %w", id, err)
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeAidevopsTrigger, NativeID: space.arn + "/trigger/" + id,
						Name: &id, Status: nonEmptyPtr(sv(tr.Status)), Region: &region, CreatedAt: tp(tr.CreatedAt),
						AttributesJSON: mustJSON(aidevopsTriggerAttrs{
							Trigger: tr, Action: action, Condition: flattenAidevopsTriggerCondition(tr.Condition),
						}), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanAidevopsGoals lists each agent space's goals (objectives the agent
// evaluates, optionally on a schedule), under the space. ListGoals models no
// not-found error, so no failed space is skipped silently.
func scanAidevopsGoals(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, int, error) {
	return childFanOut(ctx, st, acct, region, "aidevops:ListGoals", spaces, func(error) bool { return false },
		func(gctx context.Context, space childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := devopsagent.NewListGoalsPaginator(client, &devopsagent.ListGoalsInput{AgentSpaceId: &space.id},
				func(o *devopsagent.ListGoalsPaginatorOptions) { o.Limit = aidevopsPageCap })
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, g := range out.Goals {
					id := sv(g.GoalId)
					if id == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeAidevopsGoal, NativeID: space.arn + "/goal/" + id,
						Name: g.Title, Status: nonEmptyPtr(string(g.Status)), Region: &region, CreatedAt: tp(g.CreatedAt),
						AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanAidevopsWebhooks lists each association's inbound webhooks, under the
// association. The Webhook shape carries only id, URL and auth type; no
// webhook secret is part of it. ListWebhooks has no pagination.
func scanAidevopsWebhooks(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, associations []aidevopsAssociation) (int, int, error) {
	var parents []childParent
	spaceOf := make(map[string]string, len(associations))
	for _, a := range associations {
		if !aidevopsHasWebhooks(a.config) {
			continue
		}
		parents = append(parents, a.parent)
		spaceOf[a.parent.arn] = a.spaceID
	}
	return childFanOut(ctx, st, acct, region, "aidevops:ListWebhooks", parents, isAidevopsParentGone,
		func(gctx context.Context, assoc childParent) ([]*store.Resource, error) {
			spaceID := spaceOf[assoc.arn]
			out, err := client.ListWebhooks(gctx, &devopsagent.ListWebhooksInput{AgentSpaceId: &spaceID, AssociationId: &assoc.id})
			if err != nil {
				return nil, err
			}
			var rows []*store.Resource
			for _, w := range out.Webhooks {
				id := sv(w.WebhookId)
				if id == "" {
					continue
				}
				rows = append(rows, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeAidevopsWebhook, NativeID: assoc.arn + "/webhook/" + id,
					Name: &id, Region: &region, AttributesJSON: mustJSON(w), DiscoveredBy: scanID,
				})
			}
			return rows, nil
		})
}
