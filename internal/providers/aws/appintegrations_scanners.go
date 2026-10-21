package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/appintegrations"
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
}

func scanAppIntegrations(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := appintegrations.NewFromConfig(acct.cfg, func(o *appintegrations.Options) { o.Region = region })
	return scanAppIntegrationsAll(ctx, client, acct, region, st, scanID)
}

// scanAppIntegrationsAll runs the parent phases, which stop at their first hard
// error, then the association phases over the parents stored before that
// error, so a failure on either side costs the other no rows. The parent
// side's transient error is demoted to a warning: one transient member would
// make the dispatcher read the whole join as transient, reporting an
// association phase's hard error as a transient one.
func scanAppIntegrationsAll(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, parents, err := scanAppIntegrationsParents(ctx, client, acct, region, st, scanID)
	t, i, aerr := scanAppIntegrationsAssociations(ctx, client, acct, region, st, scanID, parents)
	return total + t, inserted + i, errors.Join(demoteTransient(st, "appintegrations", acct, region, err), aerr)
}

// appIntegrationsParents holds, per association list op, the parents stored
// this scan, keyed by the identifier that op takes.
type appIntegrationsParents struct {
	applications, dataIntegrations, eventIntegrations []childParent
}

// scanAppIntegrationsParents runs the application, data-integration and
// event-integration phases in order, stopping at the first hard error, and
// returns the parents stored before it.
func scanAppIntegrationsParents(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, parents appIntegrationsParents, err error) {
	var t, i int
	parents.applications, t, i, err = scanAppIntegrationsApplications(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	if err != nil {
		return total, inserted, parents, err
	}
	parents.dataIntegrations, t, i, err = scanAppIntegrationsDataIntegrations(ctx, client, acct, region, st, scanID)
	total, inserted = total+t, inserted+i
	if err != nil {
		return total, inserted, parents, err
	}
	parents.eventIntegrations, t, i, err = scanAppIntegrationsEventIntegrations(ctx, client, acct, region, st, scanID)
	return total + t, inserted + i, parents, err
}

// scanAppIntegrationsApplications stores the applications and returns them as
// association parents, keyed by ARN: ListApplicationAssociations takes an
// ApplicationId whose model pattern accepts an ARN or the UUID.
func scanAppIntegrationsApplications(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (parents []childParent, total, inserted int, err error) {
	p := appintegrations.NewListApplicationsPaginator(client, &appintegrations.ListApplicationsInput{},
		func(o *appintegrations.ListApplicationsPaginatorOptions) {
			o.Limit = appIntegrationsPageLimit
			o.StopOnDuplicateToken = true
		})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return parents, total, inserted, skipIfAccessDenied(st, "appintegrations:ListApplications", acct.ID, region, err)
			}
			return parents, total, inserted, fmt.Errorf("appintegrations:ListApplications: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.Applications))
		var pageParents []childParent
		for _, a := range page.Applications {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			name := sv(a.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppIntegrationsApplication,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				CreatedAt:      tp(a.CreatedTime),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
			})
			pageParents = append(pageParents, childParent{id: arn, arn: arn})
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return parents, total, inserted, fmt.Errorf("upsert appintegrations applications: %w", err)
			}
			total += len(batch)
			inserted += n
			parents = append(parents, pageParents...)
		}
	}
	return parents, total, inserted, nil
}

// scanAppIntegrationsDataIntegrations stores the data integrations and returns
// them as association parents, keyed by the data integration's id.
// ListDataIntegrationAssociations takes a DataIntegrationIdentifier the model
// documents only as "A unique identifier for the DataIntegration" (pattern
// \S); neither it nor the AWS CLI docs say whether an ARN is accepted, and
// GetDataIntegration returns the Id as a UUID. The summary has no Id field, so
// the id is the ARN's data-integration/<id> resource segment. A data
// integration whose ARN lacks that segment is stored, but its associations
// are not listed, and one warning names the count and the first.
func scanAppIntegrationsDataIntegrations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (parents []childParent, total, inserted int, err error) {
	var noID skipTally
	defer func() {
		if noID.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: appIntegrationsListDataIntegrationAssociationsOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("associations of %d data integration(s) not listed: ARN has no data-integration/<id> segment, first %s", noID.count, noID.first),
			})
		}
	}()
	p := appintegrations.NewListDataIntegrationsPaginator(client, &appintegrations.ListDataIntegrationsInput{},
		func(o *appintegrations.ListDataIntegrationsPaginatorOptions) {
			o.Limit = appIntegrationsPageLimit
			o.StopOnDuplicateToken = true
		})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return parents, total, inserted, skipIfAccessDenied(st, "appintegrations:ListDataIntegrations", acct.ID, region, err)
			}
			return parents, total, inserted, fmt.Errorf("appintegrations:ListDataIntegrations: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.DataIntegrations))
		var pageParents []childParent
		for _, d := range page.DataIntegrations {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppIntegrationsDataIntegration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(d),
				DiscoveredBy:   scanID,
			})
			if id, ok := dataIntegrationIDFromARN(arn); ok {
				pageParents = append(pageParents, childParent{id: id, arn: arn})
			} else {
				noID.skipped(arn, nil)
			}
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return parents, total, inserted, fmt.Errorf("upsert appintegrations data-integrations: %w", err)
			}
			total += len(batch)
			inserted += n
			parents = append(parents, pageParents...)
		}
	}
	return parents, total, inserted, nil
}

// dataIntegrationIDFromARN returns the <id> of an
// arn:...:data-integration/<id> ARN.
func dataIntegrationIDFromARN(arn string) (string, bool) {
	_, id, ok := strings.Cut(arn, ":data-integration/")
	return id, ok && id != ""
}

// scanAppIntegrationsEventIntegrations stores the event integrations and
// returns them as association parents, keyed by name, the only identifier
// ListEventIntegrationAssociations takes. One without a name is stored but has
// no association listing.
func scanAppIntegrationsEventIntegrations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string) (parents []childParent, total, inserted int, err error) {
	p := appintegrations.NewListEventIntegrationsPaginator(client, &appintegrations.ListEventIntegrationsInput{},
		func(o *appintegrations.ListEventIntegrationsPaginatorOptions) {
			o.Limit = appIntegrationsPageLimit
			o.StopOnDuplicateToken = true
		})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return parents, total, inserted, skipIfAccessDenied(st, "appintegrations:ListEventIntegrations", acct.ID, region, err)
			}
			return parents, total, inserted, fmt.Errorf("appintegrations:ListEventIntegrations: %w", err)
		}
		batch := make([]*store.Resource, 0, len(page.EventIntegrations))
		var pageParents []childParent
		for _, e := range page.EventIntegrations {
			arn := sv(e.EventIntegrationArn)
			if arn == "" {
				continue
			}
			name := sv(e.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeAppIntegrationsEventIntegration,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(e),
				TagsJSON:       mapTagsJSON(e.Tags),
				DiscoveredBy:   scanID,
			})
			if name != "" {
				pageParents = append(pageParents, childParent{id: name, arn: arn})
			}
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return parents, total, inserted, fmt.Errorf("upsert appintegrations event-integrations: %w", err)
			}
			total += len(batch)
			inserted += n
			parents = append(parents, pageParents...)
		}
	}
	return parents, total, inserted, nil
}

const (
	appIntegrationsListApplicationAssociationsOp      = "appintegrations:ListApplicationAssociations"
	appIntegrationsListDataIntegrationAssociationsOp  = "appintegrations:ListDataIntegrationAssociations"
	appIntegrationsListEventIntegrationAssociationsOp = "appintegrations:ListEventIntegrationAssociations"
)

// appIntegrationsPageLimit is the model's MaxResults cap; every list op this
// file calls takes the same MaxResults shape.
const appIntegrationsPageLimit = 50

// scanAppIntegrationsAssociations runs the association phases, each over
// its own parents; they are independent, so every phase runs and their errors
// are joined. Per parent: ResourceNotFoundException (parent deleted since
// listing) is skipped silently, a denial warns once per op, a transient error
// after the SDK's retries is skipped with one warning per op, and anything
// else fails that phase, storing none of its rows.
func scanAppIntegrationsAssociations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, parents appIntegrationsParents) (int, int, error) {
	var (
		total, inserted int
		errs            []error
	)
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanAppIntegrationsApplicationAssociations(ctx, client, acct, region, st, scanID, parents.applications)
		},
		func() (int, int, error) {
			return scanAppIntegrationsDataIntegrationAssociations(ctx, client, acct, region, st, scanID, parents.dataIntegrations)
		},
		func() (int, int, error) {
			return scanAppIntegrationsEventIntegrationAssociations(ctx, client, acct, region, st, scanID, parents.eventIntegrations)
		},
	} {
		t, i, err := phase()
		total += t
		inserted += i
		errs = append(errs, err)
	}
	return total, inserted, errors.Join(errs...)
}

func isAppIntegrationsParentGone(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// appIntegrationsAssociationRow builds an association row keyed by the ARN the
// API returned; associations carry no name, tags or timestamps.
func appIntegrationsAssociationRow(acct *account, region, scanID, typ, arn string, element any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn, Region: &region,
		AttributesJSON: mustJSON(element), DiscoveredBy: scanID,
	}
}

func scanAppIntegrationsApplicationAssociations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, appIntegrationsListApplicationAssociationsOp, apps, isAppIntegrationsParentGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := appintegrations.NewListApplicationAssociationsPaginator(client,
				&appintegrations.ListApplicationAssociationsInput{ApplicationId: &p.id},
				func(o *appintegrations.ListApplicationAssociationsPaginatorOptions) {
					o.Limit = appIntegrationsPageLimit
					o.StopOnDuplicateToken = true
				})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, a := range out.ApplicationAssociations {
					if arn := sv(a.ApplicationAssociationArn); arn != "" {
						rows = append(rows, appIntegrationsAssociationRow(acct, region, scanID, TypeAppIntegrationsApplicationAssociation, arn, a))
					}
				}
			}
			return rows, nil
		})
}

func scanAppIntegrationsDataIntegrationAssociations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, dataIntegrations []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, appIntegrationsListDataIntegrationAssociationsOp, dataIntegrations, isAppIntegrationsParentGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := appintegrations.NewListDataIntegrationAssociationsPaginator(client,
				&appintegrations.ListDataIntegrationAssociationsInput{DataIntegrationIdentifier: &p.id},
				func(o *appintegrations.ListDataIntegrationAssociationsPaginatorOptions) {
					o.Limit = appIntegrationsPageLimit
					o.StopOnDuplicateToken = true
				})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, d := range out.DataIntegrationAssociations {
					if arn := sv(d.DataIntegrationAssociationArn); arn != "" {
						rows = append(rows, appIntegrationsAssociationRow(acct, region, scanID, TypeAppIntegrationsDataIntegrationAssociation, arn, d))
					}
				}
			}
			return rows, nil
		})
}

func scanAppIntegrationsEventIntegrationAssociations(ctx context.Context, client appintegrationsAPI, acct *account, region string, st *store.Store, scanID string, eventIntegrations []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, appIntegrationsListEventIntegrationAssociationsOp, eventIntegrations, isAppIntegrationsParentGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			pager := appintegrations.NewListEventIntegrationAssociationsPaginator(client,
				&appintegrations.ListEventIntegrationAssociationsInput{EventIntegrationName: &p.id},
				func(o *appintegrations.ListEventIntegrationAssociationsPaginatorOptions) {
					o.Limit = appIntegrationsPageLimit
					o.StopOnDuplicateToken = true
				})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, e := range out.EventIntegrationAssociations {
					if arn := sv(e.EventIntegrationAssociationArn); arn != "" {
						rows = append(rows, appIntegrationsAssociationRow(acct, region, scanID, TypeAppIntegrationsEventIntegrationAssociation, arn, e))
					}
				}
			}
			return rows, nil
		})
}
