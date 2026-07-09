package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/textract"
)

func init() {
	registerType(restype.Descriptor{Type: TypeTextractAdapter, Service: "textract", Leaf: true})
	registerType(restype.Descriptor{Type: TypeTextractAdapterVersion, Service: "textract"})
	registerService(serviceEntry{
		name: "aws:textract",
		fn:   scanTextract,
	})
}

type textractAPI interface {
	ListAdapters(context.Context, *textract.ListAdaptersInput, ...func(*textract.Options)) (*textract.ListAdaptersOutput, error)
	ListAdapterVersions(context.Context, *textract.ListAdapterVersionsInput, ...func(*textract.Options)) (*textract.ListAdapterVersionsOutput, error)
}

func textractAdapterNativeID(region, acct, adapterID string) string {
	return fmt.Sprintf("arn:aws:textract:%s:%s:adapter/%s", region, acct, adapterID)
}

func scanTextract(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := textract.NewFromConfig(acct.cfg, func(o *textract.Options) { o.Region = region })

	adapterIDs, t, i, ferr := scanTextractAdapters(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, aid := range adapterIDs {
		t, i, perr := scanTextractAdapterVersions(ctx, client, acct, region, st, scanID, aid)
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanTextractAdapters(ctx context.Context, client textractAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := textract.NewListAdaptersPaginator(client, &textract.ListAdaptersInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "textract:ListAdapters", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("textract:ListAdapters: %w", perr)
		}
		for _, a := range out.Adapters {
			aid := sv(a.AdapterId)
			if aid == "" {
				continue
			}
			ids = append(ids, aid)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTextractAdapter, NativeID: textractAdapterNativeID(region, acct.ID, aid),
				Name: a.AdapterName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "textract adapters")
	return ids, t, i, err
}

func scanTextractAdapterVersions(ctx context.Context, client textractAPI, acct *account, region string, st *store.Store, scanID, adapterID string) (int, int, error) {
	aid := adapterID
	pager := textract.NewListAdapterVersionsPaginator(client, &textract.ListAdapterVersionsInput{AdapterId: &aid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "textract:ListAdapterVersions", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("textract:ListAdapterVersions: %w", perr)
		}
		for _, v := range out.AdapterVersions {
			ver := sv(v.AdapterVersion)
			if ver == "" {
				continue
			}
			status := string(v.Status)
			nid := textractAdapterNativeID(region, acct.ID, sv(v.AdapterId)) + "/adapter-version/" + ver
			label := ver
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTextractAdapterVersion, NativeID: nid,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "textract adapter-versions")
}
