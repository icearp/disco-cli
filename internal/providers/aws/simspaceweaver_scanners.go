package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/simspaceweaver"
)

// isSimSpaceWeaverNotEnabled disambiguates the not-allowlisted state from a
// real IAM denial — both surface as AccessDeniedException.
func isSimSpaceWeaverNotEnabled(err error) bool {
	return isAccessDeniedWithMessage(err, "not allowlisted")
}

func init() {
	registerService(serviceEntry{
		name: "aws:sim-space-weaver",
		fn:   scanSimSpaceWeaver,
		emits: []coverage.TypeDecl{
			{Service: "sim-space-weaver", DiscoType: TypeSimSpaceWeaverSimulation, Leaf: true},
		},
	})
}

// scanSimSpaceWeaver discovers SimSpace Weaver simulations.
func scanSimSpaceWeaver(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := simspaceweaver.NewFromConfig(acct.cfg, func(o *simspaceweaver.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListSimulations(ctx, &simspaceweaver.ListSimulationsInput{NextToken: nextToken})
		if err != nil {
			if isSimSpaceWeaverNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "simspaceweaver:ListSimulations", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("simspaceweaver:ListSimulations: %w", err)
		}
		for _, s := range out.Simulations {
			arn := sv(s.Arn)
			if arn == "" {
				continue
			}
			status := string(s.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSimSpaceWeaverSimulation, NativeID: arn,
				Name: s.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "simspaceweaver simulations")
}
