package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	"github.com/aws/aws-sdk-go-v2/service/cleanroomsml"
)

func init() {
	registerService(serviceEntry{
		name: "aws:cleanrooms-ml",
		fn:   scanCleanRoomsML,
		emits: []coverage.TypeDecl{
			{Service: "cleanrooms-ml", DiscoType: TypeCleanRoomsMLConfiguredModelAlgorithm, Leaf: true},
			{Service: "cleanrooms-ml", DiscoType: TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, Leaf: true},
			{Service: "cleanrooms-ml", DiscoType: TypeCleanRoomsMLTrainingDataset, Leaf: true},
		},
	})
}

type cleanRoomsMLAPI interface {
	ListConfiguredModelAlgorithms(context.Context, *cleanroomsml.ListConfiguredModelAlgorithmsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListConfiguredModelAlgorithmsOutput, error)
	ListConfiguredModelAlgorithmAssociations(context.Context, *cleanroomsml.ListConfiguredModelAlgorithmAssociationsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListConfiguredModelAlgorithmAssociationsOutput, error)
	ListTrainingDatasets(context.Context, *cleanroomsml.ListTrainingDatasetsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListTrainingDatasetsOutput, error)
}

// scanCleanRoomsML discovers configured model algorithms, training datasets,
// and configured-model-algorithm associations. Associations require fan-out
// per Clean Rooms membership ID (sourced from cleanrooms SDK).
func scanCleanRoomsML(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	mlClient := cleanroomsml.NewFromConfig(acct.cfg, func(o *cleanroomsml.Options) { o.Region = region })
	crClient := cleanrooms.NewFromConfig(acct.cfg, func(o *cleanrooms.Options) { o.Region = region })

	t, i, ferr := scanCRMLConfiguredModelAlgorithms(ctx, mlClient, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCRMLTrainingDatasets(ctx, mlClient, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	memberIDs, mErr := loadCleanRoomsMembershipIDs(ctx, crClient, acct, region, st)
	if mErr != nil {
		return total, inserted, mErr
	}
	t, i, ferr = scanCRMLConfiguredModelAlgorithmAssociations(ctx, mlClient, memberIDs, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func loadCleanRoomsMembershipIDs(ctx context.Context, client *cleanrooms.Client, acct *account, region string, st *store.Store) ([]string, error) {
	var ids []string
	pager := cleanrooms.NewListMembershipsPaginator(client, &cleanrooms.ListMembershipsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "cleanrooms:ListMemberships(ml-fanout)", acct.ID, region, err)
				return nil, nil
			}
			return nil, fmt.Errorf("cleanrooms:ListMemberships(ml-fanout): %w", err)
		}
		for _, m := range out.MembershipSummaries {
			if id := sv(m.Id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func scanCRMLConfiguredModelAlgorithms(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListConfiguredModelAlgorithms(ctx, &cleanroomsml.ListConfiguredModelAlgorithmsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cleanrooms-ml:ListConfiguredModelAlgorithms", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cleanrooms-ml:ListConfiguredModelAlgorithms: %w", err)
		}
		for _, c := range out.ConfiguredModelAlgorithms {
			arn := sv(c.ConfiguredModelAlgorithmArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsMLConfiguredModelAlgorithm, NativeID: arn,
				Name: c.Name, Region: &region,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "cleanrooms-ml configured-model-algorithms")
}

func scanCRMLTrainingDatasets(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListTrainingDatasets(ctx, &cleanroomsml.ListTrainingDatasetsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cleanrooms-ml:ListTrainingDatasets", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cleanrooms-ml:ListTrainingDatasets: %w", err)
		}
		for _, d := range out.TrainingDatasets {
			arn := sv(d.TrainingDatasetArn)
			if arn == "" {
				continue
			}
			status := string(d.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsMLTrainingDataset, NativeID: arn,
				Name: d.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "cleanrooms-ml training-datasets")
}

func scanCRMLConfiguredModelAlgorithmAssociations(ctx context.Context, client cleanRoomsMLAPI, memberIDs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, m := range memberIDs {
		mid := m
		var nextToken *string
		for {
			out, err := client.ListConfiguredModelAlgorithmAssociations(ctx, &cleanroomsml.ListConfiguredModelAlgorithmAssociationsInput{
				MembershipIdentifier: &mid,
				NextToken:            nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "cleanrooms-ml:ListConfiguredModelAlgorithmAssociations", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				return 0, 0, fmt.Errorf("cleanrooms-ml:ListConfiguredModelAlgorithmAssociations m=%s: %w", mid, err)
			}
			for _, a := range out.ConfiguredModelAlgorithmAssociations {
				arn := sv(a.ConfiguredModelAlgorithmAssociationArn)
				if arn == "" {
					continue
				}
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, NativeID: arn,
					Name: a.Name, Region: &region,
					AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "cleanrooms-ml configured-model-algorithm-associations")
}
