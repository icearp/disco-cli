package aws

import (
	"cmp"
	"context"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appintegrations"
	aitypes "github.com/aws/aws-sdk-go-v2/service/appintegrations/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAppIntegrationsApplication, Service: "appintegrations"})
	registerType(restype.Descriptor{Type: TypeAppIntegrationsApplicationAssociation, Service: "appintegrations"})
	registerType(restype.Descriptor{Type: TypeAppIntegrationsDataIntegration, Service: "appintegrations"})
	registerType(restype.Descriptor{Type: TypeAppIntegrationsDataIntegrationAssociation, Service: "appintegrations"})
	registerType(restype.Descriptor{Type: TypeAppIntegrationsEventIntegration, Service: "appintegrations"})
	registerType(restype.Descriptor{Type: TypeAppIntegrationsEventIntegrationAssociation, Service: "appintegrations"})
	registerService(serviceEntry{
		name: "aws:appintegrations",
		fn:   scanAppIntegrations,
	})
}

type appintegrationsAPI interface {
	ListApplications(context.Context, *appintegrations.ListApplicationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListApplicationsOutput, error)
	ListApplicationAssociations(context.Context, *appintegrations.ListApplicationAssociationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListApplicationAssociationsOutput, error)
	ListDataIntegrations(context.Context, *appintegrations.ListDataIntegrationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListDataIntegrationsOutput, error)
	ListDataIntegrationAssociations(context.Context, *appintegrations.ListDataIntegrationAssociationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListDataIntegrationAssociationsOutput, error)
	ListEventIntegrations(context.Context, *appintegrations.ListEventIntegrationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListEventIntegrationsOutput, error)
	ListEventIntegrationAssociations(context.Context, *appintegrations.ListEventIntegrationAssociationsInput, ...func(*appintegrations.Options)) (*appintegrations.ListEventIntegrationAssociationsOutput, error)
	ListTagsForResource(context.Context, *appintegrations.ListTagsForResourceInput, ...func(*appintegrations.Options)) (*appintegrations.ListTagsForResourceOutput, error)
}

const (
	appIntegrationsTagsOp                             = "appintegrations:ListTagsForResource"
	appIntegrationsListApplicationsOp                 = "appintegrations:ListApplications"
	appIntegrationsListDataIntegrationsOp             = "appintegrations:ListDataIntegrations"
	appIntegrationsListEventIntegrationsOp            = "appintegrations:ListEventIntegrations"
	appIntegrationsListApplicationAssociationsOp      = "appintegrations:ListApplicationAssociations"
	appIntegrationsListDataIntegrationAssociationsOp  = "appintegrations:ListDataIntegrationAssociations"
	appIntegrationsListEventIntegrationAssociationsOp = "appintegrations:ListEventIntegrationAssociations"
)

// appIntegrationsPageLimit is the model's MaxResults cap; every list op this
// file calls takes the same MaxResults shape.
const appIntegrationsPageLimit = 50

// appIntegrationsGone is the not-found code ListTagsForResource and every
// association listing model, for a resource deleted since it was listed.
var appIntegrationsGone = []string{"ResourceNotFoundException"}

// scanAppIntegrations discovers the applications, data integrations and
// event integrations of one region, then the associations of each one
// stored. Every listing runs as its own phase, one at a time: a phase's
// error does not stop the later ones; a failed write and ctx's end do
// (runHostPhases). Every type but the event integration, whose listed
// element carries its tags, has its tags read per row (appIntegrationsTags),
// whose denials are one warning for the whole scan.
func scanAppIntegrations(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appintegrations.NewFromConfig(acct.cfg, func(o *appintegrations.Options) { o.Region = region })
	tags := appIntegrationsTags(client).sharingDenials()
	var apps, dataIntegrations, eventIntegrations []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "appintegrations", 0, []hostPhase{
		{op: appIntegrationsListApplicationsOp, run: func(p *phaseTotals) (failed bool) {
			apps, failed = scanAppIntegrationsApplications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: appIntegrationsListDataIntegrationsOp, run: func(p *phaseTotals) (failed bool) {
			dataIntegrations, failed = scanAppIntegrationsDataIntegrations(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		countedPhase(0, appIntegrationsListEventIntegrationsOp, func() (int, int, error) {
			total, inserted, parents, err := scanAppIntegrationsEventIntegrations(ctx, client, acct, region, st, scanID)
			eventIntegrations = parents
			return total, inserted, err
		}),
		{op: appIntegrationsListApplicationAssociationsOp, run: func(p *phaseTotals) bool {
			return scanAppIntegrationsApplicationAssociations(ctx, client, acct, region, st, scanID, apps, tags, p)
		}},
		{op: appIntegrationsListDataIntegrationAssociationsOp, run: func(p *phaseTotals) bool {
			return scanAppIntegrationsDataIntegrationAssociations(ctx, client, acct, region, st, scanID, dataIntegrations, tags, p)
		}},
		{op: appIntegrationsListEventIntegrationAssociationsOp, run: func(p *phaseTotals) bool {
			return scanAppIntegrationsEventIntegrationAssociations(ctx, client, acct, region, st, scanID, eventIntegrations, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// appIntegrationsTags reads a row's tags with ListTagsForResource, sent the
// row's NativeID, the ARN its listing returned. The Service Reference lists
// application, data-integration, data-integration-association and
// event-integration-association under ListTagsForResource. It omits
// application-association there, but lists it under TagResource and
// UntagResource, the op's model ARN pattern accepts its ARN, and no response
// carries an association's tags, so it is read too.
func appIntegrationsTags(client appintegrationsAPI) rowTagReader {
	return rowTagReader{
		op:       appIntegrationsTagsOp,
		notFound: appIntegrationsGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &appintegrations.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// appIntegrationsRow is the row of a listed element stored under the ARN the
// API returned. name may be empty: the row is named "" rather than unnamed.
func appIntegrationsRow(acct *account, region, scanID, typ, arn, name string, element any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn, Name: &name, Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}
}

// scanAppIntegrationsApplications stores the applications with their tags, a
// page at a time, and returns the stored ones as association parents, keyed
// by ARN: ListApplicationAssociations takes an ApplicationId whose model
// pattern accepts an ARN or the UUID. It reports whether a write failed,
// after which the scan must stop.
func scanAppIntegrationsApplications(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, appIntegrationsListApplicationsOp, "applications", "Arn",
		func(ctx context.Context, token *string) ([]aitypes.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &appintegrations.ListApplicationsInput{MaxResults: sdkaws.Int32(appIntegrationsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Applications, out.NextToken, nil
		},
		func(a aitypes.ApplicationSummary) (*store.Resource, string, string) {
			r := appIntegrationsRow(acct, region, scanID, TypeAppIntegrationsApplication, sv(a.Arn), sv(a.Name), a)
			r.CreatedAt = tp(a.CreatedTime)
			return arnRow("applications", a.Arn, a.Name, r)
		},
		func(a aitypes.ApplicationSummary) string { return sv(a.Arn) },
		tags, p)
}

// scanAppIntegrationsDataIntegrations stores the data integrations with their
// tags, a page at a time, and returns the stored ones as association
// parents, keyed by the data integration's id.
// ListDataIntegrationAssociations takes a DataIntegrationIdentifier the model
// documents only as "A unique identifier for the DataIntegration" (pattern
// \S); neither it nor the AWS CLI docs say whether an ARN is accepted, and
// GetDataIntegration returns the Id as a UUID. The summary has no Id field, so
// the id is the ARN's data-integration/<id> resource segment. A data
// integration whose ARN lacks that segment is stored, but its associations
// are not listed, and one warning names the count and the first.
func scanAppIntegrationsDataIntegrations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, appIntegrationsListDataIntegrationsOp, "data integrations", "data-integration/<id> segment in their ARN",
		func(ctx context.Context, token *string) ([]aitypes.DataIntegrationSummary, *string, error) {
			out, err := client.ListDataIntegrations(ctx, &appintegrations.ListDataIntegrationsInput{MaxResults: sdkaws.Int32(appIntegrationsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataIntegrations, out.NextToken, nil
		},
		func(d aitypes.DataIntegrationSummary) (*store.Resource, string, string) {
			return arnRow("data integrations", d.Arn, d.Name, appIntegrationsRow(acct, region, scanID, TypeAppIntegrationsDataIntegration, sv(d.Arn), sv(d.Name), d))
		},
		func(d aitypes.DataIntegrationSummary) string { return dataIntegrationIDFromARN(sv(d.Arn)) },
		tags, p)
}

// dataIntegrationIDFromARN returns the <id> of an
// arn:...:data-integration/<id> ARN, or "" when it has none.
func dataIntegrationIDFromARN(arn string) string {
	_, id, _ := strings.Cut(arn, ":data-integration/")
	return id
}

// scanAppIntegrationsEventIntegrations stores the event integrations, whose
// listed elements carry their tags, and returns the stored ones as
// association parents, keyed by name, the only identifier
// ListEventIntegrationAssociations takes. One without a name is stored
// without its associations, and warned about.
func scanAppIntegrationsEventIntegrations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeParentList(ctx, st, acct, region, appIntegrationsListEventIntegrationsOp, "event integrations", "Name",
		func(ctx context.Context, token *string) ([]aitypes.EventIntegration, *string, error) {
			out, err := client.ListEventIntegrations(ctx, &appintegrations.ListEventIntegrationsInput{MaxResults: sdkaws.Int32(appIntegrationsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EventIntegrations, out.NextToken, nil
		},
		func(e aitypes.EventIntegration) (*store.Resource, string, string) {
			r := appIntegrationsRow(acct, region, scanID, TypeAppIntegrationsEventIntegration, sv(e.EventIntegrationArn), sv(e.Name), e)
			r.TagsJSON = mapTagsJSON(e.Tags)
			return arnRow("event integrations", e.EventIntegrationArn, e.Name, r)
		},
		func(e aitypes.EventIntegration) string { return sv(e.Name) })
}

// appIntegrationsAssociationRow is the row of an association listed under
// parent, stored under the ARN the API returned; associations carry no name
// or timestamps. One without an ARN is not stored: one empty NativeID would
// merge every such association into one resource. The warning names it by
// its client and parent.
func appIntegrationsAssociationRow(acct *account, region, scanID, typ, what, parent string, arn, clientID *string, element any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have no ARN", "client " + cmp.Or(sv(clientID), noIdentifier) + " of " + parent
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}, "", ""
}

// scanAppIntegrationsApplicationAssociations stores each application's
// associations with their tags, under it (storeEachParentTagsUnder). It
// reports whether a write failed, after which the scan must stop.
func scanAppIntegrationsApplicationAssociations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentTagsUnder(ctx, st, acct, region, appIntegrationsListApplicationAssociationsOp, appIntegrationsGone, apps,
		func(ctx context.Context, app string, token *string) ([]aitypes.ApplicationAssociationSummary, *string, error) {
			out, err := client.ListApplicationAssociations(ctx, &appintegrations.ListApplicationAssociationsInput{ApplicationId: &app, MaxResults: sdkaws.Int32(appIntegrationsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ApplicationAssociations, out.NextToken, nil
		},
		func(app string, a aitypes.ApplicationAssociationSummary) (*store.Resource, string, string) {
			return appIntegrationsAssociationRow(acct, region, scanID, TypeAppIntegrationsApplicationAssociation, "application associations", app, a.ApplicationAssociationArn, a.ClientId, a)
		},
		tags, p)
}

// scanAppIntegrationsDataIntegrationAssociations stores each data
// integration's associations with their tags, under it
// (storeEachParentTagsUnder). It reports whether a write failed, after which
// the scan must stop.
func scanAppIntegrationsDataIntegrationAssociations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, dataIntegrations []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentTagsUnder(ctx, st, acct, region, appIntegrationsListDataIntegrationAssociationsOp, appIntegrationsGone, dataIntegrations,
		func(ctx context.Context, id string, token *string) ([]aitypes.DataIntegrationAssociationSummary, *string, error) {
			out, err := client.ListDataIntegrationAssociations(ctx, &appintegrations.ListDataIntegrationAssociationsInput{DataIntegrationIdentifier: &id, MaxResults: sdkaws.Int32(appIntegrationsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.DataIntegrationAssociations, out.NextToken, nil
		},
		func(id string, d aitypes.DataIntegrationAssociationSummary) (*store.Resource, string, string) {
			return appIntegrationsAssociationRow(acct, region, scanID, TypeAppIntegrationsDataIntegrationAssociation, "data integration associations", id, d.DataIntegrationAssociationArn, d.ClientId, d)
		},
		tags, p)
}

// scanAppIntegrationsEventIntegrationAssociations stores each event
// integration's associations with their tags, under it
// (storeEachParentTagsUnder). It reports whether a write failed, after which
// the scan must stop.
func scanAppIntegrationsEventIntegrationAssociations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, eventIntegrations []childParent, tags rowTagReader, p *phaseTotals) bool {
	return storeEachParentTagsUnder(ctx, st, acct, region, appIntegrationsListEventIntegrationAssociationsOp, appIntegrationsGone, eventIntegrations,
		func(ctx context.Context, name string, token *string) ([]aitypes.EventIntegrationAssociation, *string, error) {
			out, err := client.ListEventIntegrationAssociations(ctx, &appintegrations.ListEventIntegrationAssociationsInput{EventIntegrationName: &name, MaxResults: sdkaws.Int32(appIntegrationsPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.EventIntegrationAssociations, out.NextToken, nil
		},
		func(name string, e aitypes.EventIntegrationAssociation) (*store.Resource, string, string) {
			return appIntegrationsAssociationRow(acct, region, scanID, TypeAppIntegrationsEventIntegrationAssociation, "event integration associations", name, e.EventIntegrationAssociationArn, e.ClientId, e)
		},
		tags, p)
}
