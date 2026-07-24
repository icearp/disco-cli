package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/iot"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeIoTThing, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTThingGroup, Service: "iot"})
	registerType(restype.Descriptor{Type: TypeIoTThingType, Service: "iot", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTBillingGroup, Service: "iot", Leaf: true})
	registerType(restype.Descriptor{Type: TypeIoTThingPrincipalAttachment, Service: "iot"})
}

// iotThingsAPI is the narrow surface used by the Things family.
type iotThingsAPI interface {
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

// scanIoTThings runs Things-family phases.
func scanIoTThings(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanIoTThingList(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTThingGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTThingTypes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanIoTBillingGroups(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) {
			return scanIoTThingPrincipalAttachments(ctx, client, acct, region, st, scanID)
		},
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanIoTThingList(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListThingsPaginator(client, &iot.ListThingsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListThings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListThings: %w", perr)
		}
		for _, t := range out.Things {
			if t.ThingName != nil {
				names = append(names, *t.ThingName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeThing(gctx, &iot.DescribeThingInput{ThingName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeThing %s: %w", name, derr)
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
	}, st, "iot things")
}

func scanIoTThingGroups(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListThingGroupsPaginator(client, &iot.ListThingGroupsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListThingGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListThingGroups: %w", perr)
		}
		for _, g := range out.ThingGroups {
			if g.GroupName != nil {
				names = append(names, *g.GroupName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeThingGroup(gctx, &iot.DescribeThingGroupInput{ThingGroupName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeThingGroup %s: %w", name, derr)
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
	}, st, "iot thing groups")
}

func scanIoTThingTypes(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListThingTypesPaginator(client, &iot.ListThingTypesInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListThingTypes", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListThingTypes: %w", perr)
		}
		for _, t := range out.ThingTypes {
			if t.ThingTypeName != nil {
				names = append(names, *t.ThingTypeName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeThingType(gctx, &iot.DescribeThingTypeInput{ThingTypeName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeThingType %s: %w", name, derr)
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
	}, st, "iot thing types")
}

func scanIoTBillingGroups(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListBillingGroupsPaginator(client, &iot.ListBillingGroupsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListBillingGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListBillingGroups: %w", perr)
		}
		for _, g := range out.BillingGroups {
			if g.GroupName != nil {
				names = append(names, *g.GroupName)
			}
		}
	}
	return iotDescribeFanout(ctx, names, fanoutMed, func(gctx context.Context, name string) (*store.Resource, error) {
		out, derr := client.DescribeBillingGroup(gctx, &iot.DescribeBillingGroupInput{BillingGroupName: &name})
		if derr != nil {
			if isAccessDenied(derr) {
				return nil, nil
			}
			return nil, fmt.Errorf("iot:DescribeBillingGroup %s: %w", name, derr)
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
	}, st, "iot billing groups")
}

// scanIoTThingPrincipalAttachments emits one row per (Thing, Principal)
// pair via per-Thing fan-out of ListThingPrincipals. NativeID is synthesized —
// the attachment has no AWS-issued ARN.
func scanIoTThingPrincipalAttachments(ctx context.Context, client iotThingsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := iot.NewListThingsPaginator(client, &iot.ListThingsInput{})
	var thingNames []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "iot:ListThings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("iot:ListThings: %w", perr)
		}
		for _, t := range out.Things {
			if t.ThingName != nil {
				thingNames = append(thingNames, *t.ThingName)
			}
		}
	}
	if len(thingNames) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range thingNames {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			pp := iot.NewListThingPrincipalsPaginator(client, &iot.ListThingPrincipalsInput{ThingName: &name})
			for pp.HasMorePages() {
				out, perr := pp.NextPage(gctx)
				if perr != nil {
					if isAccessDenied(perr) {
						return nil
					}
					return fmt.Errorf("iot:ListThingPrincipals %s: %w", name, perr)
				}
				for _, p := range out.Principals {
					principal := p
					arn := fmt.Sprintf("arn:aws:iot:%s:%s:thing/%s/principal/%s", region, acct.ID, name, principal)
					attrs := map[string]string{"ThingName": name, "Principal": principal}
					mu.Lock()
					batch = append(batch, &store.Resource{
						Provider:       "aws",
						AccountID:      acct.ID,
						AccountName:    &acct.Name,
						Type:           TypeIoTThingPrincipalAttachment,
						NativeID:       arn,
						Name:           &principal,
						Region:         &region,
						AttributesJSON: mustJSON(attrs),
						DiscoveredBy:   scanID,
					})
					mu.Unlock()
				}
			}
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert iot thing-principal-attachments: %w", uerr)
	}
	return len(batch), n, nil
}

// iotDescribeFanout mirrors the connect/sagemaker per-name Describe helper.
func iotDescribeFanout(
	ctx context.Context,
	names []string,
	weight int64,
	build func(context.Context, string) (*store.Resource, error),
	st *store.Store,
	label string,
) (int, int, error) {
	if len(names) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(weight)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, n := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			r, err := build(gctx, n)
			if err != nil {
				return err
			}
			if r == nil {
				return nil
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert %s: %w", label, uerr)
	}
	return len(batch), n, nil
}
