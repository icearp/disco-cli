package aws

import (
	"context"
	"errors"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssmsap"
	ssmsaptypes "github.com/aws/aws-sdk-go-v2/service/ssmsap/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSSMSAPApplication, Service: "systems-manager-sap"})
	registerType(restype.Descriptor{Type: TypeSystemsManagerSAPComponent, Service: "systems-manager-sap"})
	registerType(restype.Descriptor{Type: TypeSystemsManagerSAPDatabase, Service: "systems-manager-sap"})
	registerService(serviceEntry{
		name: "aws:systems-manager-sap",
		fn:   scanSSMSAP,
	})
}

type ssmSAPAPI interface {
	ListApplications(context.Context, *ssmsap.ListApplicationsInput, ...func(*ssmsap.Options)) (*ssmsap.ListApplicationsOutput, error)
	ListComponents(context.Context, *ssmsap.ListComponentsInput, ...func(*ssmsap.Options)) (*ssmsap.ListComponentsOutput, error)
	ListDatabases(context.Context, *ssmsap.ListDatabasesInput, ...func(*ssmsap.Options)) (*ssmsap.ListDatabasesOutput, error)
}

const (
	ssmSAPApplicationsOp = "ssm-sap:ListApplications"
	ssmSAPComponentsOp   = "ssm-sap:ListComponents"
	ssmSAPDatabasesOp    = "ssm-sap:ListDatabases"
)

// ssmSAPPageMax is the MaxResults cap the Smithy model sets on every list op
// this file calls.
const ssmSAPPageMax = 50

// ssmSAPNotFound is the code ListComponents and ListDatabases model for an
// application deleted since it was listed.
var ssmSAPNotFound = []string{"ResourceNotFoundException"}

// scanSSMSAP discovers Systems Manager for SAP applications and, per
// application, the registered components and databases. Every phase runs;
// the component and database phases read only the applications the
// application phase stored, and only a store write failure stops the scan.
func scanSSMSAP(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := ssmsap.NewFromConfig(acct.cfg, func(o *ssmsap.Options) { o.Region = region })
	var apps []string
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{ssmSAPApplicationsOp, func() (t, i int, err error) {
			t, i, apps, err = scanSSMSAPApplications(ctx, client, acct, region, st, scanID)
			return t, i, err
		}},
		{ssmSAPComponentsOp, func() (int, int, error) {
			return scanSSMSAPComponents(ctx, client, acct, region, st, scanID, apps)
		}},
		{ssmSAPDatabasesOp, func() (int, int, error) {
			return scanSSMSAPDatabases(ctx, client, acct, region, st, scanID, apps)
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

// scanSSMSAPApplications lists every application (no Filters) and also
// returns the Ids of the stored ones, for the component and database phases.
func scanSSMSAPApplications(ctx context.Context, client ssmSAPAPI, acct *account, region string, st *store.Store, scanID string) (int, int, []string, error) {
	total, inserted, parents, err := storeParentList(ctx, st, acct, region, ssmSAPApplicationsOp, "applications", "Id",
		func(ctx context.Context, token *string) ([]ssmsaptypes.ApplicationSummary, *string, error) {
			out, err := client.ListApplications(ctx, &ssmsap.ListApplicationsInput{NextToken: token, MaxResults: sdkaws.Int32(ssmSAPPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.Applications, out.NextToken, nil
		},
		func(a ssmsaptypes.ApplicationSummary) (*store.Resource, string, string) {
			if sv(a.Arn) == "" {
				return nil, "applications have an empty Arn", sv(a.Id)
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSSMSAPApplication, NativeID: *a.Arn,
				Name: a.Id, Region: &region, Status: nonEmptyPtr(string(a.DiscoveryStatus)),
				AttributesJSON: mustJSON(a), TagsJSON: mapTagsJSON(a.Tags), DiscoveredBy: scanID,
			}, "", ""
		},
		func(a ssmsaptypes.ApplicationSummary) string { return sv(a.Id) })
	ids := make([]string, len(parents))
	for i, p := range parents {
		ids[i] = p.id
	}
	return total, inserted, ids, err
}

// scanSSMSAPComponents lists each application's components concurrently.
// ListComponents models UnauthorizedException, a code isAccessDenied does not
// match, so it is tallied here with the codes isAccessDenied matches as one
// denial: the application's rows read before it are kept and one warning
// names the first denied application in parent order, after the write
// succeeded. Component rows carry no Status: the summary has no status field.
func scanSSMSAPComponents(ctx context.Context, client ssmSAPAPI, acct *account, region string, st *store.Store, scanID string, apps []string) (int, int, error) {
	var denied skipTally
	total, inserted, err := storeTokenEachParentSkip(ctx, st, acct, region, ssmSAPComponentsOp, ssmSAPNotFound, apps,
		func(ctx context.Context, app string, token *string) ([]ssmsaptypes.ComponentSummary, *string, error) {
			out, err := client.ListComponents(ctx, &ssmsap.ListComponentsInput{ApplicationId: &app, NextToken: token, MaxResults: sdkaws.Int32(ssmSAPPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.Components, out.NextToken, nil
		},
		func(_ string, c ssmsaptypes.ComponentSummary) (*store.Resource, string, string) {
			if sv(c.Arn) == "" {
				return nil, "components have an empty Arn", sv(c.ComponentId)
			}
			label := sv(c.ComponentId)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSystemsManagerSAPComponent, NativeID: *c.Arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(c), TagsJSON: mapTagsJSON(c.Tags), DiscoveredBy: scanID,
			}, "", ""
		},
		func(app string, err error) bool {
			if !isAPIErrorCode(err, "UnauthorizedException") && !isAccessDenied(err) {
				return false
			}
			denied.skipped(app, err)
			return true
		})
	if errors.Is(err, store.ErrStoreWrite) {
		return total, inserted, err
	}
	denied.warnDeniedIn(st, ssmSAPComponentsOp, acct, region, "application(s) whose components were not read were")
	return total, inserted, err
}

// scanSSMSAPDatabases lists each application's databases concurrently.
// Database rows carry no Status: the summary has no status field.
func scanSSMSAPDatabases(ctx context.Context, client ssmSAPAPI, acct *account, region string, st *store.Store, scanID string, apps []string) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, ssmSAPDatabasesOp, ssmSAPNotFound, apps,
		func(ctx context.Context, app string, token *string) ([]ssmsaptypes.DatabaseSummary, *string, error) {
			out, err := client.ListDatabases(ctx, &ssmsap.ListDatabasesInput{ApplicationId: &app, NextToken: token, MaxResults: sdkaws.Int32(ssmSAPPageMax)})
			if err != nil {
				return nil, nil, err
			}
			return out.Databases, out.NextToken, nil
		},
		func(_ string, d ssmsaptypes.DatabaseSummary) (*store.Resource, string, string) {
			if sv(d.Arn) == "" {
				return nil, "databases have an empty Arn", sv(d.DatabaseId)
			}
			label := sv(d.DatabaseId)
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSystemsManagerSAPDatabase, NativeID: *d.Arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(d), TagsJSON: mapTagsJSON(d.Tags), DiscoveredBy: scanID,
			}, "", ""
		})
}
