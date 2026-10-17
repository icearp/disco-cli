package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/cloud9"
	cloud9types "github.com/aws/aws-sdk-go-v2/service/cloud9/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeCloud9EnvironmentEC2, Service: "cloud9"})
	// LastAccess moves each time the member opens the environment; like other
	// last-used timestamps it stays a reported change, so no Volatile rule.
	registerType(restype.Descriptor{Type: TypeCloud9EnvironmentMembership, Service: "cloud9"})
	registerService(serviceEntry{
		name: "aws:cloud9",
		fn:   scanCloud9,
	})
}

const (
	cloud9DescribeMembershipsOp = "cloud9:DescribeEnvironmentMemberships"
	// cloud9MembershipPageLimit is the model's cap on
	// DescribeEnvironmentMemberships MaxResults (range 0..25).
	cloud9MembershipPageLimit int32 = 25
)

// scanCloud9 discovers Cloud9 EC2 environments, then the memberships of every
// environment it stored. The membership phase runs only when the environment
// phase succeeds: a failed environment phase stores nothing, so there are no
// parents to fan out over.
func scanCloud9(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := cloud9.NewFromConfig(acct.cfg, func(o *cloud9.Options) { o.Region = region })
	total, inserted, envs, err := scanCloud9Environments(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	t, i, err := scanCloud9Memberships(ctx, client, acct, region, st, scanID, envs)
	return total + t, inserted + i, err
}

// scanCloud9Environments discovers Cloud9 EC2 environments. Closed to new
// customers (2024-07-31); existing tenants still use it. ListEnvironments
// returns IDs only — DescribeEnvironments fan-out fills in ARN+Type. Only
// Type=ec2 emits (CFN models only AWS::Cloud9::EnvironmentEC2; SSH variant has
// no CFN type). It returns the environments it stored that carry an Id.
func scanCloud9Environments(ctx context.Context, client *cloud9.Client, acct *account, region string, st *store.Store, scanID string) (total, inserted int, envs []childParent, err error) {
	var ids []string
	var nextToken *string
	for {
		out, err := client.ListEnvironments(ctx, &cloud9.ListEnvironmentsInput{NextToken: nextToken})
		if err != nil {
			// Cloud9 closed to new customers (2024-07-31). Never-onboarded
			// accounts surface either an empty-message AccessDeniedException
			// or one with body "This account does not have access to the
			// Cloud9 service". Can't self-enable — mark not-entitled so the
			// dispatcher renders (account: not entitled). Existing tenants
			// with real IAM gaps still carry an action-identifying message
			// and surface via skipIfAccessDenied below.
			if isClosedToNewCustomers(err) ||
				isAccessDeniedWithMessage(err, "does not have access to the Cloud9 service") {
				return 0, 0, nil, markServiceNotEntitled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "cloud9:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("cloud9:ListEnvironments: %w", err)
		}
		ids = append(ids, out.EnvironmentIds...)
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	if len(ids) == 0 {
		return 0, 0, nil, nil
	}

	var batch []*store.Resource
	// DescribeEnvironments accepts up to 25 IDs per call.
	for i := 0; i < len(ids); i += 25 {
		end := i + 25
		if end > len(ids) {
			end = len(ids)
		}
		out, err := client.DescribeEnvironments(ctx, &cloud9.DescribeEnvironmentsInput{
			EnvironmentIds: ids[i:end],
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, nil, skipIfAccessDenied(st, "cloud9:DescribeEnvironments", acct.ID, region, err)
			}
			return 0, 0, nil, fmt.Errorf("cloud9:DescribeEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			if e.Type != cloud9types.EnvironmentTypeEc2 {
				continue
			}
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloud9EnvironmentEC2, NativeID: arn,
				Name: e.Name, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
			if id := sv(e.Id); id != "" {
				envs = append(envs, childParent{id: id, arn: arn})
			}
		}
	}
	total, inserted, err = upsertBatch(st, batch, "cloud9 environments")
	if err != nil {
		return 0, 0, nil, err
	}
	return total, inserted, envs, nil
}

// cloud9MembershipNativeID keys a membership under its environment: the API
// returns no ARN for one and the Service Reference defines none. UserArn is
// the member key Create/Update/DeleteEnvironmentMembership take alongside the
// environment, and its model pattern admits "/" (IAM user paths, assumed-role
// sessions), so it goes last.
func cloud9MembershipNativeID(envARN, userARN string) string {
	return envARN + "/membership/" + userARN
}

// isCloud9EnvironmentGone reports whether err says the environment no longer
// exists, i.e. it was deleted after DescribeEnvironments returned it.
func isCloud9EnvironmentGone(err error) bool {
	return isAPIErrorCode(err, "NotFoundException")
}

// scanCloud9Memberships stores the members of each environment, a page at a
// time, and links each under its environment. envs holds only environments
// stored this scan. Per environment: NotFoundException (deleted since listing)
// is skipped silently, a denial warns once, a transient error after the SDK's
// retries is skipped with one warning for all such environments,
// BadRequestException is skipped with one warning for all environments refused
// with it, and anything else fails the phase. Rows an environment stored
// before its error stay and are counted.
//
// Skipping BadRequestException is a guess: stored environments are not
// filtered on Lifecycle.Status, which may be CREATING, CREATE_FAILED, DELETING
// or DELETE_FAILED, and whether DescribeEnvironmentMemberships refuses such an
// environment with this code has not been observed. Failing the phase would
// then fail it on every scan, and skipping silently would hide a real input
// error, so it warns. The inputs disco sets are the listed environment Id and
// a page size pinned to the model cap by a test.
func scanCloud9Memberships(ctx context.Context, client cloud9.DescribeEnvironmentMembershipsAPIClient, acct *account, region string, st *store.Store, scanID string, envs []childParent) (int, int, error) {
	return childFanOutStoredSkipNotReady(ctx, st, acct, region, cloud9DescribeMembershipsOp, envs, isCloud9EnvironmentGone, byParentARN,
		"environment(s)", "an environment in a lifecycle status such as CREATING, CREATE_FAILED or DELETING may refuse the call",
		[]string{"BadRequestException"},
		func(ctx context.Context, env childParent) (int, int, error) {
			return storeCloud9Memberships(ctx, client, acct, region, st, scanID, env)
		})
}

// storeCloud9Memberships stores one environment's members a page at a time.
func storeCloud9Memberships(ctx context.Context, client cloud9.DescribeEnvironmentMembershipsAPIClient, acct *account, region string, st *store.Store, scanID string, env childParent) (total, inserted int, err error) {
	pager := cloud9.NewDescribeEnvironmentMembershipsPaginator(client,
		&cloud9.DescribeEnvironmentMembershipsInput{EnvironmentId: &env.id},
		func(o *cloud9.DescribeEnvironmentMembershipsPaginatorOptions) {
			o.Limit = cloud9MembershipPageLimit
			o.StopOnDuplicateToken = true
		})
	parentID := store.ResourceID("aws", acct.ID, env.arn)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return total, inserted, err
		}
		var children []containedChild
		for _, m := range out.Memberships {
			userARN := sv(m.UserArn)
			if userARN == "" {
				continue
			}
			children = append(children, containedChild{parentID: parentID, row: &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeCloud9EnvironmentMembership, NativeID: cloud9MembershipNativeID(env.arn, userARN),
				Name: &userARN, Region: &region,
				AttributesJSON: mustJSON(m), DiscoveredBy: scanID,
			}})
		}
		t, i, err := upsertContained(st, cloud9DescribeMembershipsOp, children)
		if err != nil {
			return total, inserted, err
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}
