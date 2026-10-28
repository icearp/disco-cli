package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
	qbtypes "github.com/aws/aws-sdk-go-v2/service/qbusiness/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeQBusinessApplication, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessChatControlsConfiguration, Service: "qbusiness", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQBusinessChatResponseConfiguration, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessDataAccessor, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessDataSource, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessIndex, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessPlugin, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessRetriever, Service: "qbusiness"})
	registerType(restype.Descriptor{Type: TypeQBusinessSubscription, Service: "qbusiness", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeQBusinessWebExperience, Service: "qbusiness"})
	registerService(serviceEntry{
		name: "aws:qbusiness",
		fn:   scanQBusiness,
	})
}

type qbusinessAPI interface {
	GetChatControlsConfiguration(context.Context, *qbusiness.GetChatControlsConfigurationInput, ...func(*qbusiness.Options)) (*qbusiness.GetChatControlsConfigurationOutput, error)
	ListApplications(context.Context, *qbusiness.ListApplicationsInput, ...func(*qbusiness.Options)) (*qbusiness.ListApplicationsOutput, error)
	ListChatResponseConfigurations(context.Context, *qbusiness.ListChatResponseConfigurationsInput, ...func(*qbusiness.Options)) (*qbusiness.ListChatResponseConfigurationsOutput, error)
	ListSubscriptions(context.Context, *qbusiness.ListSubscriptionsInput, ...func(*qbusiness.Options)) (*qbusiness.ListSubscriptionsOutput, error)
	ListDataAccessors(context.Context, *qbusiness.ListDataAccessorsInput, ...func(*qbusiness.Options)) (*qbusiness.ListDataAccessorsOutput, error)
	ListDataSources(context.Context, *qbusiness.ListDataSourcesInput, ...func(*qbusiness.Options)) (*qbusiness.ListDataSourcesOutput, error)
	ListIndices(context.Context, *qbusiness.ListIndicesInput, ...func(*qbusiness.Options)) (*qbusiness.ListIndicesOutput, error)
	ListPlugins(context.Context, *qbusiness.ListPluginsInput, ...func(*qbusiness.Options)) (*qbusiness.ListPluginsOutput, error)
	ListRetrievers(context.Context, *qbusiness.ListRetrieversInput, ...func(*qbusiness.Options)) (*qbusiness.ListRetrieversOutput, error)
	ListTagsForResource(context.Context, *qbusiness.ListTagsForResourceInput, ...func(*qbusiness.Options)) (*qbusiness.ListTagsForResourceOutput, error)
	ListWebExperiences(context.Context, *qbusiness.ListWebExperiencesInput, ...func(*qbusiness.Options)) (*qbusiness.ListWebExperiencesOutput, error)
}

const (
	qbTagsOp         = "qbusiness:ListTagsForResource"
	qbChatControlsOp = "qbusiness:GetChatControlsConfiguration"
	// qbNoIdentifier names, in a warning, a listed element with neither an id
	// nor a display name.
	qbNoIdentifier = "(an element with no identifier)"
)

// qbNotFound is the not-found code ListTagsForResource models, and the one
// every child listing read here models for an application or index deleted
// since it was listed.
var qbNotFound = []string{"ResourceNotFoundException"}

// qbARN builds arn:{p}:qbusiness:{region}:{acct}:{seg1}/{seg2}/….
func qbARN(partition, region, acct string, segments ...string) string {
	return arnFor(partition, "qbusiness", region, acct, strings.Join(segments, "/"))
}

// scanQBusiness discovers the Amazon Q Business resources of one region.
// Every listing runs as its own phase, one at a time (runHostPhases): a
// phase's error does not stop the later ones; a failed write and ctx's end
// do. The child phases read the parents stored this scan, those parents
// concurrently, so one parent's failure costs the others none of their rows.
// Every type but subscriptions and chat controls, which are not taggable, has
// its tags read per row (qbTags), whose denials are one warning for the whole
// scan.
func scanQBusiness(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := qbusiness.NewFromConfig(acct.cfg, func(o *qbusiness.Options) { o.Region = region })
	tags := qbTags(client).sharingDenials()
	var (
		apps    []qbAppRef
		indices []qbIndex
	)
	total, inserted, err := runHostPhases(ctx, st, acct, region, "qbusiness", 0, []hostPhase{
		{op: "qbusiness:ListApplications", run: func(p *phaseTotals) (failed bool) {
			apps, failed = scanQBApplications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "qbusiness:ListDataAccessors", run: func(p *phaseTotals) bool {
			return scanQBDataAccessors(ctx, client, acct, region, st, scanID, apps, tags, p)
		}},
		{op: "qbusiness:ListIndices", run: func(p *phaseTotals) (failed bool) {
			indices, failed = scanQBIndices(ctx, client, acct, region, st, scanID, apps, tags, p)
			return failed
		}},
		{op: "qbusiness:ListPlugins", run: func(p *phaseTotals) bool {
			return scanQBPlugins(ctx, client, acct, region, st, scanID, apps, tags, p)
		}},
		{op: "qbusiness:ListRetrievers", run: func(p *phaseTotals) bool {
			return scanQBRetrievers(ctx, client, acct, region, st, scanID, apps, tags, p)
		}},
		{op: "qbusiness:ListWebExperiences", run: func(p *phaseTotals) bool {
			return scanQBWebExperiences(ctx, client, acct, region, st, scanID, apps, tags, p)
		}},
		{op: "qbusiness:ListChatResponseConfigurations", run: func(p *phaseTotals) bool {
			return scanQBChatResponseConfigurations(ctx, client, acct, region, st, scanID, apps, tags, p)
		}},
		countedPhase(0, "qbusiness:ListSubscriptions", func() (int, int, error) {
			return scanQBSubscriptions(ctx, client, acct, region, st, scanID, apps)
		}),
		{op: "qbusiness:ListDataSources", run: func(p *phaseTotals) bool {
			return scanQBDataSources(ctx, client, acct, region, st, scanID, indices, tags, p)
		}},
		{op: qbChatControlsOp, run: func(p *phaseTotals) bool {
			return scanQBChatControlsConfigurations(ctx, client, acct, region, st, scanID, apps, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// qbTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN ListDataAccessors returned for a data accessor, and for
// every other type the ARN built in the Service Reference's format for it
// (application/{ApplicationId}, a child kind's
// application/{ApplicationId}/{kind}/{Id}, a data source's
// application/{ApplicationId}/index/{IndexId}/data-source/{DataSourceId}).
// A chat response configuration sends its listed ARN instead, when it has
// one (scanQBChatResponseConfigurations). The Service Reference lists every
// type read here under ListTagsForResource, and each one's Create op takes
// Tags. The op does not page its tags. ResourceNotFoundException, its
// modeled not-found, drops a resource deleted after it was listed; for an
// application that drops its children too, which are then not listed.
// Whether an application that is not ACTIVE (CREATING, FAILED) can answer
// not-found while it exists is unverified.
func qbTags(client qbusinessAPI) rowTagReader {
	return rowTagReader{
		op:       qbTagsOp,
		notFound: qbNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			return qbReadTags(ctx, client, r.NativeID)
		},
	}
}

// qbReadTags is the tags ListTagsForResource returns for arn.
func qbReadTags(ctx context.Context, client qbusinessAPI, arn string) (*string, error) {
	out, err := client.ListTagsForResource(ctx, &qbusiness.ListTagsForResourceInput{ResourceARN: &arn})
	if err != nil {
		return nil, err
	}
	return kvTagsJSON(out.Tags, func(t qbtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
}

// qbRow is the row of a listed element stored under nativeID and named name.
func qbRow(acct *account, region, scanID, typ, nativeID, name string, element any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: &name, Region: &region, AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}
}

// qbNoID is the not-stored result of an element without the id its NativeID
// is built from: one empty id would merge every such element into one
// resource. what names the elements and the missing field, name the element.
func qbNoID(what string, name *string) (*store.Resource, string, string) {
	return nil, what, cmp.Or(sv(name), qbNoIdentifier)
}

// qbAppRef is a stored application: its id, which its child listings key
// on, its ARN, and its status, which decides whether its chat controls are
// read.
type qbAppRef struct {
	id, arn string
	status  qbtypes.ApplicationStatus
}

// qbAppParents is apps as the parents of their child listings.
func qbAppParents(apps []qbAppRef) []childParent {
	parents := make([]childParent, len(apps))
	for i, a := range apps {
		parents[i] = childParent{id: a.id, arn: a.arn}
	}
	return parents
}

// scanQBApplications stores the applications with their tags and returns the
// stored ones as the parents of the later phases. The listing is held whole
// until one write, as it was before tags were read. It reports whether the
// write failed, after which the scan must stop.
func scanQBApplications(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]qbAppRef, bool) {
	status := map[string]qbtypes.ApplicationStatus{}
	parents, failed := storeParentListTags(ctx, st, acct, region, "qbusiness:ListApplications", "applications", "ApplicationId",
		func(ctx context.Context, token *string) ([]qbtypes.Application, *string, error) {
			out, err := client.ListApplications(ctx, &qbusiness.ListApplicationsInput{MaxResults: sdkaws.Int32(100), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Applications, out.NextToken, nil
		},
		func(a qbtypes.Application) (*store.Resource, string, string) {
			id := sv(a.ApplicationId)
			if id == "" {
				return qbNoID("applications have no ApplicationId", a.DisplayName)
			}
			status[id] = a.Status
			return qbRow(acct, region, scanID, TypeQBusinessApplication, qbARN(acct.partition(), region, acct.ID, "application", id), cmp.Or(sv(a.DisplayName), id), a), "", ""
		},
		func(a qbtypes.Application) string { return sv(a.ApplicationId) },
		tags, p)
	apps := make([]qbAppRef, len(parents))
	for i, pr := range parents {
		apps[i] = qbAppRef{id: pr.id, arn: pr.arn, status: status[pr.id]}
	}
	return apps, failed
}

// storeQBAppChildren stores, with their tags, the children op lists for each
// application in apps, and returns the rows stored. row gets the
// application's id and ARN. One application's failure costs the others none of
// theirs: an application deleted since it was listed is skipped, a denial
// warns once, and any other error is returned after the rows are stored.
// Every application's children are held until one write; before tags were
// read each application's whole listing was held too.
func storeQBAppChildren[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, apps []qbAppRef,
	page func(ctx context.Context, appID string, token *string) ([]Item, *string, error),
	row func(appID, appARN string, it Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) ([]*store.Resource, bool) {
	return storeEachParentTags(ctx, st, acct, region, op, qbNotFound, qbAppParents(apps), page,
		func(appID string, it Item) (*store.Resource, string, string) {
			return row(appID, qbARN(acct.partition(), region, acct.ID, "application", appID), it)
		},
		tags, p)
}

// scanQBDataAccessors stores each application's data accessors, keyed by the
// ARN their listing returns.
func scanQBDataAccessors(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeQBAppChildren(ctx, st, acct, region, "qbusiness:ListDataAccessors", apps,
		func(ctx context.Context, appID string, token *string) ([]qbtypes.DataAccessor, *string, error) {
			out, err := client.ListDataAccessors(ctx, &qbusiness.ListDataAccessorsInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(10), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataAccessors, out.NextToken, nil
		},
		func(_, _ string, d qbtypes.DataAccessor) (*store.Resource, string, string) {
			arn := sv(d.DataAccessorArn)
			if arn == "" {
				return nil, "data accessors have no DataAccessorArn", cmp.Or(sv(d.DisplayName), sv(d.DataAccessorId), qbNoIdentifier)
			}
			return qbRow(acct, region, scanID, TypeQBusinessDataAccessor, arn, cmp.Or(sv(d.DisplayName), arn), d), "", ""
		},
		tags, p)
	return failed
}

// qbIndex is a stored index: its application's id, its own id, and its ARN,
// which its data source listing is keyed by.
type qbIndex struct{ appID, id, arn string }

// scanQBIndices stores each application's indices and returns the stored
// ones, the parents of the data source phase.
func scanQBIndices(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef, tags rowTagReader, p *phaseTotals) ([]qbIndex, bool) {
	ids := map[*store.Resource]qbIndex{}
	stored, failed := storeQBAppChildren(ctx, st, acct, region, "qbusiness:ListIndices", apps,
		func(ctx context.Context, appID string, token *string) ([]qbtypes.Index, *string, error) {
			out, err := client.ListIndices(ctx, &qbusiness.ListIndicesInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(100), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Indices, out.NextToken, nil
		},
		func(appID, appARN string, idx qbtypes.Index) (*store.Resource, string, string) {
			id := sv(idx.IndexId)
			if id == "" {
				return qbNoID("indices have no IndexId", idx.DisplayName)
			}
			arn := appARN + "/index/" + id
			r := qbRow(acct, region, scanID, TypeQBusinessIndex, arn, cmp.Or(sv(idx.DisplayName), id), idx)
			ids[r] = qbIndex{appID: appID, id: id, arn: arn}
			return r, "", ""
		},
		tags, p)
	indices := make([]qbIndex, len(stored))
	for i, r := range stored {
		indices[i] = ids[r]
	}
	return indices, failed
}

func scanQBPlugins(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeQBAppChildren(ctx, st, acct, region, "qbusiness:ListPlugins", apps,
		func(ctx context.Context, appID string, token *string) ([]qbtypes.Plugin, *string, error) {
			out, err := client.ListPlugins(ctx, &qbusiness.ListPluginsInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(50), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Plugins, out.NextToken, nil
		},
		func(_, appARN string, pl qbtypes.Plugin) (*store.Resource, string, string) {
			id := sv(pl.PluginId)
			if id == "" {
				return qbNoID("plugins have no PluginId", pl.DisplayName)
			}
			return qbRow(acct, region, scanID, TypeQBusinessPlugin, appARN+"/plugin/"+id, cmp.Or(sv(pl.DisplayName), id), pl), "", ""
		},
		tags, p)
	return failed
}

func scanQBRetrievers(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeQBAppChildren(ctx, st, acct, region, "qbusiness:ListRetrievers", apps,
		func(ctx context.Context, appID string, token *string) ([]qbtypes.Retriever, *string, error) {
			out, err := client.ListRetrievers(ctx, &qbusiness.ListRetrieversInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(50), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Retrievers, out.NextToken, nil
		},
		func(_, appARN string, r qbtypes.Retriever) (*store.Resource, string, string) {
			id := sv(r.RetrieverId)
			if id == "" {
				return qbNoID("retrievers have no RetrieverId", r.DisplayName)
			}
			return qbRow(acct, region, scanID, TypeQBusinessRetriever, appARN+"/retriever/"+id, cmp.Or(sv(r.DisplayName), id), r), "", ""
		},
		tags, p)
	return failed
}

// scanQBWebExperiences stores each application's web experiences, named by
// their id: the listing carries no display name.
func scanQBWebExperiences(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef, tags rowTagReader, p *phaseTotals) bool {
	_, failed := storeQBAppChildren(ctx, st, acct, region, "qbusiness:ListWebExperiences", apps,
		func(ctx context.Context, appID string, token *string) ([]qbtypes.WebExperience, *string, error) {
			out, err := client.ListWebExperiences(ctx, &qbusiness.ListWebExperiencesInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(100), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.WebExperiences, out.NextToken, nil
		},
		func(_, appARN string, w qbtypes.WebExperience) (*store.Resource, string, string) {
			id := sv(w.WebExperienceId)
			if id == "" {
				return qbNoID("web experiences have no WebExperienceId", w.DefaultEndpoint)
			}
			return qbRow(acct, region, scanID, TypeQBusinessWebExperience, appARN+"/web-experience/"+id, id, w), "", ""
		},
		tags, p)
	return failed
}

// scanQBChatResponseConfigurations stores each application's chat response
// configurations. The model gives this op's maxResults no range, so none is
// sent and the service picks the page size. The NativeID is built, as it was
// before tags were read; the tag read sends the ARN the listing returned
// (listed: written while the rows are built, read only by the tag reads that
// start after, and dropped with the phase), or the NativeID when the listing
// returned none.
func scanQBChatResponseConfigurations(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef, tags rowTagReader, p *phaseTotals) bool {
	listed := map[*store.Resource]string{}
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		return qbReadTags(ctx, client, cmp.Or(listed[r], r.NativeID))
	}
	_, failed := storeQBAppChildren(ctx, st, acct, region, "qbusiness:ListChatResponseConfigurations", apps,
		func(ctx context.Context, appID string, token *string) ([]qbtypes.ChatResponseConfiguration, *string, error) {
			out, err := client.ListChatResponseConfigurations(ctx, &qbusiness.ListChatResponseConfigurationsInput{ApplicationId: &appID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ChatResponseConfigurations, out.NextToken, nil
		},
		func(_, appARN string, c qbtypes.ChatResponseConfiguration) (*store.Resource, string, string) {
			id := sv(c.ChatResponseConfigurationId)
			if id == "" {
				return qbNoID("chat response configurations have no ChatResponseConfigurationId", c.DisplayName)
			}
			// Keyed off appARN so resolveQBusinessChildrenToApp can strip back
			// to the parent application ARN.
			r := qbRow(acct, region, scanID, TypeQBusinessChatResponseConfiguration, appARN+"/chat-response-configuration/"+id, cmp.Or(sv(c.DisplayName), id), c)
			listed[r] = sv(c.ChatResponseConfigurationArn)
			return r, "", ""
		},
		tags, p)
	return failed
}

// scanQBSubscriptions stores each application's subscriptions, which are not
// taggable, named by their id. One application's failure costs the others
// none of theirs. Every application's subscriptions are held until one
// write: a subscription is a few small fields.
func scanQBSubscriptions(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "qbusiness:ListSubscriptions", qbNotFound, childParentIDs(qbAppParents(apps)),
		func(ctx context.Context, appID string, token *string) ([]qbtypes.Subscription, *string, error) {
			out, err := client.ListSubscriptions(ctx, &qbusiness.ListSubscriptionsInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(100), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Subscriptions, out.NextToken, nil
		},
		func(appID string, s qbtypes.Subscription) (*store.Resource, string, string) {
			id := sv(s.SubscriptionId)
			if id == "" {
				return nil, "subscriptions have no SubscriptionId", cmp.Or(sv(s.SubscriptionArn), qbNoIdentifier)
			}
			appARN := qbARN(acct.partition(), region, acct.ID, "application", appID)
			return qbRow(acct, region, scanID, TypeQBusinessSubscription, appARN+"/subscription/"+id, id, s), "", ""
		})
}

// scanQBDataSources stores each stored index's data sources, keyed under
// the index's ARN. One index's failure costs the others none of theirs.
func scanQBDataSources(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, indices []qbIndex, tags rowTagReader, p *phaseTotals) bool {
	parents := make([]childParent, len(indices))
	byARN := make(map[string]qbIndex, len(indices))
	for i, idx := range indices {
		parents[i] = childParent{id: idx.arn, arn: idx.arn}
		byARN[idx.arn] = idx
	}
	_, failed := storeEachParentTags(ctx, st, acct, region, "qbusiness:ListDataSources", qbNotFound, parents,
		func(ctx context.Context, indexARN string, token *string) ([]qbtypes.DataSource, *string, error) {
			idx := byARN[indexARN]
			out, err := client.ListDataSources(ctx, &qbusiness.ListDataSourcesInput{ApplicationId: &idx.appID, IndexId: &idx.id, MaxResults: sdkaws.Int32(10), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataSources, out.NextToken, nil
		},
		func(indexARN string, ds qbtypes.DataSource) (*store.Resource, string, string) {
			id := sv(ds.DataSourceId)
			if id == "" {
				return qbNoID("data sources have no DataSourceId", ds.DisplayName)
			}
			return qbRow(acct, region, scanID, TypeQBusinessDataSource, indexARN+"/data-source/"+id, cmp.Or(sv(ds.DisplayName), id), ds), "", ""
		},
		tags, p)
	return failed
}

// qbChatControlsNativeID keys an application's chat controls, which have no id
// of their own (one set per application). An application ARN ends in the bare
// application id, and every other child kind adds two or more segments, so
// this single-segment suffix collides with neither.
func qbChatControlsNativeID(appARN string) string {
	return appARN + "/chat-controls-configuration"
}

// scanQBChatControlsConfigurations stores each application's chat controls,
// the applications read concurrently (storeTokenEachParentSkip), so one
// application's failure costs no other its row. FAILED applications are not
// queried. That gate is inferred from the model's status doc (an application
// is ready to use only when ACTIVE, and FAILED is the only status that is
// neither ACTIVE nor a transition); the op documents no error for a FAILED
// application. ResourceNotFoundException, which the op models, means the
// application was deleted after it was listed. An application still failing
// transiently after the SDK's retries has no row to keep: every such
// application is one error, kept apart from the others so that phaseErrs
// demotes it beside a hard error. It reports whether the write failed, after
// which the scan must stop.
func scanQBChatControlsConfigurations(ctx context.Context, client qbusinessAPI, acct *account, region string, st *store.Store, scanID string, apps []qbAppRef, p *phaseTotals) bool {
	var ready []string
	for _, a := range apps {
		if a.status != qbtypes.ApplicationStatusFailed {
			ready = append(ready, a.id)
		}
	}
	var transients transientTally
	total, inserted, err := storeTokenEachParentSkip(ctx, st, acct, region, qbChatControlsOp, qbNotFound, ready,
		func(ctx context.Context, appID string, _ *string) ([]*qbusiness.GetChatControlsConfigurationOutput, *string, error) {
			cfg, err := getQBChatControls(ctx, client, appID)
			if err != nil {
				return nil, nil, err
			}
			return []*qbusiness.GetChatControlsConfigurationOutput{cfg}, nil, nil
		},
		func(appID string, cfg *qbusiness.GetChatControlsConfigurationOutput) (*store.Resource, string, string) {
			appARN := qbARN(acct.partition(), region, acct.ID, "application", appID)
			return qbRow(acct, region, scanID, TypeQBusinessChatControlsConfiguration, qbChatControlsNativeID(appARN), appID, cfg), "", ""
		},
		transients.dropUnlessNXDOMAIN)
	var transient error
	if t := &transients.skipTally; t.count > 0 {
		transient = fmt.Errorf("%s: %d application(s) not read, first %s: %w", qbChatControlsOp, t.count, t.first, t.err)
	}
	if errors.Is(err, store.ErrStoreWrite) {
		return p.addPhase(qbChatControlsOp, total, inserted, errors.Join(err, transient))
	}
	p.errs.add(qbChatControlsOp, transient)
	return p.addPhase(qbChatControlsOp, total, inserted, err)
}

// getQBChatControls reads every page of an application's chat controls. Only
// TopicConfigurations is paged (50 a page, the model's maximum); the first
// page carries the other settings, and the page token is dropped so a
// multi-page read stores the same shape as a single-page one. A repeated
// page token is errRepeatedNextToken: the topics would be incomplete.
func getQBChatControls(ctx context.Context, client qbusinessAPI, appID string) (*qbusiness.GetChatControlsConfigurationOutput, error) {
	var cfg *qbusiness.GetChatControlsConfigurationOutput
	topics, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]qbtypes.TopicConfiguration, *string, error) {
		out, err := client.GetChatControlsConfiguration(ctx, &qbusiness.GetChatControlsConfigurationInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(50), NextToken: token})
		if err != nil {
			return nil, nil, err
		}
		if cfg == nil {
			cfg = out
		}
		return out.TopicConfigurations, out.NextToken, nil
	})
	if err != nil {
		return nil, err
	}
	cfg.TopicConfigurations = topics
	cfg.NextToken = nil
	return cfg, nil
}
