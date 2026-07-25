package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectVocabulary, Service: "connect", Leaf: true})
	registerType(restype.Descriptor{Type: TypeConnectAuthenticationProfile, Service: "connect", Upstream: "AWS::connect::authentication-profile", Leaf: true})
}

// connectExtraAPI is the narrow surface for the extra per-instance leaf
// families (custom transcription vocabularies, agent authentication profiles).
type connectExtraAPI interface {
	SearchVocabularies(context.Context, *connect.SearchVocabulariesInput, ...func(*connect.Options)) (*connect.SearchVocabulariesOutput, error)
	ListAuthenticationProfiles(context.Context, *connect.ListAuthenticationProfilesInput, ...func(*connect.Options)) (*connect.ListAuthenticationProfilesOutput, error)
}

// scanConnectExtra runs the leaf families that the larger family files don't own.
func scanConnectExtra(ctx context.Context, client connectExtraAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanConnectVocabularies(ctx, client, instances, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanConnectAuthenticationProfiles(ctx, client, instances, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanConnectVocabularies(ctx context.Context, client connectExtraAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewSearchVocabulariesPaginator(client, &connect.SearchVocabulariesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:SearchVocabularies", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:SearchVocabularies %s: %w", instID, perr)
			}
			for _, v := range out.VocabularySummaryList {
				arn := sv(v.Arn)
				if arn == "" {
					continue
				}
				name := sv(v.Name)
				status := string(v.State)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeConnectVocabulary, NativeID: arn,
					Name: &name, Region: &region, Status: &status,
					AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "connect vocabularies")
}

func scanConnectAuthenticationProfiles(ctx context.Context, client connectExtraAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		pager := connect.NewListAuthenticationProfilesPaginator(client, &connect.ListAuthenticationProfilesInput{InstanceId: &instID})
		for pager.HasMorePages() {
			out, perr := pager.NextPage(ctx)
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListAuthenticationProfiles", acct.ID, region, perr)
					break
				}
				return 0, 0, fmt.Errorf("connect:ListAuthenticationProfiles %s: %w", instID, perr)
			}
			for _, p := range out.AuthenticationProfileSummaryList {
				arn := sv(p.Arn)
				if arn == "" {
					continue
				}
				name := sv(p.Name)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeConnectAuthenticationProfile, NativeID: arn,
					Name: &name, Region: &region,
					AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "connect authentication profiles")
}
