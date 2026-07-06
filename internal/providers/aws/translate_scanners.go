package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/translate"
)

func init() {
	registerService(serviceEntry{
		name: "aws:translate",
		fn:   scanTranslate,
		emits: []coverage.TypeDecl{
			{Service: "translate", DiscoType: TypeTranslateParallelData, Leaf: true},
			{Service: "translate", DiscoType: TypeTranslateTerminology, Leaf: true},
		},
	})
}

type translateAPI interface {
	ListParallelData(context.Context, *translate.ListParallelDataInput, ...func(*translate.Options)) (*translate.ListParallelDataOutput, error)
	ListTerminologies(context.Context, *translate.ListTerminologiesInput, ...func(*translate.Options)) (*translate.ListTerminologiesOutput, error)
}

// translateBlockListed reports the NotAuthorizedException AWS returns when an
// account is block-listed from Active Custom Translation (the customization
// feature ListParallelData powers) — a fraud/abuse gate distinct from account
// entitlement. Translate itself still works, so this is a sub-feature gap:
// callers silent-skip the phase (siblings run) rather than marking the whole
// service disabled/not-entitled.
func translateBlockListed(err error) bool {
	return isAPIErrorWithMessage(err, "NotAuthorizedException", "block-listed")
}

func scanTranslate(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := translate.NewFromConfig(acct.cfg, func(o *translate.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanTranslateParallelData(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanTranslateTerminologies(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanTranslateParallelData(ctx context.Context, client translateAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := translate.NewListParallelDataPaginator(client, &translate.ListParallelDataInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if translateBlockListed(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "translate:ListParallelData", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("translate:ListParallelData: %w", perr)
		}
		for _, p := range out.ParallelDataPropertiesList {
			arn := sv(p.Arn)
			if arn == "" {
				continue
			}
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTranslateParallelData, NativeID: arn,
				Name: p.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "translate parallel-data")
}

func scanTranslateTerminologies(ctx context.Context, client translateAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := translate.NewListTerminologiesPaginator(client, &translate.ListTerminologiesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if translateBlockListed(perr) {
				return 0, 0, nil
			}
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "translate:ListTerminologies", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("translate:ListTerminologies: %w", perr)
		}
		for _, term := range out.TerminologyPropertiesList {
			arn := sv(term.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTranslateTerminology, NativeID: arn,
				Name: term.Name, Region: &region,
				AttributesJSON: mustJSON(term), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "translate terminologies")
}
