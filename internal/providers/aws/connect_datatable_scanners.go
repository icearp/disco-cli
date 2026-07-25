package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConnectDataTable, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectDataTableAttribute, Service: "connect"})
	registerType(restype.Descriptor{Type: TypeConnectDataTableRecord, Service: "connect"})
}

// connectDataTableAPI is the narrow surface for the DataTable family. SDK
// has no DescribeDataTable — the list summary serves as parent attrs.
// Attributes and Records are per-table sub-resources, also list-only.
type connectDataTableAPI interface {
	ListDataTables(context.Context, *connect.ListDataTablesInput, ...func(*connect.Options)) (*connect.ListDataTablesOutput, error)
	ListDataTableAttributes(context.Context, *connect.ListDataTableAttributesInput, ...func(*connect.Options)) (*connect.ListDataTableAttributesOutput, error)
	ListDataTablePrimaryValues(context.Context, *connect.ListDataTablePrimaryValuesInput, ...func(*connect.Options)) (*connect.ListDataTablePrimaryValuesOutput, error)
}

// scanConnectDataTable runs DataTable phases per instance.
func scanConnectDataTable(ctx context.Context, client connectDataTableAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	if len(instances) == 0 {
		return 0, 0, nil
	}
	tables, lerr := listDataTablesByInstance(ctx, client, instances, acct, region, st)
	if lerr != nil {
		return 0, 0, lerr
	}

	tableBatch := make([]*store.Resource, 0, len(tables))
	for _, t := range tables {
		arn := t.arn
		name := t.name
		tableBatch = append(tableBatch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeConnectDataTable,
			NativeID:       arn,
			Name:           &name,
			Region:         &region,
			AttributesJSON: mustJSON(t.summary),
			DiscoveredBy:   scanID,
		})
	}
	tt, ti, terr := upsertConnectBatch(st, tableBatch, "connect data tables")
	if terr != nil {
		return 0, 0, terr
	}
	total += tt
	inserted += ti

	at, ai, aerr := scanConnectDataTableAttributes(ctx, client, tables, acct, region, st, scanID)
	if aerr != nil {
		return total, inserted, aerr
	}
	total += at
	inserted += ai

	rt, ri, rerr := scanConnectDataTableRecords(ctx, client, tables, acct, region, st, scanID)
	if rerr != nil {
		return total, inserted, rerr
	}
	total += rt
	inserted += ri
	return total, inserted, nil
}

type dataTableInfo struct {
	instanceID, id, arn, name string
	summary                   cttypes.DataTableSummary
}

func listDataTablesByInstance(ctx context.Context, client connectDataTableAPI, instances []cttypes.InstanceSummary, acct *account, region string, st *store.Store) ([]dataTableInfo, error) {
	var tables []dataTableInfo
	for _, inst := range instances {
		if inst.Id == nil {
			continue
		}
		instID := *inst.Id
		var token *string
		for {
			out, perr := client.ListDataTables(ctx, &connect.ListDataTablesInput{InstanceId: &instID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					_ = skipIfAccessDenied(st, "connect:ListDataTables", acct.ID, region, perr)
					break
				}
				return nil, fmt.Errorf("connect:ListDataTables %s: %w", instID, perr)
			}
			for _, s := range out.DataTableSummaryList {
				if s.Id == nil || s.Arn == nil {
					continue
				}
				name := ""
				if s.Name != nil {
					name = *s.Name
				}
				tables = append(tables, dataTableInfo{
					instanceID: instID,
					id:         *s.Id,
					arn:        *s.Arn,
					name:       name,
					summary:    s,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return tables, nil
}

func scanConnectDataTableAttributes(ctx context.Context, client connectDataTableAPI, tables []dataTableInfo, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, t := range tables {
		instID := t.instanceID
		tableID := t.id
		var token *string
		for {
			out, perr := client.ListDataTableAttributes(ctx, &connect.ListDataTableAttributesInput{InstanceId: &instID, DataTableId: &tableID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:ListDataTableAttributes %s/%s: %w", instID, tableID, perr)
			}
			for _, a := range out.Attributes {
				if a.Name == nil {
					continue
				}
				name := *a.Name
				arn := fmt.Sprintf("%s/attribute/%s", t.arn, name)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectDataTableAttribute,
					NativeID:       arn,
					Name:           &name,
					Region:         &region,
					AttributesJSON: mustJSON(a),
					DiscoveredBy:   scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertConnectBatch(st, batch, "connect data table attributes")
}

func scanConnectDataTableRecords(ctx context.Context, client connectDataTableAPI, tables []dataTableInfo, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, t := range tables {
		instID := t.instanceID
		tableID := t.id
		var token *string
		for {
			out, perr := client.ListDataTablePrimaryValues(ctx, &connect.ListDataTablePrimaryValuesInput{InstanceId: &instID, DataTableId: &tableID, NextToken: token})
			if perr != nil {
				if isAccessDenied(perr) {
					break
				}
				return 0, 0, fmt.Errorf("connect:ListDataTablePrimaryValues %s/%s: %w", instID, tableID, perr)
			}
			for _, r := range out.PrimaryValuesList {
				if r.RecordId == nil {
					continue
				}
				rid := *r.RecordId
				arn := fmt.Sprintf("%s/record/%s", t.arn, rid)
				batch = append(batch, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeConnectDataTableRecord,
					NativeID:       arn,
					Name:           &rid,
					Region:         &region,
					AttributesJSON: mustJSON(r),
					DiscoveredBy:   scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			token = out.NextToken
		}
	}
	return upsertConnectBatch(st, batch, "connect data table records")
}
