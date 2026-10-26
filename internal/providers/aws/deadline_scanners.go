package aws

import (
	"cmp"
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/deadline"
	dltypes "github.com/aws/aws-sdk-go-v2/service/deadline/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeDeadlineFarm, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineBudget, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineVolume, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineFleet, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineLicenseEndpoint, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineLimit, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineMeteredProduct, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineMonitor, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueue, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueueEnvironment, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueueFleetAssociation, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineQueueLimitAssociation, Service: "deadline"})
	registerType(restype.Descriptor{Type: TypeDeadlineStorageProfile, Service: "deadline"})
	registerService(serviceEntry{
		name: "aws:deadline",
		fn:   scanDeadline,
	})
}

type deadlineAPI interface {
	ListFarms(context.Context, *deadline.ListFarmsInput, ...func(*deadline.Options)) (*deadline.ListFarmsOutput, error)
	ListFleets(context.Context, *deadline.ListFleetsInput, ...func(*deadline.Options)) (*deadline.ListFleetsOutput, error)
	ListLicenseEndpoints(context.Context, *deadline.ListLicenseEndpointsInput, ...func(*deadline.Options)) (*deadline.ListLicenseEndpointsOutput, error)
	ListLimits(context.Context, *deadline.ListLimitsInput, ...func(*deadline.Options)) (*deadline.ListLimitsOutput, error)
	ListFarmMembers(context.Context, *deadline.ListFarmMembersInput, ...func(*deadline.Options)) (*deadline.ListFarmMembersOutput, error)
	ListFleetMembers(context.Context, *deadline.ListFleetMembersInput, ...func(*deadline.Options)) (*deadline.ListFleetMembersOutput, error)
	ListQueueMembers(context.Context, *deadline.ListQueueMembersInput, ...func(*deadline.Options)) (*deadline.ListQueueMembersOutput, error)
	ListMeteredProducts(context.Context, *deadline.ListMeteredProductsInput, ...func(*deadline.Options)) (*deadline.ListMeteredProductsOutput, error)
	ListMonitors(context.Context, *deadline.ListMonitorsInput, ...func(*deadline.Options)) (*deadline.ListMonitorsOutput, error)
	ListQueues(context.Context, *deadline.ListQueuesInput, ...func(*deadline.Options)) (*deadline.ListQueuesOutput, error)
	ListQueueEnvironments(context.Context, *deadline.ListQueueEnvironmentsInput, ...func(*deadline.Options)) (*deadline.ListQueueEnvironmentsOutput, error)
	ListQueueFleetAssociations(context.Context, *deadline.ListQueueFleetAssociationsInput, ...func(*deadline.Options)) (*deadline.ListQueueFleetAssociationsOutput, error)
	ListQueueLimitAssociations(context.Context, *deadline.ListQueueLimitAssociationsInput, ...func(*deadline.Options)) (*deadline.ListQueueLimitAssociationsOutput, error)
	ListStorageProfiles(context.Context, *deadline.ListStorageProfilesInput, ...func(*deadline.Options)) (*deadline.ListStorageProfilesOutput, error)
	ListBudgets(context.Context, *deadline.ListBudgetsInput, ...func(*deadline.Options)) (*deadline.ListBudgetsOutput, error)
	ListVolumes(context.Context, *deadline.ListVolumesInput, ...func(*deadline.Options)) (*deadline.ListVolumesOutput, error)
	ListTagsForResource(context.Context, *deadline.ListTagsForResourceInput, ...func(*deadline.Options)) (*deadline.ListTagsForResourceOutput, error)
}

const (
	deadlineListTagsOp = "deadline:ListTagsForResource"

	// deadlineMaxResults is the maxResults cap the model documents for every
	// Deadline list op disco calls.
	deadlineMaxResults int32 = 100
)

// deadlineNotFound is the not-found code the model lists for the tag read and
// for every per-parent listing: the resource, or the parent the input names,
// was deleted after it was listed.
var deadlineNotFound = []string{"ResourceNotFoundException"}

// dlFarmRef is a farm stored this scan, with the fleets and queues stored
// under it, the parents of the per-fleet and per-queue phases.
type dlFarmRef struct {
	id, arn string
	queues  []childParent
	fleets  []childParent
}

// dlChildKey is the farm id and own id of a fleet or queue, both of which its
// child listings take.
type dlChildKey struct{ farmID, id string }

// deadlineARN builds every Deadline ARN, the NativeID each tag read sends, in
// the account's partition.
func deadlineARN(partition, region, acct, resource string) string {
	return arnFor(partition, "deadline", region, acct, resource)
}

func deadlineFarmARN(partition, region, acct, farmID string) string {
	return deadlineARN(partition, region, acct, "farm/"+farmID)
}

func deadlineLicenseEndpointARN(partition, region, acct, id string) string {
	return deadlineARN(partition, region, acct, "license-endpoint/"+id)
}

// deadlineFarmParents is the farms as the parents of the per-farm phases,
// keyed by farm id.
func deadlineFarmParents(farms []dlFarmRef) []childParent {
	parents := make([]childParent, len(farms))
	for i, f := range farms {
		parents[i] = childParent{id: f.id, arn: f.arn}
	}
	return parents
}

// deadlineFarmChildren is the fleets or queues (pick) of every farm as
// parents keyed by their ARN, which names the farm too (the model does not
// say a fleet or queue id is unique across farms), with each one's farm id
// and own id by ARN.
func deadlineFarmChildren(farms []dlFarmRef, pick func(dlFarmRef) []childParent) ([]childParent, map[string]dlChildKey) {
	var parents []childParent
	keys := map[string]dlChildKey{}
	for _, f := range farms {
		for _, c := range pick(f) {
			parents = append(parents, childParent{id: c.arn, arn: c.arn})
			keys[c.arn] = dlChildKey{farmID: f.id, id: c.id}
		}
	}
	return parents, keys
}

// deadlineRow is the row of a listed Deadline item stored under nativeID,
// item verbatim as the attributes.
func deadlineRow(acct *account, region, scanID, typ, nativeID, name string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: &name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// deadlineTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID: no list element carries an ARN, so every type read through it is
// stored under one built in the ARN format the Service Reference documents
// for it. Those are the types the Service Reference lists for the action:
// farms, fleets, queues, budgets, volumes, license endpoints and monitors.
// Limits, storage profiles, queue environments, metered products, the
// queue-fleet and queue-limit associations and the members are not read: the
// action lists none of them, the Service Reference gives none an ARN format,
// and none of their Create, Put or Associate ops takes Tags. The op answers
// with every tag at once (no page token).
func deadlineTags(client deadlineAPI) rowTagReader {
	return rowTagReader{
		op:       deadlineListTagsOp,
		notFound: deadlineNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &deadline.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

func scanDeadline(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := deadline.NewFromConfig(acct.cfg, func(o *deadline.Options) { o.Region = region })
	return scanDeadlineAll(ctx, client, acct, region, st, scanID)
}

// scanDeadlineAll discovers Deadline Cloud resources in one region, running
// its phases one after another. A phase's error does not stop the later
// ones; a failed write does, since every later write would fail too, and so
// does ctx's end, reported as one error. A child phase gets the parents its
// parent phase stored: the per-farm phases the farms, the queue environments
// and queue members the queues, the volumes and fleet members the fleets, and
// the metered products the license endpoints. The phases run one at a time,
// so readRowTags's own bound is the bound on the tag reads in flight. Every op
// calls the one Deadline management endpoint, so an NXDOMAIN after rows were
// stored is a failure of this scan, not a region gap (resultLateNXDOMAIN).
func scanDeadlineAll(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		p     phaseTotals
		tags  = deadlineTags(client)
		farms []dlFarmRef
		les   []childParent
	)
	counted := func(op string, run func() (int, int, error)) func() bool {
		return func() bool {
			t, i, err := run()
			return p.addPhase(op, t, i, err)
		}
	}
	phases := []func() bool{
		func() (stop bool) {
			farms, stop = scanDeadlineFarms(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		func() bool { return scanDeadlineFleets(ctx, client, acct, region, st, scanID, farms, tags, &p) },
		counted("deadline:ListLimits", func() (int, int, error) {
			return scanDeadlineLimits(ctx, client, acct, region, st, scanID, farms)
		}),
		counted("deadline:ListStorageProfiles", func() (int, int, error) {
			return scanDeadlineStorageProfiles(ctx, client, acct, region, st, scanID, farms)
		}),
		func() bool { return scanDeadlineBudgets(ctx, client, acct, region, st, scanID, farms, tags, &p) },
		func() bool { return scanDeadlineQueues(ctx, client, acct, region, st, scanID, farms, tags, &p) },
		counted("deadline:ListQueueFleetAssociations", func() (int, int, error) {
			return scanDeadlineQueueFleetAssociations(ctx, client, acct, region, st, scanID, farms)
		}),
		counted("deadline:ListQueueLimitAssociations", func() (int, int, error) {
			return scanDeadlineQueueLimitAssociations(ctx, client, acct, region, st, scanID, farms)
		}),
		counted("deadline:ListQueueEnvironments", func() (int, int, error) {
			return scanDeadlineQueueEnvironments(ctx, client, acct, region, st, scanID, farms)
		}),
		func() bool { return scanDeadlineVolumes(ctx, client, acct, region, st, scanID, farms, tags, &p) },
		func() (stop bool) {
			les, stop = scanDeadlineLicenseEndpoints(ctx, client, acct, region, st, scanID, tags, &p)
			return stop
		},
		counted("deadline:ListMeteredProducts", func() (int, int, error) {
			return scanDeadlineMeteredProducts(ctx, client, acct, region, st, scanID, les)
		}),
		func() bool { return scanDeadlineMonitors(ctx, client, acct, region, st, scanID, tags, &p) },
		counted(deadlineListFarmMembersOp, func() (int, int, error) {
			return scanDeadlineFarmMembers(ctx, client, acct, region, st, scanID, farms)
		}),
		counted(deadlineListFleetMembersOp, func() (int, int, error) {
			return scanDeadlineFleetMembers(ctx, client, acct, region, st, scanID, farms)
		}),
		counted(deadlineListQueueMembersOp, func() (int, int, error) {
			return scanDeadlineQueueMembers(ctx, client, acct, region, st, scanID, farms)
		}),
	}
	notRun := 0
	for i, run := range phases {
		if ctx.Err() != nil {
			notRun = len(phases) - i
			break
		}
		if run() {
			break
		}
	}
	p.foldStop(ctx, "deadline", notRun)
	return p.resultLateNXDOMAIN(st, acct, region)
}

// scanDeadlineFarms stores the account's farms with their tags and returns
// the stored ones, the parents of the per-farm phases.
func scanDeadlineFarms(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (farms []dlFarmRef, storeWriteFailed bool) {
	parents, stop := storeParentListTags(ctx, st, acct, region, "deadline:ListFarms", "farms", "FarmId",
		func(ctx context.Context, token *string) ([]dltypes.FarmSummary, *string, error) {
			out, err := client.ListFarms(ctx, &deadline.ListFarmsInput{NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Farms, out.NextToken, nil
		},
		func(f dltypes.FarmSummary) (*store.Resource, string, string) {
			id := sv(f.FarmId)
			if id == "" {
				return nil, "farms have an empty FarmId", cmp.Or(sv(f.DisplayName), "(a farm with no name)")
			}
			return deadlineRow(acct, region, scanID, TypeDeadlineFarm, deadlineFarmARN(acct.partition(), region, acct.ID, id), cmp.Or(sv(f.DisplayName), id), f), "", ""
		},
		func(f dltypes.FarmSummary) string { return sv(f.FarmId) },
		tags, p)
	for _, f := range parents {
		farms = append(farms, dlFarmRef{id: f.id, arn: f.arn})
	}
	return farms, stop
}

// attachFarmChildren records each stored fleet or queue on its farm (add),
// keys holding the farm id and own id of every row built.
func attachFarmChildren(farms []dlFarmRef, stored []*store.Resource, keys map[*store.Resource]dlChildKey, add func(f *dlFarmRef, c childParent)) {
	byID := make(map[string]int, len(farms))
	for i, f := range farms {
		byID[f.id] = i
	}
	for _, r := range stored {
		k := keys[r]
		add(&farms[byID[k.farmID]], childParent{id: k.id, arn: r.NativeID})
	}
}

// scanDeadlineFleets lists the fleets of each farm the farm phase stored,
// stores them with their tags and records the stored ones on their farms,
// for the volume and fleet-member phases. A farm ListFleets no longer finds
// is skipped.
func scanDeadlineFleets(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	keys := map[*store.Resource]dlChildKey{}
	stored, stop := storeEachParentTags(ctx, st, acct, region, "deadline:ListFleets", deadlineNotFound, deadlineFarmParents(farms),
		func(ctx context.Context, farmID string, token *string) ([]dltypes.FleetSummary, *string, error) {
			out, err := client.ListFleets(ctx, &deadline.ListFleetsInput{FarmId: &farmID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Fleets, out.NextToken, nil
		},
		func(farmID string, f dltypes.FleetSummary) (*store.Resource, string, string) {
			id := sv(f.FleetId)
			if id == "" {
				return nil, "fleets have an empty FleetId", cmp.Or(sv(f.DisplayName), "(a fleet with no name)") + " of farm " + farmID
			}
			r := deadlineRow(acct, region, scanID, TypeDeadlineFleet, deadlineFarmARN(acct.partition(), region, acct.ID, farmID)+"/fleet/"+id, cmp.Or(sv(f.DisplayName), id), f)
			keys[r] = dlChildKey{farmID: farmID, id: id}
			return r, "", ""
		}, tags, p)
	attachFarmChildren(farms, stored, keys, func(f *dlFarmRef, c childParent) { f.fleets = append(f.fleets, c) })
	return stop
}

// scanDeadlineQueues lists the queues of each farm the farm phase stored,
// stores them with their tags and records the stored ones on their farms,
// for the queue-environment and queue-member phases. A farm ListQueues no
// longer finds is skipped.
func scanDeadlineQueues(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	keys := map[*store.Resource]dlChildKey{}
	stored, stop := storeEachParentTags(ctx, st, acct, region, "deadline:ListQueues", deadlineNotFound, deadlineFarmParents(farms),
		func(ctx context.Context, farmID string, token *string) ([]dltypes.QueueSummary, *string, error) {
			out, err := client.ListQueues(ctx, &deadline.ListQueuesInput{FarmId: &farmID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Queues, out.NextToken, nil
		},
		func(farmID string, q dltypes.QueueSummary) (*store.Resource, string, string) {
			id := sv(q.QueueId)
			if id == "" {
				return nil, "queues have an empty QueueId", cmp.Or(sv(q.DisplayName), "(a queue with no name)") + " of farm " + farmID
			}
			r := deadlineRow(acct, region, scanID, TypeDeadlineQueue, deadlineFarmARN(acct.partition(), region, acct.ID, farmID)+"/queue/"+id, cmp.Or(sv(q.DisplayName), id), q)
			keys[r] = dlChildKey{farmID: farmID, id: id}
			return r, "", ""
		}, tags, p)
	attachFarmChildren(farms, stored, keys, func(f *dlFarmRef, c childParent) { f.queues = append(f.queues, c) })
	return stop
}

// scanDeadlineLimits stores the limits of each farm the farm phase stored. A
// farm ListLimits no longer finds is skipped.
func scanDeadlineLimits(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "deadline:ListLimits", deadlineNotFound, childParentIDs(deadlineFarmParents(farms)),
		func(ctx context.Context, farmID string, token *string) ([]dltypes.LimitSummary, *string, error) {
			out, err := client.ListLimits(ctx, &deadline.ListLimitsInput{FarmId: &farmID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Limits, out.NextToken, nil
		},
		func(farmID string, l dltypes.LimitSummary) (*store.Resource, string, string) {
			id := sv(l.LimitId)
			if id == "" {
				return nil, "limits have an empty LimitId", cmp.Or(sv(l.DisplayName), "(a limit with no name)") + " of farm " + farmID
			}
			return deadlineRow(acct, region, scanID, TypeDeadlineLimit, deadlineFarmARN(acct.partition(), region, acct.ID, farmID)+"/limit/"+id, cmp.Or(sv(l.DisplayName), id), l), "", ""
		})
}

// scanDeadlineStorageProfiles stores the storage profiles of each farm the
// farm phase stored. A farm ListStorageProfiles no longer finds is skipped.
func scanDeadlineStorageProfiles(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "deadline:ListStorageProfiles", deadlineNotFound, childParentIDs(deadlineFarmParents(farms)),
		func(ctx context.Context, farmID string, token *string) ([]dltypes.StorageProfileSummary, *string, error) {
			out, err := client.ListStorageProfiles(ctx, &deadline.ListStorageProfilesInput{FarmId: &farmID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.StorageProfiles, out.NextToken, nil
		},
		func(farmID string, sp dltypes.StorageProfileSummary) (*store.Resource, string, string) {
			id := sv(sp.StorageProfileId)
			if id == "" {
				return nil, "storage profiles have an empty StorageProfileId", cmp.Or(sv(sp.DisplayName), "(a storage profile with no name)") + " of farm " + farmID
			}
			return deadlineRow(acct, region, scanID, TypeDeadlineStorageProfile, deadlineFarmARN(acct.partition(), region, acct.ID, farmID)+"/storage-profile/"+id, cmp.Or(sv(sp.DisplayName), id), sp), "", ""
		})
}

// scanDeadlineQueueFleetAssociations stores the queue-fleet associations of
// each farm the farm phase stored. A farm the listing no longer finds is
// skipped.
func scanDeadlineQueueFleetAssociations(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "deadline:ListQueueFleetAssociations", deadlineNotFound, childParentIDs(deadlineFarmParents(farms)),
		func(ctx context.Context, farmID string, token *string) ([]dltypes.QueueFleetAssociationSummary, *string, error) {
			out, err := client.ListQueueFleetAssociations(ctx, &deadline.ListQueueFleetAssociationsInput{FarmId: &farmID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.QueueFleetAssociations, out.NextToken, nil
		},
		func(farmID string, a dltypes.QueueFleetAssociationSummary) (*store.Resource, string, string) {
			qid, fid := sv(a.QueueId), sv(a.FleetId)
			if qid == "" || fid == "" {
				return nil, "queue-fleet associations have an empty QueueId or FleetId", fmt.Sprintf("queue %q fleet %q of farm %s", qid, fid, farmID)
			}
			arn := fmt.Sprintf("%s/queue/%s/fleet/%s/association", deadlineFarmARN(acct.partition(), region, acct.ID, farmID), qid, fid)
			return deadlineRow(acct, region, scanID, TypeDeadlineQueueFleetAssociation, arn, qid+"/"+fid, a), "", ""
		})
}

// scanDeadlineQueueLimitAssociations stores the queue-limit associations of
// each farm the farm phase stored. A farm the listing no longer finds is
// skipped.
func scanDeadlineQueueLimitAssociations(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "deadline:ListQueueLimitAssociations", deadlineNotFound, childParentIDs(deadlineFarmParents(farms)),
		func(ctx context.Context, farmID string, token *string) ([]dltypes.QueueLimitAssociationSummary, *string, error) {
			out, err := client.ListQueueLimitAssociations(ctx, &deadline.ListQueueLimitAssociationsInput{FarmId: &farmID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.QueueLimitAssociations, out.NextToken, nil
		},
		func(farmID string, a dltypes.QueueLimitAssociationSummary) (*store.Resource, string, string) {
			qid, lid := sv(a.QueueId), sv(a.LimitId)
			if qid == "" || lid == "" {
				return nil, "queue-limit associations have an empty QueueId or LimitId", fmt.Sprintf("queue %q limit %q of farm %s", qid, lid, farmID)
			}
			arn := fmt.Sprintf("%s/queue/%s/limit/%s/association", deadlineFarmARN(acct.partition(), region, acct.ID, farmID), qid, lid)
			return deadlineRow(acct, region, scanID, TypeDeadlineQueueLimitAssociation, arn, qid+"/"+lid, a), "", ""
		})
}

// scanDeadlineQueueEnvironments stores the environments of each queue the
// queue phase stored. A queue the listing no longer finds is skipped.
func scanDeadlineQueueEnvironments(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, farms []dlFarmRef) (int, int, error) {
	queues, keys := deadlineFarmChildren(farms, func(f dlFarmRef) []childParent { return f.queues })
	return storeTokenEachParent(ctx, st, acct, region, "deadline:ListQueueEnvironments", deadlineNotFound, childParentIDs(queues),
		func(ctx context.Context, queueARN string, token *string) ([]dltypes.QueueEnvironmentSummary, *string, error) {
			k := keys[queueARN]
			out, err := client.ListQueueEnvironments(ctx, &deadline.ListQueueEnvironmentsInput{
				FarmId: &k.farmID, QueueId: &k.id, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Environments, out.NextToken, nil
		},
		func(queueARN string, e dltypes.QueueEnvironmentSummary) (*store.Resource, string, string) {
			id := sv(e.QueueEnvironmentId)
			if id == "" {
				return nil, "queue environments have an empty QueueEnvironmentId", cmp.Or(sv(e.Name), "(an environment with no name)") + " of queue " + queueARN
			}
			return deadlineRow(acct, region, scanID, TypeDeadlineQueueEnvironment, queueARN+"/queue-environment/"+id, cmp.Or(sv(e.Name), id), e), "", ""
		})
}

// scanDeadlineLicenseEndpoints stores the account's license endpoints with
// their tags and returns the stored ones, keyed by id, the parents of the
// metered-product phase.
func scanDeadlineLicenseEndpoints(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (les []childParent, storeWriteFailed bool) {
	return storeParentListTags(ctx, st, acct, region, "deadline:ListLicenseEndpoints", "license endpoints", "LicenseEndpointId",
		func(ctx context.Context, token *string) ([]dltypes.LicenseEndpointSummary, *string, error) {
			out, err := client.ListLicenseEndpoints(ctx, &deadline.ListLicenseEndpointsInput{NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.LicenseEndpoints, out.NextToken, nil
		},
		func(le dltypes.LicenseEndpointSummary) (*store.Resource, string, string) {
			id := sv(le.LicenseEndpointId)
			if id == "" {
				return nil, "license endpoints have an empty LicenseEndpointId", cmp.Or(sv(le.VpcId), "(a license endpoint with no VPC)")
			}
			return deadlineRow(acct, region, scanID, TypeDeadlineLicenseEndpoint, deadlineLicenseEndpointARN(acct.partition(), region, acct.ID, id), id, le), "", ""
		},
		func(le dltypes.LicenseEndpointSummary) string { return sv(le.LicenseEndpointId) },
		tags, p)
}

// scanDeadlineMeteredProducts stores the metered products of each license
// endpoint the license-endpoint phase stored. An endpoint the listing no
// longer finds is skipped.
func scanDeadlineMeteredProducts(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, les []childParent) (int, int, error) {
	return storeTokenEachParent(ctx, st, acct, region, "deadline:ListMeteredProducts", deadlineNotFound, childParentIDs(les),
		func(ctx context.Context, leID string, token *string) ([]dltypes.MeteredProductSummary, *string, error) {
			out, err := client.ListMeteredProducts(ctx, &deadline.ListMeteredProductsInput{LicenseEndpointId: &leID, NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.MeteredProducts, out.NextToken, nil
		},
		func(leID string, m dltypes.MeteredProductSummary) (*store.Resource, string, string) {
			pid := sv(m.ProductId)
			if pid == "" {
				return nil, "metered products have an empty ProductId", cmp.Or(sv(m.Family), "(a product with no family)") + " of license endpoint " + leID
			}
			return deadlineRow(acct, region, scanID, TypeDeadlineMeteredProduct, deadlineLicenseEndpointARN(acct.partition(), region, acct.ID, leID)+"/metered-product/"+pid, pid, m), "", ""
		})
}

// scanDeadlineMonitors stores the account's monitors with their tags.
func scanDeadlineMonitors(ctx context.Context, client deadlineAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenListTags(ctx, st, acct, region, "deadline:ListMonitors",
		func(ctx context.Context, token *string) ([]dltypes.MonitorSummary, *string, error) {
			out, err := client.ListMonitors(ctx, &deadline.ListMonitorsInput{NextToken: token, MaxResults: sdkaws.Int32(deadlineMaxResults)})
			if err != nil {
				return nil, nil, err
			}
			return out.Monitors, out.NextToken, nil
		},
		func(m dltypes.MonitorSummary) (*store.Resource, string, string) {
			id := sv(m.MonitorId)
			if id == "" {
				return nil, "monitors have an empty MonitorId", cmp.Or(sv(m.DisplayName), "(a monitor with no name)")
			}
			return deadlineRow(acct, region, scanID, TypeDeadlineMonitor, deadlineARN(acct.partition(), region, acct.ID, "monitor/"+id), cmp.Or(sv(m.DisplayName), id), m), "", ""
		},
		tags, p)
}
