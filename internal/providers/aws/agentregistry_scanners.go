package aws

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol"
	artypes "github.com/aws/aws-sdk-go-v2/service/agentregistrycontrol/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAgentRegistryRegistry, Service: "agent-registry"})
	registerType(restype.Descriptor{Type: TypeAgentRegistryRegistryRecord, Service: "agent-registry"})
	registerService(serviceEntry{name: "aws:agent-registry", fn: scanAgentRegistry})
}

const (
	agentRegistryListRegistriesOp = "agent-registry:ListRegistries"
	agentRegistryListRecordsOp    = "agent-registry:ListRegistryRecords"
	agentRegistryTagsOp           = "agent-registry:ListTagsForResource"
	// agentRegistryPageCap is the model's maxResults ceiling for both list ops.
	agentRegistryPageCap = 100
)

// agentRegistryGone is the not-found code ListRegistryRecords and
// ListTagsForResource model, for a registry or record deleted after it was
// listed.
var agentRegistryGone = []string{"ResourceNotFoundException"}

// agentRegistryAPI is the narrow Agent Registry Control surface the scanners
// use; *agentregistrycontrol.Client satisfies it.
type agentRegistryAPI interface {
	ListRegistries(context.Context, *agentregistrycontrol.ListRegistriesInput, ...func(*agentregistrycontrol.Options)) (*agentregistrycontrol.ListRegistriesOutput, error)
	ListRegistryRecords(context.Context, *agentregistrycontrol.ListRegistryRecordsInput, ...func(*agentregistrycontrol.Options)) (*agentregistrycontrol.ListRegistryRecordsOutput, error)
	ListTagsForResource(context.Context, *agentregistrycontrol.ListTagsForResourceInput, ...func(*agentregistrycontrol.Options)) (*agentregistrycontrol.ListTagsForResourceOutput, error)
}

func scanAgentRegistry(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := agentregistrycontrol.NewFromConfig(acct.cfg, func(o *agentregistrycontrol.Options) { o.Region = region })
	return scanAgentRegistryAll(ctx, client, acct, region, st, scanID)
}

// scanAgentRegistryAll lists the registries with their tags, then the
// records of every registry it stored, with theirs. Both phases run
// (runHostPhases), each error kept under the op that failed, so phaseErrs
// demotes a transient one to a warning only beside a hard one. A region
// whose registries listing fails only with NXDOMAIN, nothing stored, is
// reported unavailable. Denied tag reads of both phases are one warning.
func scanAgentRegistryAll(ctx context.Context, client agentRegistryAPI, acct *account, region string, st *store.Store, scanID string) (int, error) {
	tags := agentRegistryTags(client).sharingDenials()
	var registries []childParent
	total, err := runHostPhases(ctx, st, acct, region, "agent-registry", 0, []hostPhase{
		{op: agentRegistryListRegistriesOp, run: func(p *phaseTotals) (failed bool) {
			registries, failed = scanAgentRegistryRegistries(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: agentRegistryListRecordsOp, run: func(p *phaseTotals) bool {
			return scanAgentRegistryRecords(ctx, client, acct, region, st, scanID, registries, tags, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// agentRegistryTags reads a registry's or record's tags with
// ListTagsForResource, sent the row's NativeID, the ARN its listing returned.
// The Service Reference lists both types (registry, registry-record) under
// ListTagsForResource, and the op's resourceArn documents "registries and
// registry records". ResourceNotFoundException, its modeled not-found, drops
// a resource deleted after it was listed. Both listings return only this
// account's own resources, so no read is of another account's.
func agentRegistryTags(client agentRegistryAPI) rowTagReader {
	return rowTagReader{
		op:       agentRegistryTagsOp,
		notFound: agentRegistryGone,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &agentregistrycontrol.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanAgentRegistryRegistries stores the account's registries in the region
// with their tags, a page at a time, and returns the stored ones that carry
// a RegistryId, the parents of the records phase; a later page's failure
// keeps the earlier pages' registries. One stored without a RegistryId is
// warned about, as stored without its records. It reports whether a write
// failed, after which the scan must stop.
func scanAgentRegistryRegistries(ctx context.Context, client agentRegistryAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentPagesTags(ctx, st, acct, region, agentRegistryListRegistriesOp, "registries", "RegistryId",
		func(ctx context.Context, token *string) ([]artypes.RegistrySummary, *string, error) {
			out, err := client.ListRegistries(ctx, &agentregistrycontrol.ListRegistriesInput{MaxResults: aws.Int32(agentRegistryPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Registries, out.NextToken, nil
		},
		func(r artypes.RegistrySummary) (*store.Resource, string, string) {
			return arnRow("registries", r.RegistryArn, r.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAgentRegistryRegistry, NativeID: sv(r.RegistryArn),
				Name: r.Name, Status: nonEmptyPtr(string(r.Status)), Region: &region, CreatedAt: tp(r.CreatedAt),
				AttributesJSON: mustJSON(r), DiscoveredBy: scanID,
			})
		},
		func(r artypes.RegistrySummary) string { return sv(r.RegistryId) },
		tags, p)
}

// agentRegistryNotReadyWhy is why a registry's record listing may be refused
// with ConflictException or ValidationException, both modeled for the op.
const agentRegistryNotReadyWhy = "a registry that is not READY may refuse its record listing"

// scanAgentRegistryRecords stores each registry's records (MCP servers,
// agents, skills and custom entries published to it) with their tags, under
// the registry. The registries are read concurrently and each page is
// written as soon as its tags are read (storeEachParentPagesTagsWith), since
// a registry can hold many records; one registry's failure costs the others
// none of theirs, and its own pages written before it are kept.
//
// Per registry: one deleted after it was listed (ResourceNotFoundException)
// is skipped silently when none of its records were read, and with a warning
// otherwise. ConflictException and ValidationException on a registry's first
// page skip that registry with one warning per code: a registry that is not
// READY (CREATING, CREATE_FAILED, DELETING) may refuse its record listing
// with either (not observed live), and failing the phase would fail every
// scan while it stays in that state. On a later page either is returned like
// any other error, since the registry's earlier pages were stored and the
// rest are lost. A denial warns once for the op; any other error, a
// transient one left after the SDK's retries included, is returned after the
// rows are stored. The contains pairs of the rows written are recorded in
// one closure write after the pages are, a failed row write's included, so
// the rows written before it keep their edges. It reports whether a write
// failed, after which the scan must stop.
func scanAgentRegistryRecords(ctx context.Context, client agentRegistryAPI, acct *account, region string, st *store.Store, scanID string, registries []childParent, tags rowTagReader, p *phaseTotals) bool {
	const op = agentRegistryListRecordsOp
	arnOf := make(map[string]string, len(registries))
	for _, reg := range registries {
		arnOf[reg.id] = reg.arn
	}
	var (
		conflict, notReady skipTally
		under              rowsUnder[struct{}]
		mu                 sync.Mutex // guards pastFirst: the registries are read concurrently
		pastFirst          = map[string]bool{}
	)
	failed := storeEachParentPagesTagsWith(ctx, st, acct, region, op, agentRegistryGone, registries,
		func(ctx context.Context, registryID string, token *string) ([]artypes.RegistryRecordSummary, *string, error) {
			if token != nil {
				mu.Lock()
				pastFirst[registryID] = true
				mu.Unlock()
			}
			out, err := client.ListRegistryRecords(ctx, &agentregistrycontrol.ListRegistryRecordsInput{RegistryId: &registryID, MaxResults: aws.Int32(agentRegistryPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.RegistryRecords, out.NextToken, nil
		},
		func(registryID string, rec artypes.RegistryRecordSummary) (*store.Resource, string, string) {
			r, why, key := arnRow("registry records", rec.RecordArn, rec.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAgentRegistryRegistryRecord, NativeID: sv(rec.RecordArn),
				Name: rec.Name, Status: nonEmptyPtr(string(rec.Status)), Region: &region, CreatedAt: tp(rec.CreatedAt),
				AttributesJSON: mustJSON(rec), DiscoveredBy: scanID,
			})
			if r != nil {
				under.listedUnderParent(r, arnOf[registryID], struct{}{})
			}
			return r, why, key
		},
		func(registryID string, err error) bool {
			mu.Lock()
			later := pastFirst[registryID]
			mu.Unlock()
			switch {
			case later:
				return false
			case isAPIErrorCode(err, "ConflictException"):
				conflict.skippedLeast(registryID, err)
			case isAPIErrorCode(err, "ValidationException"):
				notReady.skippedLeast(registryID, err)
			default:
				return false
			}
			return true
		},
		func(rows []*store.Resource) { under.writtenUnderParents(acct.ID, rows) },
		tags, p)
	if closureFailed := recordHierarchyPairs(st, op, under.pairs, p); failed || closureFailed {
		return true
	}
	conflict.warnCode(st, op, acct, region, "ConflictException", "registry(ies)", agentRegistryNotReadyWhy)
	notReady.warnCode(st, op, acct, region, "ValidationException", "registry(ies)", agentRegistryNotReadyWhy)
	return false
}
