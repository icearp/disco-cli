package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	connectListSecurityProfilePermissionsOp = "connect:ListSecurityProfilePermissions"
	connectListLambdaFunctionsOp            = "connect:ListLambdaFunctions"
	connectListRoutingProfileQueuesOp       = "connect:ListRoutingProfileQueues"
	// The model's MaxResults caps: MaxResult1000, MaxResult25, MaxResult100.
	connectSecurityProfilePermissionsPageCap = 1000
	connectLambdaFunctionsPageCap            = 25
	connectRoutingProfileQueuesPageCap       = 100
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectSecurityProfilePermission, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectLambdaFunction, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectRoutingProfileQueue, Service: "connect"})
}

// connectAccessAPI is the narrow surface for the access family: what a
// security profile grants, which Lambda functions an instance's flows may
// invoke, and which queues a routing profile routes from.
type connectAccessAPI interface {
	ListSecurityProfilePermissions(context.Context, *connect.ListSecurityProfilePermissionsInput, ...func(*connect.Options)) (*connect.ListSecurityProfilePermissionsOutput, error)
	ListLambdaFunctions(context.Context, *connect.ListLambdaFunctionsInput, ...func(*connect.Options)) (*connect.ListLambdaFunctionsOutput, error)
	ListRoutingProfileQueues(context.Context, *connect.ListRoutingProfileQueuesInput, ...func(*connect.Options)) (*connect.ListRoutingProfileQueuesOutput, error)
}

// scanConnectAccess runs the access phases concurrently; each fans out over
// the parent rows this scan stored, so none depends on another. Each error
// passes through demoteTransient before the join, so a throttle on one cannot
// make the dispatcher classify the service transient and hide another's hard
// error.
func scanConnectAccess(ctx context.Context, client connectAccessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	phases := []struct {
		op  string
		run func() (int, int, error)
	}{
		{connectListSecurityProfilePermissionsOp, func() (int, int, error) {
			return scanConnectSecurityProfilePermissions(ctx, client, acct, region, st, scanID)
		}},
		{connectListLambdaFunctionsOp, func() (int, int, error) {
			return scanConnectLambdaFunctions(ctx, client, acct, region, st, scanID)
		}},
		{connectListRoutingProfileQueuesOp, func() (int, int, error) {
			return scanConnectRoutingProfileQueues(ctx, client, acct, region, st, scanID)
		}},
	}
	type result struct {
		total, inserted int
		err             error
	}
	results := make([]result, len(phases))
	var wg sync.WaitGroup
	for k, p := range phases {
		wg.Go(func() {
			r := &results[k]
			r.total, r.inserted, r.err = p.run()
		})
	}
	wg.Wait()
	var (
		total, inserted int
		errs            []error
	)
	for k, r := range results {
		total += r.total
		inserted += r.inserted
		errs = append(errs, demoteTransient(st, phases[k].op, acct, region, r.err))
	}
	return total, inserted, errors.Join(errs...)
}

// connectSecurityProfilePermissionAttrs is the permission list of one
// security profile, gathered across pages.
type connectSecurityProfilePermissionAttrs struct {
	Permissions []string
}

// scanConnectSecurityProfilePermissions stores one row per security profile
// stored this scan, holding the permission names it grants, sorted so that
// page order is no change. A profile that grants nothing still gets a row
// with an empty list, so granting its first permission shows as a change on
// that row rather than as a new row. The output's LastModifiedTime and
// LastModifiedRegion describe the profile, which its own row already
// carries, so they are not repeated here.
func scanConnectSecurityProfilePermissions(ctx context.Context, client connectAccessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents, err := connectScannedParents(st, acct, region, scanID, TypeConnectSecurityProfile)
	if err != nil {
		return 0, 0, err
	}
	return childFanOut(ctx, st, acct, region, connectListSecurityProfilePermissionsOp, parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			instID, profileID := connectChildARNIDs(p.arn)
			permissions := []string{}
			pager := connect.NewListSecurityProfilePermissionsPaginator(client,
				&connect.ListSecurityProfilePermissionsInput{InstanceId: &instID, SecurityProfileId: &profileID},
				func(o *connect.ListSecurityProfilePermissionsPaginatorOptions) {
					o.Limit = connectSecurityProfilePermissionsPageCap
					o.StopOnDuplicateToken = true
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				permissions = append(permissions, out.Permissions...)
			}
			slices.Sort(permissions)
			return []*store.Resource{{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeConnectSecurityProfilePermission, NativeID: p.arn + "/permission",
				Region:         &region,
				AttributesJSON: mustJSON(connectSecurityProfilePermissionAttrs{Permissions: permissions}), DiscoveredBy: scanID,
			}}, nil
		})
}

// connectLambdaFunctionAttrs is one entry of an instance's Lambda allowlist;
// the API returns the bare function ARN.
type connectLambdaFunctionAttrs struct {
	FunctionArn string
}

// scanConnectLambdaFunctions stores one row per Lambda function ARN that an
// instance stored this scan allows its flows to invoke. ListIntegrationAssociations
// does not return these: its IntegrationType enum has no Lambda member.
func scanConnectLambdaFunctions(ctx context.Context, client connectAccessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents, err := connectStoredParents(st, acct, region, scanID, TypeConnectInstance, connectInstanceIDFromARN)
	if err != nil {
		return 0, 0, err
	}
	return childFanOut(ctx, st, acct, region, connectListLambdaFunctionsOp, parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			var rows []*store.Resource
			pager := connect.NewListLambdaFunctionsPaginator(client, &connect.ListLambdaFunctionsInput{InstanceId: &p.id},
				func(o *connect.ListLambdaFunctionsPaginatorOptions) {
					o.Limit = connectLambdaFunctionsPageCap
					o.StopOnDuplicateToken = true
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, fnARN := range out.LambdaFunctions {
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeConnectLambdaFunction, NativeID: p.arn + "/lambda-function/" + fnARN,
						Name: lambdaFunctionNameFromARN(fnARN), Region: &region,
						AttributesJSON: mustJSON(connectLambdaFunctionAttrs{FunctionArn: fnARN}), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
}

// lambdaFunctionNameFromARN returns what follows `:function:` in a Lambda
// function ARN, a qualifier included, or nil when there is no such segment.
func lambdaFunctionNameFromARN(fnARN string) *string {
	_, name, ok := strings.Cut(fnARN, ":function:")
	if !ok {
		return nil
	}
	return nonEmptyPtr(name)
}

// scanConnectRoutingProfileQueues stores one row per queue and channel of
// each routing profile stored this scan: the API lists a queue once per
// channel, each with its own priority and delay. DescribeRoutingProfile
// carries only the queue ids, not these settings. An entry without the
// model-required QueueId or Channel has no identity and is not stored; such
// entries are counted into one warning.
func scanConnectRoutingProfileQueues(ctx context.Context, client connectAccessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	parents, err := connectScannedParents(st, acct, region, scanID, TypeConnectRoutingProfile)
	if err != nil {
		return 0, 0, err
	}
	var incomplete skipTally
	total, inserted, err := childFanOut(ctx, st, acct, region, connectListRoutingProfileQueuesOp, parents, isConnectNotFound,
		func(gctx context.Context, p childParent) ([]*store.Resource, error) {
			instID, profileID := connectChildARNIDs(p.arn)
			var rows []*store.Resource
			pager := connect.NewListRoutingProfileQueuesPaginator(client,
				&connect.ListRoutingProfileQueuesInput{InstanceId: &instID, RoutingProfileId: &profileID},
				func(o *connect.ListRoutingProfileQueuesPaginatorOptions) {
					o.Limit = connectRoutingProfileQueuesPageCap
					o.StopOnDuplicateToken = true
				})
			for pager.HasMorePages() {
				out, err := pager.NextPage(gctx)
				if err != nil {
					return nil, err
				}
				for _, q := range out.RoutingProfileQueueConfigSummaryList {
					queueID, channel := sv(q.QueueId), string(q.Channel)
					if queueID == "" || channel == "" {
						incomplete.skipped(p.arn, nil)
						continue
					}
					rows = append(rows, &store.Resource{
						Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
						Type: TypeConnectRoutingProfileQueue, NativeID: p.arn + "/queue/" + queueID + "/channel/" + channel,
						Name: q.QueueName, Region: &region,
						AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
					})
				}
			}
			return rows, nil
		})
	if incomplete.count > 0 {
		st.ReportWarning(store.ScanWarning{
			Provider: "aws", Service: connectListRoutingProfileQueuesOp, Scope: acct.ID + "/" + region,
			Message: fmt.Sprintf("%d queue config(s) without the model-required QueueId or Channel not stored, first under %s; "+
				"AWS returned entries missing fields its model requires, so nothing in the account needs changing", incomplete.count, incomplete.first),
		})
	}
	return total, inserted, err
}
