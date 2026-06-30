package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/qapps"
	"github.com/aws/aws-sdk-go-v2/service/qbusiness"
)

func init() {
	registerService(serviceEntry{
		name: "aws:qapps",
		fn:   scanQApps,
		emits: []coverage.TypeDecl{
			{Service: "qapps", DiscoType: TypeQAppsQApp, Leaf: true},
		},
	})
}

type qappsAPI interface {
	ListQApps(context.Context, *qapps.ListQAppsInput, ...func(*qapps.Options)) (*qapps.ListQAppsOutput, error)
}

// scanQApps enumerates Q Business applications (each is a Q Apps "instance")
// directly via the qbusiness SDK, then lists the Q Apps under each instance.
// Listing the parents in-scanner avoids depending on the qbusiness service
// scanner's phase-1 ordering — phase-1 scanners run concurrently, so reading
// already-stored qbusiness rows would race.
func scanQApps(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	qbClient := qbusiness.NewFromConfig(acct.cfg, func(o *qbusiness.Options) { o.Region = region })
	pager := qbusiness.NewListApplicationsPaginator(qbClient, &qbusiness.ListApplicationsInput{})
	var instanceIDs []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "qapps:ListApplications", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("qapps:ListApplications: %w", perr)
		}
		for _, a := range out.Applications {
			if id := sv(a.ApplicationId); id != "" {
				instanceIDs = append(instanceIDs, id)
			}
		}
	}

	client := qapps.NewFromConfig(acct.cfg, func(o *qapps.Options) { o.Region = region })
	return scanQAppsBody(ctx, client, instanceIDs, acct, region, st, scanID)
}

func scanQAppsBody(ctx context.Context, client qappsAPI, instanceIDs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instanceIDs {
		instanceID := inst
		pager := qapps.NewListQAppsPaginator(client, &qapps.ListQAppsInput{InstanceId: &instanceID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) || isAPIErrorCode(perr, "ResourceNotFoundException") {
					break
				}
				return 0, 0, fmt.Errorf("qapps:ListQApps %s: %w", inst, perr)
			}
			for _, a := range out.Apps {
				arn := sv(a.AppArn)
				if arn == "" {
					continue
				}
				label := sv(a.Title)
				if label == "" {
					label = sv(a.AppId)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeQAppsQApp, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "qapps q-apps")
}
