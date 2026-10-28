package aws

import (
	"cmp"
	"context"
	"fmt"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	crtypes "github.com/aws/aws-sdk-go-v2/service/cleanrooms/types"
	"github.com/aws/aws-sdk-go-v2/service/cleanroomsml"
	mltypes "github.com/aws/aws-sdk-go-v2/service/cleanroomsml/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLConfiguredModelAlgorithm, Service: "cleanrooms-ml"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, Service: "cleanrooms-ml"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLTrainingDataset, Service: "cleanrooms-ml"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLAudienceModel, Service: "cleanrooms-ml"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLConfiguredAudienceModel, Service: "cleanrooms-ml"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLMLInputChannel, Service: "cleanrooms-ml"})
	registerType(restype.Descriptor{Type: TypeCleanRoomsMLTrainedModel, Service: "cleanrooms-ml"})
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
	ListTagsForResource(context.Context, *cleanroomsml.ListTagsForResourceInput, ...func(*cleanroomsml.Options)) (*cleanroomsml.ListTagsForResourceOutput, error)
}

const (
	// crmlPageCap is the model's maxResults cap (1 to 100) of every Clean
	// Rooms ML listing scanned.
	crmlPageCap = 100

	crmlTagsOp = "cleanrooms-ml:ListTagsForResource"
	// crmlMembershipsOp is the membership listing this scan makes for its
	// per-membership listings, apart from the aws:cleanrooms scan's own.
	crmlMembershipsOp = "cleanrooms:ListMemberships(ml-fanout)"
)

// crmlNotReadyWhy says, in the skip warning, why a per-membership listing
// refused with ValidationException is skipped rather than failed. Which
// memberships the ML ops refuse, and with which code, is unverified live.
const crmlNotReadyWhy = "a membership may refuse its ML listings, for example one that was removed or whose collaboration was deleted"

// scanCleanRoomsML discovers the Clean Rooms ML resources of one region: the
// configured model algorithms, training datasets, audience models and
// configured audience models of the account, then the configured model
// algorithm associations, ML input channels and trained models of each of
// its Clean Rooms memberships. Every listing runs as its own phase, one at a
// time: a phase's error does not stop the later ones; a failed write and
// ctx's end do; a region whose first ML listing finds no endpoint runs no
// other phase (runHostPhases). Every type has its tags read per row
// (crmlTags), and the tag denials of the whole scan are one warning.
//
// The memberships are listed here, because the aws:cleanrooms scan that
// stores them runs concurrently with this one. The listing runs inside the
// associations phase, the first that needs it, so a region without Clean
// Rooms ML makes no Clean Rooms call either.
func scanCleanRoomsML(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	ml := cleanroomsml.NewFromConfig(acct.cfg, func(o *cleanroomsml.Options) { o.Region = region })
	cr := cleanrooms.NewFromConfig(acct.cfg, func(o *cleanrooms.Options) { o.Region = region })
	tags := crmlTags(ml).sharingDenials()
	var memberships []childParent
	total, inserted, err := runHostPhases(ctx, st, acct, region, "cleanrooms-ml", 0, []hostPhase{
		{op: "cleanrooms-ml:ListConfiguredModelAlgorithms", run: func(p *phaseTotals) bool {
			return scanCRMLConfiguredModelAlgorithms(ctx, ml, acct, region, st, scanID, tags, p)
		}},
		{op: "cleanrooms-ml:ListTrainingDatasets", run: func(p *phaseTotals) bool {
			return scanCRMLTrainingDatasets(ctx, ml, acct, region, st, scanID, tags, p)
		}},
		{op: "cleanrooms-ml:ListAudienceModels", run: func(p *phaseTotals) bool {
			return scanCRMLAudienceModels(ctx, ml, acct, region, st, scanID, tags, p)
		}},
		{op: "cleanrooms-ml:ListConfiguredAudienceModels", run: func(p *phaseTotals) bool {
			return scanCRMLConfiguredAudienceModels(ctx, ml, acct, region, st, scanID, tags, p)
		}},
		{op: "cleanrooms-ml:ListConfiguredModelAlgorithmAssociations", run: func(p *phaseTotals) bool {
			memberships = crmlMemberships(ctx, cr, acct, region, st, p)
			return scanCRMLConfiguredModelAlgorithmAssociations(ctx, ml, memberships, acct, region, st, scanID, tags, p)
		}},
		{op: "cleanrooms-ml:ListMLInputChannels", run: func(p *phaseTotals) bool {
			return scanCRMLMLInputChannels(ctx, ml, memberships, acct, region, st, scanID, tags, p)
		}},
		{op: "cleanrooms-ml:ListTrainedModels", run: func(p *phaseTotals) bool {
			return scanCRMLTrainedModels(ctx, ml, memberships, acct, region, st, scanID, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// crmlTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries; the op answers with every tag
// at once (no page token). ResourceNotFoundException, its modeled not-found,
// drops a resource deleted after it was listed. Every listing read here
// returns this account's own resources: the account-wide ones list the
// caller's, the per-membership ones those of its own memberships.
//
// The Service Reference lists only trainingdataset, audiencemodel,
// configuredaudiencemodel and audiencegenerationjob under
// ListTagsForResource; its entry omits the configured model algorithms and
// the membership-scoped types, while its TagResource and UnTagResource
// entries list them. The op's model (its TaggableArn pattern) accepts the ARNs of
// all seven types read here, and their Get* outputs carry Tags, so all seven
// are read.
func crmlTags(client cleanRoomsMLAPI) rowTagReader {
	return rowTagReader{
		op:       crmlTagsOp,
		notFound: []string{"ResourceNotFoundException"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &cleanroomsml.ListTagsForResourceInput{ResourceArn: sdkaws.String(r.NativeID)})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// crmlRow is the row of a listed item stored under the ARN it carries, the
// item verbatim as the attributes. An item without an ARN is not stored: one
// empty NativeID would merge every such item into one resource; what names
// the items in that warning, key the item.
func crmlRow(acct *account, region, scanID, typ, what string, arn, name *string, created *time.Time, key string, item any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty ARN", key
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn),
		Name: name, Region: &region, CreatedAt: tp(created), AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// crmlStatus is a row's Status: the listed status, empty included.
func crmlStatus(status string) *string { return &status }

// crmlMemberships lists the account's Clean Rooms memberships and returns
// them, those of the pages read before a failed page included; the listing's
// error goes to p. A denial is warned about rather than returned, and a
// membership without an id is warned about, as its ML resources cannot be
// listed. An NXDOMAIN before any membership was read means the region has no
// Clean Rooms endpoint, so it has no memberships: no error.
func crmlMemberships(ctx context.Context, client cleanRoomsAPI, acct *account, region string, st *store.Store, p *phaseTotals) []childParent {
	items, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]crtypes.MembershipSummary, *string, error) {
		out, err := client.ListMemberships(ctx, &cleanrooms.ListMembershipsInput{NextToken: token, MaxResults: sdkaws.Int32(crPageCap)})
		if err != nil {
			return nil, nil, err
		}
		return out.MembershipSummaries, out.NextToken, nil
	})
	if len(items) == 0 && isDNSNotFound(listErr) {
		return nil
	}
	var (
		memberships []childParent
		noID        skipTally
	)
	for _, m := range items {
		if id := sv(m.Id); id != "" {
			memberships = append(memberships, childParent{id: id, arn: sv(m.Arn)})
			continue
		}
		noID.skipped(cmp.Or(sv(m.Arn), sv(m.CollaborationName), "(a membership with no name)"), nil)
	}
	_, _, err := storeReadRows(st, acct, region, crmlMembershipsOp, nil, listErr, func() {
		if noID.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: crmlMembershipsOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d memberships listed with an empty Id, so their ML resources were not read; first %s", noID.count, noID.first),
			})
		}
	})
	p.addPhase(crmlMembershipsOp, 0, 0, err)
	return memberships
}

// scanCRMLConfiguredModelAlgorithms stores the region's configured model
// algorithms with their tags, a page at a time.
func scanCRMLConfiguredModelAlgorithms(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "cleanrooms-ml:ListConfiguredModelAlgorithms",
		func(ctx context.Context, token *string) ([]mltypes.ConfiguredModelAlgorithmSummary, *string, error) {
			out, err := client.ListConfiguredModelAlgorithms(ctx, &cleanroomsml.ListConfiguredModelAlgorithmsInput{NextToken: token, MaxResults: sdkaws.Int32(crmlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfiguredModelAlgorithms, out.NextToken, nil
		},
		func(c mltypes.ConfiguredModelAlgorithmSummary) (*store.Resource, string, string) {
			return crmlRow(acct, region, scanID, TypeCleanRoomsMLConfiguredModelAlgorithm, "configured model algorithms",
				c.ConfiguredModelAlgorithmArn, c.Name, c.CreateTime, cmp.Or(sv(c.Name), "(a configured model algorithm with no name)"), c)
		}, tags, p)
}

// scanCRMLTrainingDatasets stores the region's training datasets with their
// tags, a page at a time.
func scanCRMLTrainingDatasets(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "cleanrooms-ml:ListTrainingDatasets",
		func(ctx context.Context, token *string) ([]mltypes.TrainingDatasetSummary, *string, error) {
			out, err := client.ListTrainingDatasets(ctx, &cleanroomsml.ListTrainingDatasetsInput{NextToken: token, MaxResults: sdkaws.Int32(crmlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TrainingDatasets, out.NextToken, nil
		},
		func(d mltypes.TrainingDatasetSummary) (*store.Resource, string, string) {
			r, why, key := crmlRow(acct, region, scanID, TypeCleanRoomsMLTrainingDataset, "training datasets",
				d.TrainingDatasetArn, d.Name, d.CreateTime, cmp.Or(sv(d.Name), "(a training dataset with no name)"), d)
			if r != nil {
				r.Status = crmlStatus(string(d.Status))
			}
			return r, why, key
		}, tags, p)
}

// scanCRMLAudienceModels stores the region's audience models with their
// tags, a page at a time.
func scanCRMLAudienceModels(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "cleanrooms-ml:ListAudienceModels",
		func(ctx context.Context, token *string) ([]mltypes.AudienceModelSummary, *string, error) {
			out, err := client.ListAudienceModels(ctx, &cleanroomsml.ListAudienceModelsInput{NextToken: token, MaxResults: sdkaws.Int32(crmlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.AudienceModels, out.NextToken, nil
		},
		func(a mltypes.AudienceModelSummary) (*store.Resource, string, string) {
			r, why, key := crmlRow(acct, region, scanID, TypeCleanRoomsMLAudienceModel, "audience models",
				a.AudienceModelArn, a.Name, a.CreateTime, cmp.Or(sv(a.Name), "(an audience model with no name)"), a)
			if r != nil {
				r.Status = crmlStatus(string(a.Status))
			}
			return r, why, key
		}, tags, p)
}

// scanCRMLConfiguredAudienceModels stores the region's configured audience
// models with their tags, a page at a time.
func scanCRMLConfiguredAudienceModels(ctx context.Context, client cleanRoomsMLAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "cleanrooms-ml:ListConfiguredAudienceModels",
		func(ctx context.Context, token *string) ([]mltypes.ConfiguredAudienceModelSummary, *string, error) {
			out, err := client.ListConfiguredAudienceModels(ctx, &cleanroomsml.ListConfiguredAudienceModelsInput{NextToken: token, MaxResults: sdkaws.Int32(crmlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfiguredAudienceModels, out.NextToken, nil
		},
		func(c mltypes.ConfiguredAudienceModelSummary) (*store.Resource, string, string) {
			r, why, key := crmlRow(acct, region, scanID, TypeCleanRoomsMLConfiguredAudienceModel, "configured audience models",
				c.ConfiguredAudienceModelArn, c.Name, c.CreateTime, cmp.Or(sv(c.Name), "(a configured audience model with no name)"), c)
			if r != nil {
				r.Status = crmlStatus(string(c.Status))
			}
			return r, why, key
		}, tags, p)
}

// storeCRMLEachMembership stores the items op lists for each membership with
// their tags, the memberships read concurrently and each page written as
// soon as its tags are read, the reads of the phase sharing one bound
// (storeEachParentPagesTagsSkip). One membership's failure does not stop the
// others, and the pages written before it are kept. A membership op refuses
// with ValidationException is skipped with one warning for op, once the
// writes succeeded.
//
// The per-membership ops model no not-found code, so a membership deleted
// after it was listed fails its listing with whatever code the service
// sends, and that error is returned. ListMemberships also keeps returning
// memberships whose status is REMOVED or COLLABORATION_DELETED; whether the
// ML ops answer those with ResourceNotFoundException is unverified live (the
// scan before this one suppressed that code without a warning).
func storeCRMLEachMembership[Item any](
	ctx context.Context, st *store.Store, acct *account, region, op string, memberships []childParent,
	page func(ctx context.Context, membership string, token *string) ([]Item, *string, error),
	row func(membership string, it Item) (*store.Resource, string, string),
	tags rowTagReader, p *phaseTotals,
) (storeWriteFailed bool) {
	var notReady skipTally
	failed := storeEachParentPagesTagsSkip(ctx, st, acct, region, op, nil, memberships, page, row, func(m string, err error) bool {
		if !isAPIErrorCode(err, "ValidationException") {
			return false
		}
		notReady.skippedLeast(m, err)
		return true
	}, tags, p)
	if !failed {
		notReady.warnCode(st, op, acct, region, "ValidationException", "membership(s)", crmlNotReadyWhy)
	}
	return failed
}

// scanCRMLConfiguredModelAlgorithmAssociations stores the configured model
// algorithm associations of each membership with their tags.
func scanCRMLConfiguredModelAlgorithmAssociations(ctx context.Context, client cleanRoomsMLAPI, memberships []childParent, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeCRMLEachMembership(ctx, st, acct, region, "cleanrooms-ml:ListConfiguredModelAlgorithmAssociations", memberships,
		func(ctx context.Context, m string, token *string) ([]mltypes.ConfiguredModelAlgorithmAssociationSummary, *string, error) {
			out, err := client.ListConfiguredModelAlgorithmAssociations(ctx, &cleanroomsml.ListConfiguredModelAlgorithmAssociationsInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crmlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfiguredModelAlgorithmAssociations, out.NextToken, nil
		},
		func(m string, a mltypes.ConfiguredModelAlgorithmAssociationSummary) (*store.Resource, string, string) {
			return crmlRow(acct, region, scanID, TypeCleanRoomsMLConfiguredModelAlgorithmAssociation, "configured model algorithm associations",
				a.ConfiguredModelAlgorithmAssociationArn, a.Name, a.CreateTime, cmp.Or(sv(a.Name), "(a configured model algorithm association with no name)")+" of membership "+m, a)
		}, tags, p)
}

// scanCRMLMLInputChannels stores the ML input channels of each membership
// with their tags.
func scanCRMLMLInputChannels(ctx context.Context, client cleanRoomsMLAPI, memberships []childParent, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeCRMLEachMembership(ctx, st, acct, region, "cleanrooms-ml:ListMLInputChannels", memberships,
		func(ctx context.Context, m string, token *string) ([]mltypes.MLInputChannelSummary, *string, error) {
			out, err := client.ListMLInputChannels(ctx, &cleanroomsml.ListMLInputChannelsInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crmlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.MlInputChannelsList, out.NextToken, nil
		},
		func(m string, c mltypes.MLInputChannelSummary) (*store.Resource, string, string) {
			r, why, key := crmlRow(acct, region, scanID, TypeCleanRoomsMLMLInputChannel, "ML input channels",
				c.MlInputChannelArn, c.Name, c.CreateTime, cmp.Or(sv(c.Name), "(an ML input channel with no name)")+" of membership "+m, c)
			if r != nil {
				r.Status = crmlStatus(string(c.Status))
			}
			return r, why, key
		}, tags, p)
}

// scanCRMLTrainedModels stores the trained models of each membership with
// their tags.
func scanCRMLTrainedModels(ctx context.Context, client cleanRoomsMLAPI, memberships []childParent, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeCRMLEachMembership(ctx, st, acct, region, "cleanrooms-ml:ListTrainedModels", memberships,
		func(ctx context.Context, m string, token *string) ([]mltypes.TrainedModelSummary, *string, error) {
			out, err := client.ListTrainedModels(ctx, &cleanroomsml.ListTrainedModelsInput{MembershipIdentifier: &m, NextToken: token, MaxResults: sdkaws.Int32(crmlPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.TrainedModels, out.NextToken, nil
		},
		func(m string, tm mltypes.TrainedModelSummary) (*store.Resource, string, string) {
			r, why, key := crmlRow(acct, region, scanID, TypeCleanRoomsMLTrainedModel, "trained models",
				tm.TrainedModelArn, tm.Name, tm.CreateTime, cmp.Or(sv(tm.Name), "(a trained model with no name)")+" of membership "+m, tm)
			if r != nil {
				r.Status = crmlStatus(string(tm.Status))
			}
			return r, why, key
		}, tags, p)
}
