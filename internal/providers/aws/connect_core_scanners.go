package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectInstance, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectTrafficDistributionGroup, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectPhoneNumber, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectEmailAddress, Service: "connect"})
}

// connectCoreAPI is the narrow surface for the Core family: top-level
// Instance, account-scoped TrafficDistributionGroup/PhoneNumber (via V2),
// and per-instance EmailAddress.
type connectCoreAPI interface {
	connectInstanceLister
	DescribeInstance(context.Context, *connect.DescribeInstanceInput, ...func(*connect.Options)) (*connect.DescribeInstanceOutput, error)
	ListTrafficDistributionGroups(context.Context, *connect.ListTrafficDistributionGroupsInput, ...func(*connect.Options)) (*connect.ListTrafficDistributionGroupsOutput, error)
	DescribeTrafficDistributionGroup(context.Context, *connect.DescribeTrafficDistributionGroupInput, ...func(*connect.Options)) (*connect.DescribeTrafficDistributionGroupOutput, error)
	ListPhoneNumbersV2(context.Context, *connect.ListPhoneNumbersV2Input, ...func(*connect.Options)) (*connect.ListPhoneNumbersV2Output, error)
	DescribePhoneNumber(context.Context, *connect.DescribePhoneNumberInput, ...func(*connect.Options)) (*connect.DescribePhoneNumberOutput, error)
	SearchEmailAddresses(context.Context, *connect.SearchEmailAddressesInput, ...func(*connect.Options)) (*connect.SearchEmailAddressesOutput, error)
}

// scanConnectCore runs the Core phases: instances, traffic distribution
// groups, claimed phone numbers, and per-instance email addresses.
func scanConnectCore(ctx context.Context, client connectCoreAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	return connectRunPhases(st, acct, region,
		connectPhase{"connect instances", func() (int, int, error) {
			return scanConnectInstances(ctx, client, instances, acct, region, st, scanID)
		}},
		connectPhase{"connect traffic distribution groups", func() (int, int, error) {
			return scanConnectTrafficDistributionGroups(ctx, client, acct, region, st, scanID)
		}},
		connectPhase{"connect phone numbers", func() (int, int, error) {
			return scanConnectPhoneNumbers(ctx, client, acct, region, st, scanID)
		}},
		connectPhase{"connect email addresses", func() (int, int, error) {
			return scanConnectEmailAddresses(ctx, client, instances, acct, region, st, scanID)
		}},
	)
}

// scanConnectInstances describes each instance from the pre-fetched list.
// NativeID = Instance ARN.
func scanConnectInstances(ctx context.Context, client connectCoreAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := &connectListing[cttypes.InstanceSummary]{op: "connect:ListInstances"}
	for _, i := range instances {
		listed.add("", sv(i.Id), sv(i.Arn), i)
	}
	return connectDescribe[cttypes.InstanceSummary]{
		rtype: TypeConnectInstance, op: "connect:DescribeInstance", label: "connect instances",
		describe: func(ctx context.Context, it connectListed[cttypes.InstanceSummary], r *store.Resource) error {
			out, err := client.DescribeInstance(ctx, &connect.DescribeInstanceInput{InstanceId: &it.id})
			if err != nil {
				return err
			}
			inst := out.Instance
			if inst == nil {
				return errConnectNoBody
			}
			alias, status := sv(inst.InstanceAlias), string(inst.InstanceStatus)
			r.Name, r.Status, r.CreatedAt = &alias, &status, tp(inst.CreatedTime)
			r.TagsJSON = mapTagsJSON(inst.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.InstanceSummary) {
			alias, status := sv(s.InstanceAlias), string(s.InstanceStatus)
			r.Name, r.Status, r.CreatedAt = &alias, &status, tp(s.CreatedTime)
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

// scanConnectTrafficDistributionGroups lists account-scoped TDGs then
// describes each.
func scanConnectTrafficDistributionGroups(ctx context.Context, client connectCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := &connectListing[cttypes.TrafficDistributionGroupSummary]{op: "connect:ListTrafficDistributionGroups"}
	err := connectReadPages(ctx, "", listed,
		connect.NewListTrafficDistributionGroupsPaginator(client, &connect.ListTrafficDistributionGroupsInput{},
			func(o *connect.ListTrafficDistributionGroupsPaginatorOptions) { o.Limit = 10 }),
		func(o *connect.ListTrafficDistributionGroupsOutput) []cttypes.TrafficDistributionGroupSummary {
			return o.TrafficDistributionGroupSummaryList
		},
		func(s cttypes.TrafficDistributionGroupSummary) (string, string) { return sv(s.Id), sv(s.Arn) })
	// Global-Resiliency TDGs need a provisioned Connect instance; without
	// one AWS denies via resource-based-policy explicit deny — expected
	// env state, silent-skip. (Distinct from isSCPExplicitDeny, which
	// matches "service control policy".)
	if err != nil && !isAccessDeniedWithMessage(err, "explicit deny in a resource-based policy") {
		listed.failed(st, acct, region, region, err)
	}
	return connectDescribe[cttypes.TrafficDistributionGroupSummary]{
		rtype: TypeConnectTrafficDistributionGroup, op: "connect:DescribeTrafficDistributionGroup", label: "connect traffic distribution groups",
		describe: func(ctx context.Context, it connectListed[cttypes.TrafficDistributionGroupSummary], r *store.Resource) error {
			out, err := client.DescribeTrafficDistributionGroup(ctx, &connect.DescribeTrafficDistributionGroupInput{TrafficDistributionGroupId: &it.id})
			if err != nil {
				return err
			}
			tdg := out.TrafficDistributionGroup
			if tdg == nil {
				return errConnectNoBody
			}
			name, status := sv(tdg.Name), string(tdg.Status)
			r.Name, r.Status = &name, &status
			r.TagsJSON = mapTagsJSON(tdg.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.TrafficDistributionGroupSummary) {
			name, status := sv(s.Name), string(s.Status)
			r.Name, r.Status = &name, &status
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

// scanConnectPhoneNumbers lists account-scoped phone numbers via V2 then
// describes each. ListPhoneNumbersV2 lists across all instances + TDGs in the
// region without an InstanceId filter.
func scanConnectPhoneNumbers(ctx context.Context, client connectCoreAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := &connectListing[cttypes.ListPhoneNumbersSummary]{op: "connect:ListPhoneNumbersV2"}
	if err := connectReadPages(ctx, "", listed,
		connect.NewListPhoneNumbersV2Paginator(client, &connect.ListPhoneNumbersV2Input{},
			func(o *connect.ListPhoneNumbersV2PaginatorOptions) { o.Limit = 1000 }),
		func(o *connect.ListPhoneNumbersV2Output) []cttypes.ListPhoneNumbersSummary {
			return o.ListPhoneNumbersSummaryList
		},
		func(s cttypes.ListPhoneNumbersSummary) (string, string) {
			return sv(s.PhoneNumberId), sv(s.PhoneNumberArn)
		},
	); err != nil {
		listed.failed(st, acct, region, region, err)
	}
	return connectDescribe[cttypes.ListPhoneNumbersSummary]{
		rtype: TypeConnectPhoneNumber, op: "connect:DescribePhoneNumber", label: "connect phone numbers",
		describe: func(ctx context.Context, it connectListed[cttypes.ListPhoneNumbersSummary], r *store.Resource) error {
			// The ARN, not the UUID: a number claimed to a traffic distribution
			// group, described by UUID from the group's alternate region, is
			// ResourceNotFoundException, which would skip it.
			out, err := client.DescribePhoneNumber(ctx, &connect.DescribePhoneNumberInput{PhoneNumberId: &it.arn})
			if err != nil {
				return err
			}
			pn := out.ClaimedPhoneNumberSummary
			if pn == nil {
				return errConnectNoBody
			}
			num := sv(pn.PhoneNumber)
			r.Name = &num
			r.TagsJSON = mapTagsJSON(pn.Tags)
			r.AttributesJSON = mustJSON(out)
			return nil
		},
		summary: func(r *store.Resource, s cttypes.ListPhoneNumbersSummary) {
			num := sv(s.PhoneNumber)
			r.Name = &num
			r.AttributesJSON = mustJSON(s)
		},
	}.store(ctx, st, acct, region, scanID, listed)
}

// scanConnectEmailAddresses searches per instance — SearchEmailAddresses
// requires InstanceId — and stores each search-summary entry as AttributesJSON.
// The search summary has no tags and the scanner does not call
// DescribeEmailAddress or ListTagsForResource, so these rows carry no tags.
func scanConnectEmailAddresses(ctx context.Context, client connectCoreAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	listed := connectListPerInstance(ctx, st, acct, region, "connect:SearchEmailAddresses", instances,
		func(instID string) connectPager[*connect.SearchEmailAddressesOutput] {
			return &connectTokenPager[*connect.SearchEmailAddressesOutput]{
				call: func(ctx context.Context, token *string) (*connect.SearchEmailAddressesOutput, error) {
					return client.SearchEmailAddresses(ctx, &connect.SearchEmailAddressesInput{InstanceId: &instID, NextToken: token, MaxResults: sdkaws.Int32(100)})
				},
				next: func(o *connect.SearchEmailAddressesOutput) *string { return o.NextToken },
			}
		},
		func(o *connect.SearchEmailAddressesOutput) []cttypes.EmailAddressMetadata { return o.EmailAddresses },
		func(e cttypes.EmailAddressMetadata) (string, string) {
			return sv(e.EmailAddressId), sv(e.EmailAddressArn)
		})
	return connectStoreListed(st, acct, region, scanID, TypeConnectEmailAddress, "connect email addresses", listed,
		func(r *store.Resource, it connectListed[cttypes.EmailAddressMetadata]) {
			addr := sv(it.summary.EmailAddress)
			r.Name = &addr
			r.AttributesJSON = mustJSON(it.summary)
		})
}
