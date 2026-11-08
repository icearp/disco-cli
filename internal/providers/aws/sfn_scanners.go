package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	sfntypes "github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSFNStateMachine, Service: "stepfunctions"})
	registerType(restype.Descriptor{Type: TypeSFNActivity, Service: "stepfunctions"})
	registerType(restype.Descriptor{Type: TypeSFNStateMachineAlias, Service: "stepfunctions"})
	registerType(restype.Descriptor{Type: TypeSFNStateMachineVersion, Service: "stepfunctions"})
	registerService(serviceEntry{
		name: "aws:sfn",
		fn:   scanSFN,
	})
}

// sfnAPI is the narrow set of Step Functions operations scanSFN calls.
type sfnAPI interface {
	ListStateMachines(context.Context, *sfn.ListStateMachinesInput, ...func(*sfn.Options)) (*sfn.ListStateMachinesOutput, error)
	DescribeStateMachine(context.Context, *sfn.DescribeStateMachineInput, ...func(*sfn.Options)) (*sfn.DescribeStateMachineOutput, error)
	ListActivities(context.Context, *sfn.ListActivitiesInput, ...func(*sfn.Options)) (*sfn.ListActivitiesOutput, error)
	ListStateMachineAliases(context.Context, *sfn.ListStateMachineAliasesInput, ...func(*sfn.Options)) (*sfn.ListStateMachineAliasesOutput, error)
	ListStateMachineVersions(context.Context, *sfn.ListStateMachineVersionsInput, ...func(*sfn.Options)) (*sfn.ListStateMachineVersionsOutput, error)
	ListTagsForResource(context.Context, *sfn.ListTagsForResourceInput, ...func(*sfn.Options)) (*sfn.ListTagsForResourceOutput, error)
}

const (
	sfnListStateMachinesOp = "states:ListStateMachines"
	sfnTagsOp              = "states:ListTagsForResource"
	sfnDescribeOp          = "states:DescribeStateMachine"
	// sfnPageCap is the model's PageSize maximum, the MaxResults cap of
	// every Step Functions listing scanned here.
	sfnPageCap = 1000
)

// scanSFN discovers Step Functions state machines, activities, and each state
// machine's aliases and versions, with the tags of the state machines and
// activities (sfnTags). Every listing runs as its own phase, one at a time and
// in that order: the alias and version phases read the state machines the
// first phase stored. A phase's error does not stop the later ones; a failed
// write and ctx's end do, and so does a first phase that stores nothing and
// fails only with NXDOMAIN, since the region does not serve Step Functions
// (runHostPhases). The tag reads' denials are one warning for the whole scan.
func scanSFN(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, error) {
	client := sfn.NewFromConfig(acct.cfg, sfnClientOptions(region))
	tags := sfnTags(client).sharingDenials()
	var machines []childParent
	total, err := runHostPhases(ctx, st, acct, region, "sfn", 0, []hostPhase{
		{op: sfnListStateMachinesOp, run: func(p *phaseTotals) bool {
			var failed bool
			machines, failed = scanSFNStateMachines(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "states:ListActivities", run: func(p *phaseTotals) bool {
			return scanSFNActivities(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: sfnListAliasesOp, run: func(p *phaseTotals) bool {
			return scanSFNAliases(ctx, client, acct, region, st, scanID, machines, p)
		}},
		{op: sfnListVersionsOp, run: func(p *phaseTotals) bool {
			return scanSFNVersions(ctx, client, acct, region, st, scanID, machines, p)
		}},
	})
	tags.warnDenials(st, acct, region)
	return total, err
}

// sfnClientOptions pins the client to region and makes it retry
// KmsThrottlingException, DescribeStateMachine's error for KMS throttling a
// call Step Functions makes for the caller. The model marks it a client error
// without the retryable trait, and the SDK lists it among neither its
// retryable nor its throttle codes, so a single throttle would otherwise fail
// the call unretried. It gets standard backoff only: adaptive mode's
// client-side rate limiting does not learn from a code added this way.
func sfnClientOptions(region string) func(*sfn.Options) {
	return func(o *sfn.Options) {
		o.Region = region
		o.Retryer = retry.AddWithErrorCodes(o.Retryer, "KmsThrottlingException")
	}
}

// sfnTags reads a state machine's or an activity's tags with
// ListTagsForResource, sent the row's NativeID: the ARN its listing returned.
// The Service Reference lists the activity and statemachine resources, and
// only those, on the op and on TagResource, whose doc names "the Step
// Functions state machine or activity"; CreateStateMachine and CreateActivity
// take Tags. The op returns every tag at once. ResourceNotFound, its modeled
// not-found, drops a resource deleted after it was listed.
//
// Aliases and versions get no tag read: CreateStateMachineAlias and
// PublishStateMachineVersion take no tags, neither the op nor TagResource
// lists them in the Service Reference, and their list elements carry none.
// The Service Reference does give them the aws:ResourceTag condition key; an
// alias or version ARN is a qualified state machine ARN, so that key
// presumably reads the state machine's tags (not verified against a live
// account).
func sfnTags(client sfnAPI) rowTagReader {
	return rowTagReader{
		op:       sfnTagsOp,
		notFound: []string{"ResourceNotFound"},
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &sfn.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t sfntypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// isSFNDescribeDenied reports a denied DescribeStateMachine: an IAM denial,
// or KmsAccessDeniedException, the model's error for a state machine
// encrypted with a customer managed key the caller may not use.
func isSFNDescribeDenied(err error) bool {
	return isAccessDenied(err) || isAPIErrorCode(err, "KmsAccessDeniedException")
}

// scanSFNStateMachines stores the state machines a page at a time, each row's
// attributes replaced with its DescribeStateMachine body, which carries the
// RoleArn, Definition and LoggingConfiguration resolveSFNRelationships reads,
// and its Status set from that body. A state machine DescribeStateMachine no
// longer finds (StateMachineDoesNotExist) was deleted since the listing and is
// dropped. A KmsAccessDeniedException or KmsInvalidStateException, a
// definition encrypted with a key the caller may not use or that is disabled
// or deleted, is asked again with METADATA_ONLY, which the op documents as
// answering without the encrypted definition; its body is the row, and those
// rows, once written, are warned about once (warnSFNDefinitionsNotRead). Any
// other failure, the second ask's included, keeps the StateMachineListItem as
// the row, without a Status (rowDetailReader), a denial (isSFNDescribeDenied)
// warned about once. Name comes from the list element. It returns the state
// machines stored, as the parents of the alias and version phases, and
// reports whether a write failed, after which the scan must stop.
func scanSFNStateMachines(ctx context.Context, client sfnAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (machines []childParent, storeWriteFailed bool) {
	// metadataOnly holds the KMS error of each state machine read with
	// METADATA_ONLY, tallied into noDefinition only once its row is written:
	// a row the tag read drops as deleted is not stored without a definition.
	var (
		mu           sync.Mutex
		metadataOnly = map[string]error{}
		noDefinition skipTally
	)
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: sfnDescribeOp, what: "state machine(s)", notFound: []string{"StateMachineDoesNotExist"},
			isDenied: isSFNDescribeDenied,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				arn := r.NativeID
				out, err := client.DescribeStateMachine(ctx, &sfn.DescribeStateMachineInput{StateMachineArn: &arn})
				// The op's METADATA_ONLY caveat, a labelled ARN, does not
				// apply: the listing returns unqualified state machine ARNs.
				// Not verified live: a disabled key is assumed not to block a
				// METADATA_ONLY read either; if it does, the second error is
				// handled as the first would have been.
				if isAPIErrorCode(err, "KmsAccessDeniedException", "KmsInvalidStateException") {
					kmsErr := err
					out, err = client.DescribeStateMachine(ctx, &sfn.DescribeStateMachineInput{StateMachineArn: &arn, IncludedData: sfntypes.IncludedDataMetadataOnly})
					if err == nil {
						mu.Lock()
						metadataOnly[arn] = kmsErr
						mu.Unlock()
					}
				}
				if err != nil {
					return nil, err
				}
				// The reads run concurrently, each on its own row only.
				status := string(out.Status)
				r.Status = &status
				return out, nil
			},
		},
		// stored runs after the page's detail reads have returned, so it reads
		// metadataOnly without the lock.
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				machines = append(machines, childParent{id: r.NativeID, arn: r.NativeID})
				if kmsErr, ok := metadataOnly[r.NativeID]; ok {
					noDefinition.skipped(r.NativeID, kmsErr)
				}
			}
		},
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, sfnListStateMachinesOp,
		func(ctx context.Context, token *string) ([]sfntypes.StateMachineListItem, *string, error) {
			out, err := client.ListStateMachines(ctx, &sfn.ListStateMachinesInput{MaxResults: sfnPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StateMachines, out.NextToken, nil
		},
		func(sm sfntypes.StateMachineListItem) (*store.Resource, string, string) {
			return arnRow("state machines", sm.StateMachineArn, sm.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSFNStateMachine, NativeID: sv(sm.StateMachineArn),
				Name: sm.Name, Region: &region,
				AttributesJSON: mustJSON(sm), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
	if !failed {
		warnSFNDefinitionsNotRead(st, acct, region, &noDefinition)
	}
	return machines, failed
}

// warnSFNDefinitionsNotRead warns once for the state machines t counted,
// stored without their definition because DescribeStateMachine could not
// decrypt it with the state machine's key, naming the first and its error. Call it only once the rows are
// stored.
func warnSFNDefinitionsNotRead(st *store.Store, acct *account, region string, t *skipTally) {
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: sfnDescribeOp, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d state machine(s) stored without their definition, first %s: %v", t.count, t.first, t.err),
	})
}

// scanSFNActivities stores the activities, each page written as soon as its
// rows' tags are read. It reports whether a write failed, after which the
// scan must stop.
func scanSFNActivities(ctx context.Context, client sfnAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "states:ListActivities",
		func(ctx context.Context, token *string) ([]sfntypes.ActivityListItem, *string, error) {
			out, err := client.ListActivities(ctx, &sfn.ListActivitiesInput{MaxResults: sfnPageCap, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Activities, out.NextToken, nil
		},
		func(a sfntypes.ActivityListItem) (*store.Resource, string, string) {
			return arnRow("activities", a.ActivityArn, a.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSFNActivity, NativeID: sv(a.ActivityArn),
				Name: a.Name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}, tags, p)
}
