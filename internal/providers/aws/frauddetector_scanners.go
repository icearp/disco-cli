package aws

import (
	"cmp"
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/frauddetector"
	frauddetectortypes "github.com/aws/aws-sdk-go-v2/service/frauddetector/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeFraudDetectorDetector, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorEntityType, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorEventType, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorLabel, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorList, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorOutcome, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorVariable, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorModel, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorExternalModel, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorRule, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorDetectorVersion, Service: "frauddetector"})
	registerType(restype.Descriptor{Type: TypeFraudDetectorModelVersion, Service: "frauddetector"})
	registerService(serviceEntry{
		name: "aws:frauddetector",
		fn:   scanFraudDetector,
	})
}

type fraudDetectorAPI interface {
	GetDetectors(context.Context, *frauddetector.GetDetectorsInput, ...func(*frauddetector.Options)) (*frauddetector.GetDetectorsOutput, error)
	GetEntityTypes(context.Context, *frauddetector.GetEntityTypesInput, ...func(*frauddetector.Options)) (*frauddetector.GetEntityTypesOutput, error)
	GetEventTypes(context.Context, *frauddetector.GetEventTypesInput, ...func(*frauddetector.Options)) (*frauddetector.GetEventTypesOutput, error)
	GetLabels(context.Context, *frauddetector.GetLabelsInput, ...func(*frauddetector.Options)) (*frauddetector.GetLabelsOutput, error)
	GetListsMetadata(context.Context, *frauddetector.GetListsMetadataInput, ...func(*frauddetector.Options)) (*frauddetector.GetListsMetadataOutput, error)
	GetOutcomes(context.Context, *frauddetector.GetOutcomesInput, ...func(*frauddetector.Options)) (*frauddetector.GetOutcomesOutput, error)
	GetVariables(context.Context, *frauddetector.GetVariablesInput, ...func(*frauddetector.Options)) (*frauddetector.GetVariablesOutput, error)
	GetModels(context.Context, *frauddetector.GetModelsInput, ...func(*frauddetector.Options)) (*frauddetector.GetModelsOutput, error)
	GetExternalModels(context.Context, *frauddetector.GetExternalModelsInput, ...func(*frauddetector.Options)) (*frauddetector.GetExternalModelsOutput, error)
	GetRules(context.Context, *frauddetector.GetRulesInput, ...func(*frauddetector.Options)) (*frauddetector.GetRulesOutput, error)
	DescribeDetector(context.Context, *frauddetector.DescribeDetectorInput, ...func(*frauddetector.Options)) (*frauddetector.DescribeDetectorOutput, error)
	GetDetectorVersion(context.Context, *frauddetector.GetDetectorVersionInput, ...func(*frauddetector.Options)) (*frauddetector.GetDetectorVersionOutput, error)
	DescribeModelVersions(context.Context, *frauddetector.DescribeModelVersionsInput, ...func(*frauddetector.Options)) (*frauddetector.DescribeModelVersionsOutput, error)
	ListTagsForResource(context.Context, *frauddetector.ListTagsForResourceInput, ...func(*frauddetector.Options)) (*frauddetector.ListTagsForResourceOutput, error)
}

const fdListTagsOp = "frauddetector:ListTagsForResource"

// fdNotFound is the code ListTagsForResource and the per-detector and
// per-model listings model for a resource deleted since it was listed.
var fdNotFound = []string{"ResourceNotFoundException"}

// Every listing sends the maximum of its op's maxResults range trait; the
// docs give no range of their own.

func scanFraudDetector(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := frauddetector.NewFromConfig(acct.cfg, func(o *frauddetector.Options) { o.Region = region })
	return scanFraudDetectorIn(ctx, client, acct, region, st, scanID)
}

// scanFraudDetectorIn runs every listing in turn, each per-detector or
// per-model listing after the listing of its parents, which hands it the
// parents it stored. A phase's error does not stop the later ones; a failed
// write and ctx's end do (runHostPhases). The phases run one at a time, so
// readEachKey's own bound is the bound on the reads in flight. An account
// the service is closed to is reported not entitled when a listing says so
// before any listing answered with an item or an error (fdGate).
func scanFraudDetectorIn(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var (
		gate              fdGate
		tags              = fdTags(client)
		detectors, models []childParent
		modelTypes        map[string]frauddetectortypes.ModelTypeEnum
		storeWriteFailed  bool
		phase             = func(op string, run func(p *phaseTotals) bool) hostPhase {
			return hostPhase{op: op, run: func(p *phaseTotals) bool {
				if gate.err != nil {
					return false
				}
				return run(p)
			}}
		}
	)
	phases := []hostPhase{
		phase("frauddetector:GetDetectors", func(p *phaseTotals) bool {
			detectors, storeWriteFailed = scanFDDetectors(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase("frauddetector:GetEntityTypes", func(p *phaseTotals) bool {
			return scanFDEntityTypes(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("frauddetector:GetEventTypes", func(p *phaseTotals) bool {
			return scanFDEventTypes(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("frauddetector:GetLabels", func(p *phaseTotals) bool {
			return scanFDLabels(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("frauddetector:GetListsMetadata", func(p *phaseTotals) bool {
			return scanFDLists(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("frauddetector:GetOutcomes", func(p *phaseTotals) bool {
			return scanFDOutcomes(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("frauddetector:GetVariables", func(p *phaseTotals) bool {
			return scanFDVariables(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("frauddetector:GetModels", func(p *phaseTotals) bool {
			models, modelTypes, storeWriteFailed = scanFDModels(ctx, client, acct, region, st, scanID, &gate, tags, p)
			return storeWriteFailed
		}),
		phase("frauddetector:GetExternalModels", func(p *phaseTotals) bool {
			return scanFDExternalModels(ctx, client, acct, region, st, scanID, &gate, tags, p)
		}),
		phase("frauddetector:GetRules", func(p *phaseTotals) bool {
			return scanFDRules(ctx, client, acct, region, st, scanID, detectors, tags, p)
		}),
		phase("frauddetector:DescribeDetector", func(p *phaseTotals) bool {
			return scanFDDetectorVersions(ctx, client, acct, region, st, scanID, detectors, tags, p)
		}),
		phase("frauddetector:DescribeModelVersions", func(p *phaseTotals) bool {
			return scanFDModelVersions(ctx, client, acct, region, st, scanID, models, modelTypes, tags, p)
		}),
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "frauddetector", 0, phases)
	if gate.err != nil {
		return total, inserted, markServiceNotEntitled(gate.err)
	}
	return total, inserted, err
}

// fdGate records the closed-to-new-customers answer (isClosedToNewCustomers)
// of an account-wide listing's first page while every page read before it
// answered empty and without an error (answered stays false): the scan then
// reports the service not entitled and no later listing runs. Nothing was
// stored, failed or warned about by then, since only a listed item or an
// error leads to a row, an error or a warning. The same answer after an item
// or an error, or on a later page, follows answers that show the service open
// to the account, and is warned about as the denial it is coded as, beside
// the earlier phases' results.
type fdGate struct {
	err      error
	answered bool
}

// fdListPage reads one page of an account-wide listing with read, ending the
// listing without an error on the answer gate records.
func fdListPage[Item any](gate *fdGate, read func(ctx context.Context, token *string) ([]Item, *string, error)) func(ctx context.Context, token *string) ([]Item, *string, error) {
	return func(ctx context.Context, token *string) ([]Item, *string, error) {
		items, next, err := read(ctx, token)
		if err != nil && token == nil && !gate.answered && isClosedToNewCustomers(err) {
			gate.err = err
			return nil, nil, nil
		}
		if err != nil || len(items) > 0 {
			gate.answered = true
		}
		return items, next, err
	}
}

// fdTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its element carries (whose pattern the op's resourceARN
// trait accepts in every aws* partition). The op pages its answer, 50 tags a
// page (its maxResults range is exactly 50), so every page is read. Its
// model lists AccessDeniedException as its only denial code.
func fdTags(client fraudDetectorAPI) rowTagReader {
	return rowTagReader{
		op:       fdListTagsOp,
		notFound: fdNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]frauddetectortypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &frauddetector.ListTagsForResourceInput{
					ResourceARN: sdkaws.String(r.NativeID), NextToken: token, MaxResults: sdkaws.Int32(50),
				})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t frauddetectortypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// fdRow is the row of a listed item stored under its ARN, item verbatim as
// the attributes.
func fdRow(acct *account, region, scanID, typ, arn string, name *string, item any) *store.Resource {
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: arn,
		Name: name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}
}

// fdNoARN is the reason and key of a listed item not stored because it lacks
// the ARN its NativeID is; the model marks every element's Arn optional.
func fdNoARN(what, id string) (*store.Resource, string, string) {
	return nil, what + " have an empty Arn", cmp.Or(id, "(an element with no id)")
}

// fdNamed is a row's Name: the item's name, else its ARN.
func fdNamed(name *string, arn string) *string {
	return sdkaws.String(cmp.Or(sv(name), arn))
}

// scanFDDetectors stores the detectors with their tags and returns the stored
// ones, the parents of the rule and detector version phases.
func scanFDDetectors(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	return storeParentListTags(ctx, st, acct, region, "frauddetector:GetDetectors", "detectors", "DetectorId",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.Detector, *string, error) {
			out, err := client.GetDetectors(ctx, &frauddetector.GetDetectorsInput{NextToken: token, MaxResults: sdkaws.Int32(10)})
			if err != nil {
				return nil, nil, err
			}
			return out.Detectors, out.NextToken, nil
		}),
		func(d frauddetectortypes.Detector) (*store.Resource, string, string) {
			arn := sv(d.Arn)
			if arn == "" {
				return fdNoARN("detectors", sv(d.DetectorId))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorDetector, arn, fdNamed(d.DetectorId, arn), d), "", ""
		},
		func(d frauddetectortypes.Detector) string { return sv(d.DetectorId) },
		tags, p)
}

func scanFDEntityTypes(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "frauddetector:GetEntityTypes",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.EntityType, *string, error) {
			out, err := client.GetEntityTypes(ctx, &frauddetector.GetEntityTypesInput{NextToken: token, MaxResults: sdkaws.Int32(10)})
			if err != nil {
				return nil, nil, err
			}
			return out.EntityTypes, out.NextToken, nil
		}),
		func(e frauddetectortypes.EntityType) (*store.Resource, string, string) {
			arn := sv(e.Arn)
			if arn == "" {
				return fdNoARN("entity types", sv(e.Name))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorEntityType, arn, fdNamed(e.Name, arn), e), "", ""
		},
		tags, p)
}

func scanFDEventTypes(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "frauddetector:GetEventTypes",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.EventType, *string, error) {
			out, err := client.GetEventTypes(ctx, &frauddetector.GetEventTypesInput{NextToken: token, MaxResults: sdkaws.Int32(10)})
			if err != nil {
				return nil, nil, err
			}
			return out.EventTypes, out.NextToken, nil
		}),
		func(e frauddetectortypes.EventType) (*store.Resource, string, string) {
			arn := sv(e.Arn)
			if arn == "" {
				return fdNoARN("event types", sv(e.Name))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorEventType, arn, fdNamed(e.Name, arn), e), "", ""
		},
		tags, p)
}

func scanFDLabels(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "frauddetector:GetLabels",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.Label, *string, error) {
			out, err := client.GetLabels(ctx, &frauddetector.GetLabelsInput{NextToken: token, MaxResults: sdkaws.Int32(50)})
			if err != nil {
				return nil, nil, err
			}
			return out.Labels, out.NextToken, nil
		}),
		func(l frauddetectortypes.Label) (*store.Resource, string, string) {
			arn := sv(l.Arn)
			if arn == "" {
				return fdNoARN("labels", sv(l.Name))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorLabel, arn, fdNamed(l.Name, arn), l), "", ""
		},
		tags, p)
}

// scanFDLists stores the allow/deny lists with their tags. The Service
// Reference lists the list resource under ListTagsForResource and CreateList
// takes tags.
func scanFDLists(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "frauddetector:GetListsMetadata",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.AllowDenyList, *string, error) {
			out, err := client.GetListsMetadata(ctx, &frauddetector.GetListsMetadataInput{NextToken: token, MaxResults: sdkaws.Int32(50)})
			if err != nil {
				return nil, nil, err
			}
			return out.Lists, out.NextToken, nil
		}),
		func(l frauddetectortypes.AllowDenyList) (*store.Resource, string, string) {
			arn := sv(l.Arn)
			if arn == "" {
				return fdNoARN("lists", sv(l.Name))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorList, arn, fdNamed(l.Name, arn), l), "", ""
		},
		tags, p)
}

func scanFDOutcomes(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "frauddetector:GetOutcomes",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.Outcome, *string, error) {
			out, err := client.GetOutcomes(ctx, &frauddetector.GetOutcomesInput{NextToken: token, MaxResults: sdkaws.Int32(100)})
			if err != nil {
				return nil, nil, err
			}
			return out.Outcomes, out.NextToken, nil
		}),
		func(o frauddetectortypes.Outcome) (*store.Resource, string, string) {
			arn := sv(o.Arn)
			if arn == "" {
				return fdNoARN("outcomes", sv(o.Name))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorOutcome, arn, fdNamed(o.Name, arn), o), "", ""
		},
		tags, p)
}

func scanFDVariables(ctx context.Context, client fraudDetectorAPI, acct *account, region string, st *store.Store, scanID string, gate *fdGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenListTags(ctx, st, acct, region, "frauddetector:GetVariables",
		fdListPage(gate, func(ctx context.Context, token *string) ([]frauddetectortypes.Variable, *string, error) {
			out, err := client.GetVariables(ctx, &frauddetector.GetVariablesInput{NextToken: token, MaxResults: sdkaws.Int32(100)})
			if err != nil {
				return nil, nil, err
			}
			return out.Variables, out.NextToken, nil
		}),
		func(v frauddetectortypes.Variable) (*store.Resource, string, string) {
			arn := sv(v.Arn)
			if arn == "" {
				return fdNoARN("variables", sv(v.Name))
			}
			return fdRow(acct, region, scanID, TypeFraudDetectorVariable, arn, fdNamed(v.Name, arn), v), "", ""
		},
		tags, p)
}
