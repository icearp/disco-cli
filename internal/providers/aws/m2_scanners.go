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
	ListTagsForResource(context.Context, *m2.ListTagsForResourceInput, ...func(*m2.Options)) (*m2.ListTagsForResourceOutput, error)
}

const (
	m2ListApplicationsOp = "m2:ListApplications"
	m2ListEnvironmentsOp = "m2:ListEnvironments"
	m2ListDeploymentsOp  = "m2:ListDeployments"
	m2ListDataSetsOp     = "m2:ListDataSets"
	m2TagsOp             = "m2:ListTagsForResource"
	// m2PageCap is the maxResults cap (MaxResults, 1-2000) of every m2
	// listing read here.
	m2PageCap = 2000
)

// m2NotFound is the not-found code ListDeployments and ListTagsForResource
// model: an application or resource deleted after it was listed.
var m2NotFound = []string{"ResourceNotFoundException"}

// scanM2 discovers M2 (Mainframe Modernization) applications, environments,
// and per-application deployments and data sets. Applications and
// environments are keyed by their ARNs and carry their tags; deployments and
// data sets have no ARN and are keyed by the parent application's ARN plus a
// path.
func scanM2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := m2.NewFromConfig(acct.cfg, func(o *m2.Options) { o.Region = region })
	return scanM2All(ctx, client, acct, region, st, scanID)
}

// scanM2All runs every listing as its own phase, one at a time and in this
// order: applications, environments, then each stored application's
// deployments and data sets. A phase's error does not stop the later ones; a
// failed write and ctx's end do, and so does an application phase that
// stores nothing and fails only with NXDOMAIN, since the region does not
// serve the service (runHostPhases). The tag reads' denials are one warning
// for the whole scan.
func scanM2All(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	tags := m2Tags(client).sharingDenials()
	var apps []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "m2", 0, []hostPhase{
		{op: m2ListApplicationsOp, run: func(p *phaseTotals) bool {
			var failed bool
			apps, failed = scanM2Applications(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: m2ListEnvironmentsOp, run: func(p *phaseTotals) bool {
			return scanM2Environments(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, m2ListDeploymentsOp, func() (int, int, error) {
			return scanM2Deployments(ctx, client, acct, region, st, scanID, apps)
		}),
		countedPhase(0, m2ListDataSetsOp, func() (int, int, error) {
			return scanM2Datasets(ctx, client, acct, region, st, scanID, apps)
		}),
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// m2Tags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: the ARN its listing returned. Only applications and environments
// are read: TagResource and the Service Reference's tag condition keys cover
// those two (its ListTagsForResource entry names no resource at all), their
// Create inputs take Tags, and deployments and data sets have no ARN to send.
// The op returns every tag at once. ResourceNotFoundException, its modeled
// not-found, drops a resource deleted after it was listed.
func m2Tags(client m2API) rowTagReader {
	return rowTagReader{
		op:       m2TagsOp,
		notFound: m2NotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &m2.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanM2Applications stores the applications, each page written as soon as
// its rows' tags are read, and returns those stored with an ApplicationId,
// which the deployment and data set listings key on. It reports whether a
// write failed, after which the scan must stop.
func scanM2Applications(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, m2ListApplicationsOp, "applications", "ApplicationId",
		func(ctx context.Context, token *string) ([]m2types.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &m2.ListApplicationsInput{MaxResults: sdkaws.Int32(m2PageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Applications, out.NextToken, nil
		},
		func(a m2types.ApplicationSummary) (*store.Resource, string, string) {
			status := string(a.Status)
			return arnRow("applications", a.ApplicationArn, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Application, NativeID: sv(a.ApplicationArn),
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		func(a m2types.ApplicationSummary) string { return sv(a.ApplicationId) },
		tags, p)
}

// scanM2Environments stores the runtime environments, each page written as
// soon as its rows' tags are read. It reports whether a write failed, after
// which the scan must stop.
func scanM2Environments(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, m2ListEnvironmentsOp,
		func(ctx context.Context, token *string) ([]m2types.EnvironmentSummary, *string, error) {
			out, err := client.ListEnvironments(ctx, &m2.ListEnvironmentsInput{MaxResults: sdkaws.Int32(m2PageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Environments, out.NextToken, nil
		},
		func(e m2types.EnvironmentSummary) (*store.Resource, string, string) {
			status := string(e.Status)
			return arnRow("environments", e.EnvironmentArn, e.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Environment, NativeID: sv(e.EnvironmentArn),
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanM2Deployments stores each application's deployments, the applications
// listed concurrently (storeTokenEachParent): one application's failure costs
// the others none of theirs, and the pages it read before failing are kept.
// An application deleted before its listing was read
// (ResourceNotFoundException) is skipped silently, and one deleted
// mid-listing keeps the deployments already read and is warned about; a
// denial warns once; any other error is returned after the rows are stored.
func scanM2Deployments(ctx context.Context, client m2API, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	arnOf := make(map[string]string, len(apps))
	for _, a := range apps {
		arnOf[a.id] = a.arn
	}
	return storeTokenEachParent(ctx, st, acct, region, m2ListDeploymentsOp, m2NotFound, childParentIDs(apps),
		func(ctx context.Context, appID string, token *string) ([]m2types.DeploymentSummary, *string, error) {
			out, err := client.ListDeployments(ctx, &m2.ListDeploymentsInput{ApplicationId: &appID, MaxResults: sdkaws.Int32(m2PageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Deployments, out.NextToken, nil
		},
		func(appID string, d m2types.DeploymentSummary) (*store.Resource, string, string) {
			did := sv(d.DeploymentId)
			if did == "" {
				return nil, "deployments have no DeploymentId", "a deployment of " + arnOf[appID]
			}
			status := string(d.Status)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeM2Deployment, NativeID: arnOf[appID] + "/deployment/" + did,
				Name: &did, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			}, "", ""
		})
}

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
		func(o *m2.ListDataSetsPaginatorOptions) { o.Limit = m2PageCap })
	parentID := store.ResourceID("aws", acct.ID, app.arn)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if isM2PageSizeRejected(err) {
			// %v, not %w: through the chain, the not-ready skip would match
			// the ValidationException and warn about an error disco caused.
			return total, inserted, fmt.Errorf("%s rejected page size %d: %v", m2ListDataSetsOp, m2PageCap, err)
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
