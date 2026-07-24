package aws

import (
	"context"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/resourceexplorer2"
)

func init() {
	registerType(restype.Descriptor{Type: TypeResourceExplorer2Index, Service: "resource-explorer-2", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeResourceExplorer2View, Service: "resource-explorer-2", Leaf: true})
	registerType(restype.Descriptor{Type: TypeResourceExplorer2DefaultViewAssociation, Service: "resource-explorer-2", Leaf: true, Managed: true})
	registerType(restype.Descriptor{Type: TypeResourceExplorer2ManagedView, Service: "resource-explorer-2", Upstream: "AWS::resource-explorer-2::managed-view", Leaf: true, Managed: true})
	registerService(serviceEntry{
		name: "aws:resource-explorer-2",
		fn:   scanResourceExplorer2,
	})
}

type resourceExplorer2API interface {
	ListIndexes(context.Context, *resourceexplorer2.ListIndexesInput, ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListIndexesOutput, error)
	ListViews(context.Context, *resourceexplorer2.ListViewsInput, ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListViewsOutput, error)
	GetDefaultView(context.Context, *resourceexplorer2.GetDefaultViewInput, ...func(*resourceexplorer2.Options)) (*resourceexplorer2.GetDefaultViewOutput, error)
	ListManagedViews(context.Context, *resourceexplorer2.ListManagedViewsInput, ...func(*resourceexplorer2.Options)) (*resourceexplorer2.ListManagedViewsOutput, error)
}

// scanResourceExplorer2 discovers ResourceExplorer2 indexes, views, and the
// default-view association (per-region singleton).
func scanResourceExplorer2(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := resourceexplorer2.NewFromConfig(acct.cfg, func(o *resourceexplorer2.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanRE2Indexes(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRE2Views(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRE2DefaultView(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanRE2ManagedViews(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanRE2Indexes(ctx context.Context, client resourceExplorer2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := resourceexplorer2.NewListIndexesPaginator(client, &resourceexplorer2.ListIndexesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "resourceexplorer2:ListIndexes", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("resourceexplorer2:ListIndexes: %w", err)
		}
		for _, idx := range out.Indexes {
			arn := sv(idx.Arn)
			if arn == "" {
				continue
			}
			label := arn
			itype := string(idx.Type)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResourceExplorer2Index, NativeID: arn,
				Name: &label, Region: &region, Status: &itype,
				AttributesJSON: mustJSON(idx), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "resource-explorer-2 indexes")
}

func scanRE2Views(ctx context.Context, client resourceExplorer2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := resourceexplorer2.NewListViewsPaginator(client, &resourceexplorer2.ListViewsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "resourceexplorer2:ListViews", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("resourceexplorer2:ListViews: %w", err)
		}
		for _, viewArn := range out.Views {
			if viewArn == "" {
				continue
			}
			name := re2ViewName(viewArn)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResourceExplorer2View, NativeID: viewArn,
				Name: &name, Region: &region,
				// Default views are named after their region (view/{region}/{uuid}); a
				// customer view carries a chosen name (view/{name}/{uuid}).
				ManagedByProvider: name == region,
				AttributesJSON:    mustJSON(map[string]string{"ViewArn": viewArn}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "resource-explorer-2 views")
}

// re2ViewName extracts a view's name from its ARN suffix view/{name}/{uuid}.
// The trailing segment is the generated UUID; the name is everything between
// "view/" and that final slash. Falls back to the full ARN on an unexpected shape.
func re2ViewName(arn string) string {
	_, suffix, ok := strings.Cut(arn, ":view/")
	if !ok {
		return arn
	}
	if i := strings.LastIndexByte(suffix, '/'); i >= 0 {
		return suffix[:i]
	}
	return suffix
}

// scanRE2DefaultView captures the per-(account, region) default-view
// singleton. Synth ARN: arn:aws:resource-explorer-2:{r}:{a}:default-view-association.
func scanRE2DefaultView(ctx context.Context, client resourceExplorer2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetDefaultView(ctx, &resourceexplorer2.GetDefaultViewInput{})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException", "ValidationException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("resourceexplorer2:GetDefaultView: %w", err)
	}
	if sv(out.ViewArn) == "" {
		return 0, 0, nil
	}
	arn := fmt.Sprintf("arn:aws:resource-explorer-2:%s:%s:default-view-association", region, acct.ID)
	label := "default-view-association"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeResourceExplorer2DefaultViewAssociation, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "resource-explorer-2 default-view-association")
}

// scanRE2ManagedViews lists AWS-managed views (ListManagedViews returns ARNs);
// sets ManagedByProvider since AWS manages these.
func scanRE2ManagedViews(ctx context.Context, client resourceExplorer2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := resourceexplorer2.NewListManagedViewsPaginator(client, &resourceexplorer2.ListManagedViewsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "resourceexplorer2:ListManagedViews", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("resourceexplorer2:ListManagedViews: %w", err)
		}
		for _, viewArn := range out.ManagedViews {
			if viewArn == "" {
				continue
			}
			label := viewArn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeResourceExplorer2ManagedView, NativeID: viewArn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"ManagedViewArn": viewArn}),
				DiscoveredBy:   scanID,
			})
		}
	}
	return upsertBatch(st, batch, "resource-explorer-2 managed-views")
}
