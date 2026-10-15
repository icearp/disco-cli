package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/m2"
	m2types "github.com/aws/aws-sdk-go-v2/service/m2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeM2Application, Service: "m2"})
	registerType(restype.Descriptor{Type: TypeM2Environment, Service: "m2"})
	registerType(restype.Descriptor{Type: TypeM2Deployment, Service: "m2"})
	registerType(restype.Descriptor{Type: TypeM2Dataset, Service: "m2"})
	registerService(serviceEntry{
		name: "aws:m2",
		fn:   scanM2,
	})
}

type m2API interface {
	ListApplications(context.Context, *m2.ListApplicationsInput, ...func(*m2.Options)) (*m2.ListApplicationsOutput, error)
	ListEnvironments(context.Context, *m2.ListEnvironmentsInput, ...func(*m2.Options)) (*m2.ListEnvironmentsOutput, error)
	ListDeployments(context.Context, *m2.ListDeploymentsInput, ...func(*m2.Options)) (*m2.ListDeploymentsOutput, error)
	ListDataSets(context.Context, *m2.ListDataSetsInput, ...func(*m2.Options)) (*m2.ListDataSetsOutput, error)
}

// scanM2 discovers M2 (Mainframe Modernization) applications, environments,
// and per-application deployments and data sets. Applications and
// environments are keyed by their ARNs; deployments and data sets have none
// and are keyed by the parent application's ARN plus a path.
func scanM2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := m2.NewFromConfig(acct.cfg, func(o *m2.Options) { o.Region = region })
	return scanM2All(ctx, client, acct, region, st, scanID)
}

// scanM2All runs the older phases, then the data set phase over the
// applications they stored. When an older phase after the application phase
// fails, the data set phase still runs over every stored application; when
// the data set phase fails, the older phases' rows and counts are kept. The
// data set phase tallies its transient errors per application, so only the
// older phases' error needs demoting before the join.
func scanM2All(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, apps, err := scanM2OlderPhases(ctx, client, acct, region, st, scanID)
	td, id, derr := scanM2Datasets(ctx, client, acct, region, st, scanID, apps)
	return total + td, inserted + id, errors.Join(demoteTransient(st, "m2", acct, region, err), derr)
}

// scanM2OlderPhases runs the phases that predate the data set phase, stopping
// at the first hard error. It returns the applications scanM2Applications
// stored, or none when that phase failed.
func scanM2OlderPhases(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, apps []childParent, err error) {
	apps, t, i, ferr := scanM2Applications(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, nil, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanM2Environments(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, apps, ferr
	}
	total += t
	inserted += i

	for _, a := range apps {
		t, i, ferr = scanM2Deployments(ctx, client, acct, region, st, scanID, a.id, a.arn)
		if ferr != nil {
			return total, inserted, apps, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, apps, nil
}

func scanM2Applications(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := m2.NewListApplicationsPaginator(client, &m2.ListApplicationsInput{})
	var batch []*store.Resource
	var apps []childParent
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "m2:ListApplications", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("m2:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			arn := sv(a.ApplicationArn)
			id := sv(a.ApplicationId)
			if arn == "" || id == "" {
				continue
			}
			apps = append(apps, childParent{id: id, arn: arn})
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Application, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "m2 applications")
	return apps, t, i, err
}

func scanM2Environments(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := m2.NewListEnvironmentsPaginator(client, &m2.ListEnvironmentsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "m2:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("m2:ListEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			arn := sv(e.EnvironmentArn)
			if arn == "" {
				continue
			}
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Environment, NativeID: arn,
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "m2 environments")
}

func scanM2Deployments(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string, appID, appARN string) (int, int, error) {
	aid := appID
	pager := m2.NewListDeploymentsPaginator(client, &m2.ListDeploymentsInput{ApplicationId: &aid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "m2:ListDeployments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("m2:ListDeployments: %w", err)
		}
		for _, d := range out.Deployments {
			did := sv(d.DeploymentId)
			if did == "" {
				continue
			}
			arn := appARN + "/deployment/" + did
			label := did
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Deployment, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "m2 deployments")
}

const (
	m2ListDataSetsOp = "m2:ListDataSets"
	// m2DataSetsPageLimit is the model's maxResults cap for ListDataSets.
	m2DataSetsPageLimit = 2000
)

// m2DatasetNativeID keys a data set under its application: ListDataSets
// returns no ARN, and the Service Reference defines ARNs only for
// applications and environments. The name's model pattern (^\S{1,200}$)
// admits "/", so it goes last.
func m2DatasetNativeID(appARN, name string) string {
	return appARN + "/dataset/" + name
}

// scanM2Datasets stores each application's catalogued data sets, a page at a
// time because one application can hold many, and links each under its
// application. Per application: ResourceNotFoundException (deleted since
// listing) is skipped silently, a denial warns once, a transient error after
// the SDK's retries is skipped with one warning for all such applications,
// ValidationException and ConflictException are each skipped with one warning
// for all applications refused with that code, and anything else fails the
// phase, as does a ValidationException naming maxResults, which disco sets.
// Rows an application stored before its error stay and are counted.
//
// The model lists ValidationException and ConflictException for ListDataSets
// without saying when either is returned. Data sets live in the runtime
// environment an application is deployed to, and how the call answers for an
// application that is not deployed, or is creating, failed or deleting, has
// not been observed; failing the phase would fail it on every scan, and
// skipping silently would hide a real error.
func scanM2Datasets(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	return childFanOutStoredSkipNotReady(ctx, st, acct, region, m2ListDataSetsOp, apps, isGone, byParentID,
		"application(s)", "the application may not be deployed or ready", []string{"ValidationException", "ConflictException"},
		func(ctx context.Context, app childParent) (int, int, error) {
			return storeM2Datasets(ctx, client, acct, region, st, scanID, app)
		})
}

// isM2PageSizeRejected reports whether err is a ValidationException naming
// maxResults, the page size disco itself chose.
func isM2PageSizeRejected(err error) bool {
	var ve *m2types.ValidationException
	if !errors.As(err, &ve) {
		return false
	}
	return slices.ContainsFunc(ve.FieldList, func(f m2types.ValidationExceptionField) bool {
		return strings.EqualFold(sv(f.Name), "maxResults")
	})
}

// storeM2Datasets stores one application's data sets a page at a time.
func storeM2Datasets(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string, app childParent) (total, inserted int, err error) {
	pager := m2.NewListDataSetsPaginator(client, &m2.ListDataSetsInput{ApplicationId: sdkaws.String(app.id)},
		func(o *m2.ListDataSetsPaginatorOptions) { o.Limit = m2DataSetsPageLimit })
	parentID := store.ResourceID("aws", acct.ID, app.arn)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if isM2PageSizeRejected(err) {
			// %v, not %w: through the chain, the not-ready skip would match
			// the ValidationException and warn about an error disco caused.
			return total, inserted, fmt.Errorf("%s rejected page size %d: %v", m2ListDataSetsOp, m2DataSetsPageLimit, err)
		}
		if err != nil {
			return total, inserted, err
		}
		var children []containedChild
		for _, d := range out.DataSets {
			name := sv(d.DataSetName)
			if name == "" {
				continue
			}
			children = append(children, containedChild{parentID: parentID, row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Dataset, NativeID: m2DatasetNativeID(app.arn, name),
				Name: &name, Region: &region, CreatedAt: tp(d.CreationTime),
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}})
		}
		t, i, err := upsertContained(st, m2ListDataSetsOp, children)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}
