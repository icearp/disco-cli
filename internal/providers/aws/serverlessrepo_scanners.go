package aws

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/service/serverlessapplicationrepository"
	sartypes "github.com/aws/aws-sdk-go-v2/service/serverlessapplicationrepository/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeServerlessRepoApplication, Service: "serverlessrepo"})
	registerType(restype.Descriptor{Type: TypeServerlessRepoApplicationVersion, Service: "serverlessrepo"})
	registerType(restype.Descriptor{Type: TypeServerlessRepoApplicationPolicy, Service: "serverlessrepo"})
	registerService(serviceEntry{
		name: "aws:serverlessrepo",
		fn:   scanServerlessRepo,
	})
}

const (
	sarListApplicationsOp        = "serverlessrepo:ListApplications"
	sarListApplicationVersionsOp = "serverlessrepo:ListApplicationVersions"
	sarGetApplicationPolicyOp    = "serverlessrepo:GetApplicationPolicy"
)

// sarPageLimit is the model's cap on MaxItems for ListApplications and
// ListApplicationVersions (range 1..100).
const sarPageLimit = 100

// serverlessRepoAPI is the narrow surface the serverlessrepo phases use.
// ListApplications is paginator-native, returns owned applications; the
// summary's ApplicationId carries the application ARN.
type serverlessRepoAPI interface {
	ListApplications(context.Context, *serverlessapplicationrepository.ListApplicationsInput, ...func(*serverlessapplicationrepository.Options)) (*serverlessapplicationrepository.ListApplicationsOutput, error)
	ListApplicationVersions(context.Context, *serverlessapplicationrepository.ListApplicationVersionsInput, ...func(*serverlessapplicationrepository.Options)) (*serverlessapplicationrepository.ListApplicationVersionsOutput, error)
	GetApplicationPolicy(context.Context, *serverlessapplicationrepository.GetApplicationPolicyInput, ...func(*serverlessapplicationrepository.Options)) (*serverlessapplicationrepository.GetApplicationPolicyOutput, error)
}

func scanServerlessRepo(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := serverlessapplicationrepository.NewFromConfig(acct.cfg, func(o *serverlessapplicationrepository.Options) { o.Region = region })
	return scanServerlessRepoAll(ctx, client, acct, region, st, scanID)
}

// scanServerlessRepoAll stores the account's applications, then the versions and
// the policy of every application it stored, including the applications
// listed before a failed page. All three phases run; their errors are joined
// and the counts summed. The application phase's transient error is demoted to
// a warning, since one transient member would make the dispatcher read the
// whole join as transient. The child phases need no demotion: each skips a
// parent whose call still fails transiently, with a warning, and never
// returns a transient error.
func scanServerlessRepoAll(ctx context.Context, client serverlessRepoAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, apps, err := scanServerlessRepoApplications(ctx, client, acct, region, st, scanID)
	vTotal, vInserted, verr := scanServerlessRepoApplicationVersions(ctx, client, acct, region, st, scanID, apps)
	pTotal, pInserted, perr := scanServerlessRepoApplicationPolicies(ctx, client, acct, region, st, scanID, apps)
	return total + vTotal + pTotal, inserted + vInserted + pInserted,
		errors.Join(demoteTransient(st, sarListApplicationsOp, acct, region, err), verr, perr)
}

// scanServerlessRepoApplications stores the applications ListApplications
// returns, which its model documents as the applications the caller owns, so
// the child phases never fan out over other accounts' public applications. It
// stores a page at a time and returns the applications of the stored pages,
// each identified by its ARN, as the child phases' parents. A failed page
// fetch adds no application, so on that error the applications of the
// earlier, stored pages are returned with it (a denial is warned and
// dropped). A failed write may have left its page unstored, so then no
// parents are returned at all, though earlier pages stay stored.
func scanServerlessRepoApplications(ctx context.Context, client serverlessRepoAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	var apps []childParent
	p := serverlessapplicationrepository.NewListApplicationsPaginator(client, &serverlessapplicationrepository.ListApplicationsInput{},
		func(o *serverlessapplicationrepository.ListApplicationsPaginatorOptions) {
			o.Limit = sarPageLimit
			o.StopOnDuplicateToken = true
		})
	total, inserted, err := pageScan(ctx, sarListApplicationsOp, acct, region, st,
		p.HasMorePages,
		func(c context.Context) (*serverlessapplicationrepository.ListApplicationsOutput, error) {
			return p.NextPage(c)
		},
		func(o *serverlessapplicationrepository.ListApplicationsOutput) []sartypes.ApplicationSummary {
			return o.Applications
		},
		func(a sartypes.ApplicationSummary) *store.Resource {
			arn := sv(a.ApplicationId)
			if arn == "" {
				return nil
			}
			apps = append(apps, childParent{id: arn, arn: arn})
			name := sv(a.Name)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServerlessRepoApplication, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			}
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, nil, err
	}
	return total, inserted, apps, err
}

// isSARApplicationGone reports whether err says the application no longer
// exists, i.e. it was deleted after ListApplications listed it. Both child
// ops model NotFoundException.
func isSARApplicationGone(err error) bool { return isAPIErrorCode(err, "NotFoundException") }

// sarApplicationVersionNativeID keys a version under its application: the API
// returns no version ARN, and the Service Reference defines ARNs only for
// applications. The semantic version has no model pattern; it goes last in
// case it ever holds a "/".
func sarApplicationVersionNativeID(appARN, semver string) string {
	return appARN + "/version/" + semver
}

// sarApplicationPolicyNativeID keys an application's policy, a singleton
// config of the application, under the application's ARN so the two rows do
// not share an identity.
func sarApplicationPolicyNativeID(appARN string) string { return appARN + "/policy" }

// scanServerlessRepoApplicationVersions stores the published versions of each
// application in apps, which holds only applications stored this scan, and
// links each under its application. Per application: NotFoundException
// (deleted since listing) is skipped silently, a denial warns once, a
// transient error after the SDK's retries is skipped with one warning for all
// such applications, and anything else fails the phase.
func scanServerlessRepoApplicationVersions(ctx context.Context, client serverlessRepoAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, sarListApplicationVersionsOp, apps, isSARApplicationGone, byParentARN,
		func(ctx context.Context, app childParent) ([]*store.Resource, error) {
			pager := serverlessapplicationrepository.NewListApplicationVersionsPaginator(client,
				&serverlessapplicationrepository.ListApplicationVersionsInput{ApplicationId: &app.id},
				func(o *serverlessapplicationrepository.ListApplicationVersionsPaginatorOptions) {
					o.Limit = sarPageLimit
					o.StopOnDuplicateToken = true
				})
			var rows []*store.Resource
			for pager.HasMorePages() {
				out, err := pager.NextPage(ctx)
				if err != nil {
					return nil, err
				}
				for _, v := range out.Versions {
					semver := sv(v.SemanticVersion)
					if semver == "" {
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeServerlessRepoApplicationVersion, NativeID: sarApplicationVersionNativeID(app.arn, semver),
						Name: &semver, Region: &region, CreatedAt: nonEmptyPtr(sv(v.CreationTime)),
						AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// scanServerlessRepoApplicationPolicies stores the policy of each application
// in apps, which holds only applications stored this scan, and links it under
// its application. An application whose policy has no statements gets no
// row. Per-application errors are handled as in
// scanServerlessRepoApplicationVersions.
func scanServerlessRepoApplicationPolicies(ctx context.Context, client serverlessRepoAPI, acct *account, region string, st *store.Store, scanID string, apps []childParent) (int, int, error) {
	return childFanOutSkipTransient(ctx, st, acct, region, sarGetApplicationPolicyOp, apps, isSARApplicationGone, byParentARN,
		func(ctx context.Context, app childParent) ([]*store.Resource, error) {
			out, err := client.GetApplicationPolicy(ctx, &serverlessapplicationrepository.GetApplicationPolicyInput{ApplicationId: &app.id})
			if err != nil {
				return nil, err
			}
			if len(out.Statements) == 0 {
				return nil, nil
			}
			name := "policy"
			return []*store.Resource{{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeServerlessRepoApplicationPolicy, NativeID: sarApplicationPolicyNativeID(app.arn),
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
			}}, nil
		})
}
