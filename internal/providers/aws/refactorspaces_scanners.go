package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/migrationhubrefactorspaces"
	rstypes "github.com/aws/aws-sdk-go-v2/service/migrationhubrefactorspaces/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeRefactorSpacesEnvironment, Service: "refactor-spaces"})
	registerType(restype.Descriptor{Type: TypeRefactorSpacesApplication, Service: "refactor-spaces"})
	registerType(restype.Descriptor{Type: TypeRefactorSpacesService, Service: "refactor-spaces"})
	registerType(restype.Descriptor{Type: TypeRefactorSpacesRoute, Service: "refactor-spaces"})
	registerService(serviceEntry{
		name: "aws:refactor-spaces",
		fn:   scanRefactorSpaces,
	})
}

type refactorSpacesAPI interface {
	ListEnvironments(context.Context, *migrationhubrefactorspaces.ListEnvironmentsInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListEnvironmentsOutput, error)
	ListApplications(context.Context, *migrationhubrefactorspaces.ListApplicationsInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListApplicationsOutput, error)
	ListServices(context.Context, *migrationhubrefactorspaces.ListServicesInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListServicesOutput, error)
	ListRoutes(context.Context, *migrationhubrefactorspaces.ListRoutesInput, ...func(*migrationhubrefactorspaces.Options)) (*migrationhubrefactorspaces.ListRoutesOutput, error)
}

const (
	rsEnvironmentsOp = "migrationhubrefactorspaces:ListEnvironments"
	rsApplicationsOp = "migrationhubrefactorspaces:ListApplications"
	rsServicesOp     = "migrationhubrefactorspaces:ListServices"
	rsRoutesOp       = "migrationhubrefactorspaces:ListRoutes"

	// rsPageMax is the MaxResults cap the model sets on every list op.
	rsPageMax = 100
)

// rsNotFound is the code the child list ops model for a parent deleted since
// it was listed.
var rsNotFound = []string{"ResourceNotFoundException"}

// rsApp is an application stored this scan, with the ids ListServices and
// ListRoutes key on.
type rsApp struct{ envID, appID, arn string }

// scanRefactorSpaces discovers Migration Hub Refactor Spaces environments,
// per-environment applications, and per-application services and routes.
// Every phase runs; a child phase reads only the parents its parent phase
// stored, and only a store write failure stops the scan.
func scanRefactorSpaces(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := migrationhubrefactorspaces.NewFromConfig(acct.cfg, func(o *migrationhubrefactorspaces.Options) { o.Region = region })
	var (
		envs []childParent
		apps []rsApp
	)
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{rsEnvironmentsOp, func() (t, i int, err error) {
			t, i, envs, err = scanRSEnvironments(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{rsApplicationsOp, func() (t, i int, err error) {
			t, i, apps, err = scanRSApplications(ctx, client, acct, region, st, scanID, envs)
			return t, i, err
		}},
		{rsServicesOp, func() (int, int, error) {
			return scanRSServices(ctx, client, acct, region, st, scanID, apps)
		}},
		{rsRoutesOp, func() (int, int, error) {
			return scanRSRoutes(ctx, client, acct, region, st, scanID, apps)
		}},
	}
	var p phaseTotals
	for _, ph := range phases {
		t, i, err := ph.run()
		if p.addPhase(ph.op, t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// rsRow builds the row of a Refactor Spaces resource, or returns why it
// cannot be stored (what says which resources) and its id when the ARN is
// empty.
func rsRow(acct *account, region, scanID, rtype, what string, arn, id, name *string, attrs any, tags map[string]string) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty Arn", sv(id)
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: rtype, NativeID: *arn, Name: name, Region: &region,
		AttributesJSON: mustJSON(attrs), TagsJSON: mapTagsJSON(tags), DiscoveredBy: scanID,
	}, "", ""
}

// scanRSEnvironments also returns the stored environments for the
// application phase.
func scanRSEnvironments(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []childParent, error) {
	return storeParentList(ctx, st, acct, region, rsEnvironmentsOp, "environments", "EnvironmentId",
		func(ctx context.Context, token *string) ([]rstypes.EnvironmentSummary, *string, error) {
			out, err := client.ListEnvironments(ctx, &migrationhubrefactorspaces.ListEnvironmentsInput{NextToken: token, MaxResults: sdkaws.Int32(rsPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.EnvironmentSummaryList, out.NextToken, nil
		},
		func(e rstypes.EnvironmentSummary) (*store.Resource, string, string) {
			return rsRow(acct, region, scanID, TypeRefactorSpacesEnvironment, "environments", e.Arn, e.EnvironmentId, e.Name, e, e.Tags)
		},
		func(e rstypes.EnvironmentSummary) string { return sv(e.EnvironmentId) })
}

// scanRSApplications lists the applications of each environment concurrently
// and also returns the stored ones that carry an ApplicationId, for the
// service and route phases; those without one are warned about once the
// write succeeded. A failed write returns no applications.
func scanRSApplications(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string, envs []childParent) (int, int, []rsApp, error) {
	envIDs := make([]string, len(envs))
	for i, e := range envs {
		envIDs[i] = e.id
	}
	var (
		apps []rsApp
		noID skipTally
	)
	total, inserted, err := storeTokenEachParent(ctx, st, acct, region, rsApplicationsOp, rsNotFound, envIDs,
		func(ctx context.Context, envID string, token *string) ([]rstypes.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &migrationhubrefactorspaces.ListApplicationsInput{EnvironmentIdentifier: &envID, NextToken: token, MaxResults: sdkaws.Int32(rsPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.ApplicationSummaryList, out.NextToken, nil
		},
		func(envID string, a rstypes.ApplicationSummary) (*store.Resource, string, string) {
			r, why, key := rsRow(acct, region, scanID, TypeRefactorSpacesApplication, "applications", a.Arn, a.ApplicationId, a.Name, a, a.Tags)
			switch {
			case r == nil:
			case sv(a.ApplicationId) == "":
				noID.skipped(r.NativeID, nil)
			default:
				apps = append(apps, rsApp{envID: envID, appID: *a.ApplicationId, arn: r.NativeID})
			}
			return r, why, key
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, nil, err
	}
	warnNoChildID(st, rsApplicationsOp, acct, region, &noID, "applications", "ApplicationId")
	return total, inserted, apps, err
}

// storeRSAppChildren lists op's items for each application concurrently
// through storeTokenEachParent, keyed by the application ARN so a warning
// names the application; page gets the application's ids.
func storeRSAppChildren[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, apps []rsApp,
	page func(ctx context.Context, app rsApp, token *string) ([]Item, *string, error),
	row func(it Item) (*store.Resource, string, string),
) (int, int, error) {
	byARN := make(map[string]rsApp, len(apps))
	arns := make([]string, len(apps))
	for i, a := range apps {
		byARN[a.arn], arns[i] = a, a.arn
	}
	return storeTokenEachParent(ctx, st, acct, region, op, rsNotFound, arns,
		func(ctx context.Context, arn string, token *string) ([]Item, *string, error) {
			return page(ctx, byARN[arn], token)
		},
		func(_ string, it Item) (*store.Resource, string, string) { return row(it) })
}

func scanRSServices(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string, apps []rsApp) (int, int, error) {
	return storeRSAppChildren(ctx, st, acct, region, rsServicesOp, apps,
		func(ctx context.Context, app rsApp, token *string) ([]rstypes.ServiceSummary, *string, error) {
			out, err := client.ListServices(ctx, &migrationhubrefactorspaces.ListServicesInput{
				EnvironmentIdentifier: &app.envID, ApplicationIdentifier: &app.appID, NextToken: token, MaxResults: sdkaws.Int32(rsPageMax),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.ServiceSummaryList, out.NextToken, nil
		},
		func(s rstypes.ServiceSummary) (*store.Resource, string, string) {
			return rsRow(acct, region, scanID, TypeRefactorSpacesService, "services", s.Arn, s.ServiceId, s.Name, s, s.Tags)
		})
}

// scanRSRoutes stores routes without a name: RouteSummary has none.
func scanRSRoutes(ctx context.Context, client refactorSpacesAPI, acct *account, region string, st *store.Store, scanID string, apps []rsApp) (int, int, error) {
	return storeRSAppChildren(ctx, st, acct, region, rsRoutesOp, apps,
		func(ctx context.Context, app rsApp, token *string) ([]rstypes.RouteSummary, *string, error) {
			out, err := client.ListRoutes(ctx, &migrationhubrefactorspaces.ListRoutesInput{
				EnvironmentIdentifier: &app.envID, ApplicationIdentifier: &app.appID, NextToken: token, MaxResults: sdkaws.Int32(rsPageMax),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.RouteSummaryList, out.NextToken, nil
		},
		func(r rstypes.RouteSummary) (*store.Resource, string, string) {
			return rsRow(acct, region, scanID, TypeRefactorSpacesRoute, "routes", r.Arn, r.RouteId, nil, r, r.Tags)
		})
}
