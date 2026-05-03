package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	ggtypes "github.com/aws/aws-sdk-go-v2/service/greengrass/types"
)

// versOut — uniform shape returned by all 7 ListXxxDefinitionVersions
// callers. Greengrass v1 outputs share the same VersionInformation +
// NextToken pair across kinds, only the input field name differs.
type versOut struct {
	Versions  []ggtypes.VersionInformation
	NextToken *string
}

// ggListDefs runs the manual NextToken loop for a kind's definitions
// list, upserts a row per definition, and returns the collected
// DefinitionIds for the version-list pass.
func ggListDefs[Out any](
	ctx context.Context, acct *account, region string, st *store.Store, scanID string,
	op string, dtype string,
	listFn func(*string) (Out, error),
	extract func(Out) (any, *string),
) ([]string, int, int, error) {
	var ids []string
	var batch []*store.Resource
	var token *string
	for {
		out, err := listFn(token)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "greengrass:List"+op, acct.ID, region, err)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("greengrass:List%s: %w", op, err)
		}
		defsAny, next := extract(out)
		defs, ok := defsAny.([]ggtypes.DefinitionInformation)
		if !ok {
			return nil, 0, 0, fmt.Errorf("greengrass:List%s: unexpected type %T", op, defsAny)
		}
		for _, d := range defs {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			id := sv(d.Id)
			ids = append(ids, id)
			label := sv(d.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: dtype, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if next == nil || *next == "" {
			break
		}
		token = next
	}
	t, i, err := upsertBatch(st, batch, "greengrass "+op)
	return ids, t, i, err
}

// scanGGVersions iterates over collected definition IDs and fetches
// each kind's versions via the supplied listFn. Each version emits one
// row keyed on its native ARN.
func scanGGVersions(
	ctx context.Context, acct *account, region string, st *store.Store, scanID string,
	op string, dtype string, defIDs []string,
	listFn func(defID, token *string) (versOut, error),
) (int, int, error) {
	if len(defIDs) == 0 {
		return 0, 0, nil
	}
	var batch []*store.Resource
	for _, defID := range defIDs {
		id := defID
		var token *string
		for {
			out, err := listFn(&id, token)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return 0, 0, fmt.Errorf("greengrass:List%s %s: %w", op, defID, err)
			}
			for _, v := range out.Versions {
				arn := sv(v.Arn)
				if arn == "" {
					continue
				}
				label := sv(v.Version)
				if label == "" {
					label = sv(v.Id)
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: dtype, NativeID: arn,
					Name: &label, Region: &region, AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertBatch(st, batch, "greengrass "+op)
}
