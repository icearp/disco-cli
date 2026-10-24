package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	iottypes "github.com/aws/aws-sdk-go-v2/service/iot/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTThing, Service: "iot", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeIoTThingGroup, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTThingType, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTBillingGroup, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTThingPrincipalAttachment, Service: "iot"})
}

// iotThingsAPI is the narrow surface used by the Things family.
type iotThingsAPI interface {
	iotTagsAPI
	ListThings(context.Context, *iot.ListThingsInput, ...func(*iot.Options)) (*iot.ListThingsOutput, error)
	DescribeThing(context.Context, *iot.DescribeThingInput, ...func(*iot.Options)) (*iot.DescribeThingOutput, error)
	ListThingGroups(context.Context, *iot.ListThingGroupsInput, ...func(*iot.Options)) (*iot.ListThingGroupsOutput, error)
	DescribeThingGroup(context.Context, *iot.DescribeThingGroupInput, ...func(*iot.Options)) (*iot.DescribeThingGroupOutput, error)
	ListThingTypes(context.Context, *iot.ListThingTypesInput, ...func(*iot.Options)) (*iot.ListThingTypesOutput, error)
	DescribeThingType(context.Context, *iot.DescribeThingTypeInput, ...func(*iot.Options)) (*iot.DescribeThingTypeOutput, error)
	ListBillingGroups(context.Context, *iot.ListBillingGroupsInput, ...func(*iot.Options)) (*iot.ListBillingGroupsOutput, error)
	DescribeBillingGroup(context.Context, *iot.DescribeBillingGroupInput, ...func(*iot.Options)) (*iot.DescribeBillingGroupOutput, error)
	ListThingPrincipals(context.Context, *iot.ListThingPrincipalsInput, ...func(*iot.Options)) (*iot.ListThingPrincipalsOutput, error)
}

// scanIoTThings runs Things-family phases, adding each to p, and reports
// whether a write failed.
func scanIoTThings(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	// Listed once for the thing phase and the principal phase.
	things := iotList(ctx, "iot:ListThings", "iot things", "ThingName",
		func(ctx context.Context, token *string) ([]iottypes.ThingAttribute, *string, error) {
			out, err := client.ListThings(ctx, &iot.ListThingsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.Things, out.NextToken, nil
		},
		func(x iottypes.ThingAttribute) *string { return x.ThingName },
		func(x iottypes.ThingAttribute) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTThing, x.ThingArn, x.ThingName, x)
		})
	return runIoTPhases(p,
		func(p *phaseTotals) bool { return scanIoTThingList(ctx, client, acct, region, st, scanID, p, things) },
		func(p *phaseTotals) bool { return scanIoTThingGroups(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTThingTypes(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool { return scanIoTBillingGroups(ctx, client, acct, region, st, scanID, p, tags) },
		func(p *phaseTotals) bool {
			return scanIoTThingPrincipalAttachments(ctx, client, acct, region, st, scanID, p, things.keys)
		},
	)
}

// scanIoTThingList describes and stores the things list holds.
func scanIoTThingList(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, list *iotListing) (storeWriteFailed bool) {
	d := list.describe(ctx, st, acct, region, "iot:DescribeThing", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeThing(ctx, &iot.DescribeThingInput{ThingName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.ThingArn)
			if arn == "" {
				return nil, nil
			}
			tname := sv(out.ThingName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTThing,
				NativeID:       arn,
				Name:           &tname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStore(st, acct, region, p, list, d)
}

func scanIoTThingGroups(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListThingGroups", "iot thing groups", "GroupName",
		func(ctx context.Context, token *string) ([]iottypes.GroupNameAndArn, *string, error) {
			out, err := client.ListThingGroups(ctx, &iot.ListThingGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ThingGroups, out.NextToken, nil
		},
		func(x iottypes.GroupNameAndArn) *string { return x.GroupName },
		func(x iottypes.GroupNameAndArn) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTThingGroup, x.GroupArn, x.GroupName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeThingGroup", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeThingGroup(ctx, &iot.DescribeThingGroupInput{ThingGroupName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.ThingGroupArn)
			if arn == "" {
				return nil, nil
			}
			gname := sv(out.ThingGroupName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTThingGroup,
				NativeID:       arn,
				Name:           &gname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTThingTypes(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListThingTypes", "iot thing types", "ThingTypeName",
		func(ctx context.Context, token *string) ([]iottypes.ThingTypeDefinition, *string, error) {
			out, err := client.ListThingTypes(ctx, &iot.ListThingTypesInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.ThingTypes, out.NextToken, nil
		},
		func(x iottypes.ThingTypeDefinition) *string { return x.ThingTypeName },
		func(x iottypes.ThingTypeDefinition) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTThingType, x.ThingTypeArn, x.ThingTypeName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeThingType", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeThingType(ctx, &iot.DescribeThingTypeInput{ThingTypeName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.ThingTypeArn)
			if arn == "" {
				return nil, nil
			}
			tname := sv(out.ThingTypeName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTThingType,
				NativeID:       arn,
				Name:           &tname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

func scanIoTBillingGroups(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, tags iotTags) (storeWriteFailed bool) {
	list := iotList(ctx, "iot:ListBillingGroups", "iot billing groups", "GroupName",
		func(ctx context.Context, token *string) ([]iottypes.GroupNameAndArn, *string, error) {
			out, err := client.ListBillingGroups(ctx, &iot.ListBillingGroupsInput{NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap)})
			if err != nil {
				return nil, nil, err
			}
			return out.BillingGroups, out.NextToken, nil
		},
		func(x iottypes.GroupNameAndArn) *string { return x.GroupName },
		func(x iottypes.GroupNameAndArn) *store.Resource {
			return iotListedRow(acct, region, scanID, TypeIoTBillingGroup, x.GroupArn, x.GroupName, x)
		})
	d := list.describe(ctx, st, acct, region, "iot:DescribeBillingGroup", isIoTNotFound,
		func(ctx context.Context, name string) (*store.Resource, error) {
			out, derr := client.DescribeBillingGroup(ctx, &iot.DescribeBillingGroupInput{BillingGroupName: &name})
			if derr != nil {
				return nil, derr
			}
			arn := sv(out.BillingGroupArn)
			if arn == "" {
				return nil, nil
			}
			gname := sv(out.BillingGroupName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeIoTBillingGroup,
				NativeID:       arn,
				Name:           &gname,
				Region:         &region,
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		})
	return iotStoreTagged(ctx, st, acct, region, p, list, d, tags.reader())
}

// scanIoTThingPrincipalAttachments stores one row per (thing, principal)
// pair, listing each thing's principals (scanIoTPrincipals). NativeID is
// synthesized since the attachment has no AWS-issued ARN.
func scanIoTThingPrincipalAttachments(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string, p *phaseTotals, things []string) (storeWriteFailed bool) {
	return scanIoTPrincipals(ctx, st, acct, region, scanID, p, iotPrincipalPhase{
		op: "iot:ListThingPrincipals", what: "iot things' principals", kind: "thing", keyAttr: "ThingName",
		typ: TypeIoTThingPrincipalAttachment,
	}, things, func(thing string) func(context.Context, *string) ([]string, *string, error) {
		return func(ctx context.Context, token *string) ([]string, *string, error) {
			out, err := client.ListThingPrincipals(ctx, &iot.ListThingPrincipalsInput{
				ThingName: &thing, NextToken: token, MaxResults: sdkaws.Int32(iotListPageCap),
			})
			if err != nil {
				return nil, nil, err
			}
			return out.Principals, out.NextToken, nil
		}
	})
}
