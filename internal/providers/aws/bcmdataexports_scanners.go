package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/bcmdataexports"
)

func init() {
	registerService(serviceEntry{
		name: "aws:bcmdataexports",
		fn:   scanBCMDataExports,
		emits: []coverage.TypeDecl{
			{Service: "bcmdataexports", DiscoType: TypeBCMDataExportsExport},
		},
	})
}

// bcmDataExportsAPI is the narrow surface scanBCMDataExports uses.
// ListExports returns ExportReference summaries; GetExport pulls the
// full Export body for the S3-destination edge.
type bcmDataExportsAPI interface {
	ListExports(context.Context, *bcmdataexports.ListExportsInput, ...func(*bcmdataexports.Options)) (*bcmdataexports.ListExportsOutput, error)
	GetExport(context.Context, *bcmdataexports.GetExportInput, ...func(*bcmdataexports.Options)) (*bcmdataexports.GetExportOutput, error)
}

func scanBCMDataExports(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := bcmdataexports.NewFromConfig(acct.cfg, func(o *bcmdataexports.Options) { o.Region = region })
	return scanBCMDataExportsExports(ctx, client, acct, region, st, scanID)
}

func scanBCMDataExportsExports(ctx context.Context, client bcmDataExportsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var arns []string
	pager := bcmdataexports.NewListExportsPaginator(client, &bcmdataexports.ListExportsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isPayerAccountOnly(perr) {
				return total, inserted, markServiceNotEntitled(perr)
			}
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "bcmdataexports:ListExports", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("bcmdataexports:ListExports: %w", perr)
		}
		for _, e := range out.Exports {
			if a := sv(e.ExportArn); a != "" {
				arns = append(arns, a)
			}
		}
	}
	if len(arns) == 0 {
		return 0, 0, nil
	}
	rows := make([]*store.Resource, 0, len(arns))
	for _, arn := range arns {
		a := arn
		out, derr := client.GetExport(ctx, &bcmdataexports.GetExportInput{ExportArn: &a})
		if derr != nil {
			if isAccessDenied(derr) {
				_ = skipIfAccessDenied(st, "bcmdataexports:GetExport", acct.ID, region, derr)
				continue
			}
			return total, inserted, fmt.Errorf("bcmdataexports:GetExport %s: %w", a, derr)
		}
		if out.Export == nil {
			continue
		}
		exp := out.Export
		name := sv(exp.Name)
		rows = append(rows, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeBCMDataExportsExport,
			NativeID:       a,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(exp),
			DiscoveredBy:   scanID,
		})
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(rows)
	if uerr != nil {
		return total, inserted, fmt.Errorf("upsert bcmdataexports exports: %w", uerr)
	}
	return len(rows), n, nil
}
