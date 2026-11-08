package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent"
	"github.com/aws/aws-sdk-go-v2/service/devopsagent/document"
	devopsagenttypes "github.com/aws/aws-sdk-go-v2/service/devopsagent/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAidevopsAgentSpace, Service: "aidevops"})
	// A SigV4 MCP server's custom headers (@sensitive in the model) can carry
	// an API key or bearer token sent to the server. The header names are
	// kept, and a value only where its shape is judged safe (a reference, a
	// boolean); any other value is replaced with a reason-tagged placeholder.
	registerType(restype.Descriptor{Type: TypeAidevopsService, Service: "aidevops", Redact: []redact.Rule{
		{Path: "AdditionalServiceDetails.Mcpserversigv4.CustomHeaders.*", Mode: redact.RedactUnlessSafe},
	}})
	registerType(restype.Descriptor{Type: TypeAidevopsAssociations, Service: "aidevops", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeAidevopsPrivateConnection, Service: "aidevops"})
	registerType(restype.Descriptor{Type: TypeAidevopsAsset, Service: "aidevops", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeAidevopsTrigger, Service: "aidevops", Untaggable: true})
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
	ListTagsForResource(context.Context, *devopsagent.ListTagsForResourceInput, ...func(*devopsagent.Options)) (*devopsagent.ListTagsForResourceOutput, error)
}

const (
	aidevopsListAgentSpacesOp        = "aidevops:ListAgentSpaces"
	aidevopsListServicesOp           = "aidevops:ListServices"
	aidevopsListPrivateConnectionsOp = "aidevops:ListPrivateConnections"
	aidevopsTagsOp                   = "aidevops:ListTagsForResource"
)

// aidevopsARN builds a NativeID as arn:{p}:aidevops:{region}:{account}:{kind}/{id}:
// no aidevops listing returns an ARN. For agent spaces (aidevopsAgentSpaceARN),
// services and private connections this is the Service Reference's ARN format,
// which ListTagsForResource is sent (aidevopsTags). An association's
// association/{id} is not: the Service Reference nests it under its agent space.
func aidevopsARN(partition, region, acctID, kind, id string) string {
	return arnFor(partition, "aidevops", region, acctID, kind+"/"+id)
}

// aidevopsAgentSpaceARN is an agent space's ARN in the Service Reference's
// format, agentspace/${AgentSpaceId}; the AgentSpace SDK shape has no ARN
// field. Assets and triggers stored under it then carry their own Service
// Reference ARNs (agentspace/${AgentSpaceId}/asset/${AssetId}, …/trigger/…).
func aidevopsAgentSpaceARN(partition, region, acctID, id string) string {
	return aidevopsARN(partition, region, acctID, "agentspace", id)
}

func scanAidevops(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total int, err error) {
	client := devopsagent.NewFromConfig(acct.cfg, func(o *devopsagent.Options) { o.Region = region })
	return scanAidevopsAll(ctx, client, acct, region, st, scanID)
}

// scanAidevopsAll runs every listing as its own phase, one at a time: a
// phase's error does not stop the later ones; a failed write and ctx's end
// do, and so does an NXDOMAIN of the first listing with nothing stored (the
// region does not serve the service) (runHostPhases). The per-parent phases
// get the parents stored this scan. Agent spaces, services and private
// connections have their tags read per row (aidevopsTags), whose denials are
// one warning for the scan.
//
// Assets, triggers and goals are served by the data-plane host (dp.), the
// rest by the control plane (cp.), yet every phase runs as host 0: the
// data-plane phases list only under agent spaces already stored, so a
// data-plane NXDOMAIN always follows stored rows and is a failure of this
// scan (resultLateNXDOMAIN). As a host of its own, its first NXDOMAIN would be
// dropped as a region serving the control plane alone.
func scanAidevopsAll(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := aidevopsTags(client).sharingDenials()
	var (
		spaces       []childParent
		associations []aidevopsAssociation
	)
	total, err := runHostPhases(ctx, st, acct, region, "aidevops", 0, []hostPhase{
		{op: aidevopsListAgentSpacesOp, run: func(p *phaseTotals) (failed bool) {
			spaces, failed = scanAidevopsAgentSpaces(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: aidevopsListServicesOp, run: func(p *phaseTotals) bool {
			return scanAidevopsServices(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "aidevops:ListAssociations", func() (int, error) {
			var (
				t   int
				err error
			)
			associations, t, err = scanAidevopsAssociations(ctx, client, acct, region, st, scanID, spaces)
			return t, err
		}),
		{op: aidevopsListPrivateConnectionsOp, run: func(p *phaseTotals) bool {
			return scanAidevopsPrivateConnections(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "aidevops:ListAssets", func() (int, error) {
			return scanAidevopsAssets(ctx, client, acct, region, st, scanID, spaces)
		}),
		countedPhase(0, "aidevops:ListTriggers", func() (int, error) {
			return scanAidevopsTriggers(ctx, client, acct, region, st, scanID, spaces)
		}),
		countedPhase(0, "aidevops:ListGoals", func() (int, error) {
			return scanAidevopsGoals(ctx, client, acct, region, st, scanID, spaces)
		}),
		countedPhase(0, "aidevops:ListWebhooks", func() (int, error) {
			return scanAidevopsWebhooks(ctx, client, acct, region, st, scanID, associations)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// aidevopsTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN built in the Service Reference's format, on the
// assumption, unverified live, that the ARN's id is the id the listing
// returns. The Service Reference lists agent spaces, services and private
// connections, and no other type stored here, under ListTagsForResource and
// TagResource; the op's model ARN pattern accepts those three kinds only, and
// only their Create (RegisterService for a service) inputs take Tags.
// ResourceNotFoundException, the op's modeled not-found, drops a resource
// deleted after it was listed. The model's ARN pattern names the aws
// partition only; a GovCloud or China ARN is sent in its own partition, and
// how the op answers it is unverified.
func aidevopsTags(client aidevopsAPI) rowTagReader {
	return rowTagReader{
		op:       aidevopsTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &devopsagent.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanAidevopsAgentSpaces stores the agent spaces with their tags, a page at
// a time, and returns those stored as the parents of the per-space phases,
// each keyed by its AgentSpaceId. It reports whether a write failed, after
// which the scan must stop.
func scanAidevopsAgentSpaces(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, aidevopsListAgentSpacesOp, "agent spaces", "AgentSpaceId",
		func(ctx context.Context, token *string) ([]devopsagenttypes.AgentSpace, *string, error) {
			out, err := client.ListAgentSpaces(ctx, &devopsagent.ListAgentSpacesInput{MaxResults: sdkaws.Int32(aidevopsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AgentSpaces, out.NextToken, nil
		},
		func(a devopsagenttypes.AgentSpace) (*store.Resource, string, string) {
			id := sv(a.AgentSpaceId)
			if id == "" {
				return nil, "agent spaces have no AgentSpaceId", cmp.Or(sv(a.Name), noIdentifier)
			}
			name := sv(a.Name)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAidevopsAgentSpace, NativeID: aidevopsAgentSpaceARN(acct.partition(), region, acct.ID, id),
				Name: &name, Region: &region, CreatedAt: tp(a.CreatedAt),
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}, "", ""
		},
		func(a devopsagenttypes.AgentSpace) string { return sv(a.AgentSpaceId) },
		tags, p)
}

// scanAidevopsServices stores the registered services with their tags, each
// page written as soon as its tags are read. It reports whether a write
// failed, after which the scan must stop.
func scanAidevopsServices(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, aidevopsListServicesOp,
		func(ctx context.Context, token *string) ([]devopsagenttypes.RegisteredService, *string, error) {
			out, err := client.ListServices(ctx, &devopsagent.ListServicesInput{MaxResults: sdkaws.Int32(aidevopsPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Services, out.NextToken, nil
		},
		func(s devopsagenttypes.RegisteredService) (*store.Resource, string, string) {
			id := sv(s.ServiceId)
			if id == "" {
				return nil, "services have no ServiceId", cmp.Or(sv(s.Name), noIdentifier)
			}
			name := sv(s.Name)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAidevopsService, NativeID: aidevopsARN(acct.partition(), region, acct.ID, "service", id),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(newAidevopsServiceAttrs(s)), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
}

// aidevopsServiceAttrs is the SDK RegisteredService with its AccessibleResources
// documents rendered and its AdditionalServiceDetails union, which marshals as
// an unlabelled {"Value": ...}, keyed by member name. A nil and an empty
// AccessibleResources stay null and [], as the SDK shape writes them.
type aidevopsServiceAttrs struct {
	devopsagenttypes.RegisteredService
	AccessibleResources      []aidevopsDocumentJSON
	AdditionalServiceDetails aidevopsServiceDetailsAttrs
}

func newAidevopsServiceAttrs(s devopsagenttypes.RegisteredService) aidevopsServiceAttrs {
	out := aidevopsServiceAttrs{RegisteredService: s, AdditionalServiceDetails: flattenAidevopsServiceDetails(s.AdditionalServiceDetails)}
	if s.AccessibleResources != nil {
		out.AccessibleResources = make([]aidevopsDocumentJSON, 0, len(s.AccessibleResources))
	}
	for _, d := range s.AccessibleResources {
		out.AccessibleResources = append(out.AccessibleResources, aidevopsDocumentJSON{d})
	}
	return out
}

// aidevopsDocumentJSON renders a Smithy document through smithyDocumentJSON when
// the row is marshalled, so a document that does not render fails the row's
// mustJSON, which the store reports as a scan error with the row still
// written, instead of the row being dropped.
type aidevopsDocumentJSON struct{ doc document.Interface }

func (d aidevopsDocumentJSON) MarshalJSON() ([]byte, error) {
	return smithyDocumentJSON(d.doc)
}

// aidevopsServiceDetailsAttrs holds the one AdditionalServiceDetails member a
// service carries, under its member name. A member newer than the pinned SDK
// is kept under UnknownMember with its tag (precedent: acmAcmeEndpointAttrs).
type aidevopsServiceDetailsAttrs struct {
	Azuredevops       *devopsagenttypes.RegisteredAzureDevOpsServiceDetails `json:",omitempty"`
	Azureidentity     *devopsagenttypes.RegisteredAzureIdentityDetails      `json:",omitempty"`
	Github            *devopsagenttypes.RegisteredGithubServiceDetails      `json:",omitempty"`
	Gitlab            *devopsagenttypes.RegisteredGitLabServiceDetails      `json:",omitempty"`
	Mcpserver         *devopsagenttypes.RegisteredMCPServerDetails          `json:",omitempty"`
	Mcpserverdatadog  *devopsagenttypes.RegisteredMCPServerDetails          `json:",omitempty"`
	Mcpservergrafana  *devopsagenttypes.RegisteredGrafanaServerDetails      `json:",omitempty"`
	Mcpservernewrelic *devopsagenttypes.RegisteredNewRelicDetails           `json:",omitempty"`
	Mcpserversigv4    *devopsagenttypes.RegisteredMCPServerSigV4Details     `json:",omitempty"`
	Mcpserversplunk   *devopsagenttypes.RegisteredMCPServerDetails          `json:",omitempty"`
	Pagerduty         *devopsagenttypes.RegisteredPagerDutyDetails          `json:",omitempty"`
	Remoteagent       *devopsagenttypes.RegisteredRemoteAgentDetails        `json:",omitempty"`
	Remoteagentsigv4  *devopsagenttypes.RegisteredRemoteAgentSigV4Details   `json:",omitempty"`
	Servicenow        *devopsagenttypes.RegisteredServiceNowDetails         `json:",omitempty"`
	Slack             *devopsagenttypes.RegisteredSlackServiceDetails       `json:",omitempty"`
	UnknownMember     *devopsagenttypes.UnknownUnionMember                  `json:",omitempty"`
}

func flattenAidevopsServiceDetails(d devopsagenttypes.AdditionalServiceDetails) aidevopsServiceDetailsAttrs {
	var out aidevopsServiceDetailsAttrs
	switch m := d.(type) {
	case *devopsagenttypes.AdditionalServiceDetailsMemberAzuredevops:
		out.Azuredevops = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberAzureidentity:
		out.Azureidentity = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberGithub:
		out.Github = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberGitlab:
		out.Gitlab = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberMcpserver:
		out.Mcpserver = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberMcpserverdatadog:
		out.Mcpserverdatadog = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberMcpservergrafana:
		out.Mcpservergrafana = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberMcpservernewrelic:
		out.Mcpservernewrelic = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberMcpserversigv4:
		out.Mcpserversigv4 = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberMcpserversplunk:
		out.Mcpserversplunk = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberPagerduty:
		out.Pagerduty = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberRemoteagent:
		out.Remoteagent = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberRemoteagentsigv4:
		out.Remoteagentsigv4 = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberServicenow:
		out.Servicenow = &m.Value
	case *devopsagenttypes.AdditionalServiceDetailsMemberSlack:
		out.Slack = &m.Value
	case *devopsagenttypes.UnknownUnionMember:
		out.UnknownMember = m
	}
	return out
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
func scanAidevopsAssociations(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (associations []aidevopsAssociation, total int, err error) {
	for _, space := range spaces {
		p := devopsagent.NewListAssociationsPaginator(client, &devopsagent.ListAssociationsInput{AgentSpaceId: &space.id})
		for p.HasMorePages() {
			page, perr := p.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "aidevops:ListAssociations", acct.ID, region, perr)
					break
				}
				return associations, total, fmt.Errorf("aidevops:ListAssociations %s: %w", space.id, perr)
			}
			batch := make([]*store.Resource, 0, len(page.Associations))
			listed := make([]aidevopsAssociation, 0, len(page.Associations))
			for _, a := range page.Associations {
				id := sv(a.AssociationId)
				if id == "" {
					continue
				}
				nativeID := aidevopsARN(acct.partition(), region, acct.ID, "association", id)
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
			t, uerr := upsertBatch(st, batch, "aidevops associations")
			total += t
			if uerr != nil {
				return associations, total, uerr
			}
			associations = append(associations, listed...)
		}
	}
	return associations, total, nil
}

// scanAidevopsPrivateConnections stores the private connections with their
// tags. ListPrivateConnections is a single call: the model gives it no
// pagination. It reports whether the write failed, after which the scan must
// stop.
func scanAidevopsPrivateConnections(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, aidevopsListPrivateConnectionsOp,
		func(ctx context.Context, _ *string) ([]devopsagenttypes.PrivateConnectionSummary, *string, error) {
			out, err := client.ListPrivateConnections(ctx, &devopsagent.ListPrivateConnectionsInput{})
			if err != nil {
				return nil, nil, err
			}
			return out.PrivateConnections, nil, nil
		},
		func(c devopsagenttypes.PrivateConnectionSummary) (*store.Resource, string, string) {
			name := sv(c.Name)
			if name == "" {
				return nil, "private connections have no Name", cmp.Or(sv(c.HostAddress), noIdentifier)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAidevopsPrivateConnection, NativeID: aidevopsARN(acct.partition(), region, acct.ID, "private-connection", name),
				Name: &name, Status: nonEmptyPtr(string(c.Status)), Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			}, "", ""
		},
		tags, p)
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
func scanAidevopsAssets(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, error) {
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
func scanAidevopsTriggers(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, error) {
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
func scanAidevopsGoals(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, spaces []childParent) (int, error) {
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
func scanAidevopsWebhooks(ctx context.Context, client aidevopsAPI, acct *account, region string, st *store.Store, scanID string, associations []aidevopsAssociation) (int, error) {
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
