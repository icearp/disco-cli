package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	"github.com/aws/aws-sdk-go-v2/service/cleanroomsml"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLConfiguredModelAlgorithm, Service: "cleanrooms-ml", Upstream: "AWS::CleanRoomsML::ConfiguredModelAlgorithm", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, Service: "cleanrooms-ml", Upstream: "AWS::CleanRoomsML::ConfiguredModelAlgorithmAssociation", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLTrainingDataset, Service: "cleanrooms-ml", Upstream: "AWS::CleanRoomsML::TrainingDataset", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLAudienceModel, Service: "cleanrooms-ml", Upstream: "AWS::cleanrooms-ml::audiencemodel"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLConfiguredAudienceModel, Service: "cleanrooms-ml", Upstream: "AWS::cleanrooms-ml::configuredaudiencemodel"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLMLInputChannel, Service: "cleanrooms-ml", Upstream: "AWS::cleanrooms-ml::MLInputChannel", Leaf: true})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLTrainedModel, Service: "cleanrooms-ml", Upstream: "AWS::cleanrooms-ml::TrainedModel"})
	registerService(serviceEntry{
		name: "aws:cleanrooms-ml",
		fn:   scanCleanRoomsML,
	})
}

type cleanRoomsMLAPI interface {
	ListConfiguredModelAlgorithms(context.Context, *cleanroomsml.ListConfiguredModelAlgorithmsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListConfiguredModelAlgorithmsOutput, error)
	ListConfiguredModelAlgorithmAssociations(context.Context, *cleanroomsml.ListConfiguredModelAlgorithmAssociationsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListConfiguredModelAlgorithmAssociationsOutput, error)
	ListTrainingDatasets(context.Context, *cleanroomsml.ListTrainingDatasetsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListTrainingDatasetsOutput, error)
	ListAudienceModels(context.Context, *cleanroomsml.ListAudienceModelsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListAudienceModelsOutput, error)
	ListConfiguredAudienceModels(context.Context, *cleanroomsml.ListConfiguredAudienceModelsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListConfiguredAudienceModelsOutput, error)
	ListMLInputChannels(context.Context, *cleanroomsml.ListMLInputChannelsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListMLInputChannelsOutput, error)
	ListTrainedModels(context.Context, *cleanroomsml.ListTrainedModelsInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListTrainedModelsOutput, error)
}

// scanCleanRoomsML discovers configured model algorithms, training datasets,
// and configured-model-algorithm associations; associations fan out per Clean
// Rooms membership ID (from the cleanrooms SDK).
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

	t, i, ferr = scanCRMLAudienceModels(ctx, mlClient, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanCRMLConfiguredAudienceModels(ctx, mlClient, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	memberIDs, mErr := loadCleanRoomsMembershipIDs(ctx, crClient, acct, region, st)
	if mErr != nil {
		return total, inserted, mErr
	}
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) {
			return scanCRMLConfiguredModelAlgorithmAssociations(ctx, mlClient, memberIDs, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanCRMLMLInputChannels(ctx, mlClient, memberIDs, acct, region, st, scanID)
		},
		func() (int, int, error) {
			return scanCRMLTrainedModels(ctx, mlClient, memberIDs, acct, region, st, scanID)
		},
	} {
		t, i, ferr = phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
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

func scanCRMLAudienceModels(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := cleanroomsml.NewListAudienceModelsPaginator(client, &cleanroomsml.ListAudienceModelsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cleanrooms-ml:ListAudienceModels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cleanrooms-ml:ListAudienceModels: %w", err)
		}
		for _, a := range out.AudienceModels {
			arn := sv(a.AudienceModelArn)
			if arn == "" {
				continue
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsMLAudienceModel, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), CreatedAt: tp(a.CreateTime), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms-ml audience-models")
}

func scanCRMLConfiguredAudienceModels(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := cleanroomsml.NewListConfiguredAudienceModelsPaginator(client, &cleanroomsml.ListConfiguredAudienceModelsInput{})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "cleanrooms-ml:ListConfiguredAudienceModels", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("cleanrooms-ml:ListConfiguredAudienceModels: %w", err)
		}
		for _, c := range out.ConfiguredAudienceModels {
			arn := sv(c.ConfiguredAudienceModelArn)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCleanRoomsMLConfiguredAudienceModel, NativeID: arn,
				Name: c.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), CreatedAt: tp(c.CreateTime), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "cleanrooms-ml configured-audience-models")
}

// scanCRMLMLInputChannels and scanCRMLTrainedModels fan out per membership —
// ListMLInputChannels / ListTrainedModels both require a MembershipIdentifier.
func scanCRMLMLInputChannels(ctx context.Context, client cleanRoomsMLAPI, memberIDs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, m := range memberIDs {
		mid := m
		pager := cleanroomsml.NewListMLInputChannelsPaginator(client, &cleanroomsml.ListMLInputChannelsInput{MembershipIdentifier: &mid})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "cleanrooms-ml:ListMLInputChannels", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				return 0, 0, fmt.Errorf("cleanrooms-ml:ListMLInputChannels m=%s: %w", mid, err)
			}
			for _, c := range out.MlInputChannelsList {
				arn := sv(c.MlInputChannelArn)
				if arn == "" {
					continue
				}
				status := string(c.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCleanRoomsMLMLInputChannel, NativeID: arn,
					Name: c.Name, Region: &region, Status: &status,
					AttributesJSON: mustJSON(c), CreatedAt: tp(c.CreateTime), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "cleanrooms-ml ml-input-channels")
}

func scanCRMLTrainedModels(ctx context.Context, client cleanRoomsMLAPI, memberIDs []string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, m := range memberIDs {
		mid := m
		pager := cleanroomsml.NewListTrainedModelsPaginator(client, &cleanroomsml.ListTrainedModelsInput{MembershipIdentifier: &mid})
		for pager.HasMorePages() {
			out, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "cleanrooms-ml:ListTrainedModels", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
					break
				}
				return 0, 0, fmt.Errorf("cleanrooms-ml:ListTrainedModels m=%s: %w", mid, err)
			}
			for _, tm := range out.TrainedModels {
				arn := sv(tm.TrainedModelArn)
				if arn == "" {
					continue
				}
				status := string(tm.Status)
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeCleanRoomsMLTrainedModel, NativeID: arn,
					Name: tm.Name, Region: &region, Status: &status,
					AttributesJSON: mustJSON(tm), CreatedAt: tp(tm.CreateTime), DiscoveredBy: scanID,
				})
			}
		}
	}
	return upsertBatch(st, batch, "cleanrooms-ml trained-models")
}
