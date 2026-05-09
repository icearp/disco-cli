package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveGlueTableToDatabase,
		EdgeDecl{TypeGlueTable, TypeGlueDatabase, store.RelAttachedTo},
	)
	registerResolver(
		resolveGluePartitionToTable,
		EdgeDecl{TypeGluePartition, TypeGlueTable, store.RelAttachedTo},
	)
	registerResolver(
		resolveGlueTableOptimizerToTable,
		EdgeDecl{TypeGlueTableOptimizer, TypeGlueTable, store.RelAttachedTo},
	)
	registerResolver(
		resolveGlueSchemaVersionToSchema,
		EdgeDecl{TypeGlueSchemaVersion, TypeGlueSchema, store.RelAttachedTo},
	)
	registerResolver(
		resolveGlueDataQualityRulesetTargets,
		EdgeDecl{TypeGlueDataQualityRuleset, TypeGlueDatabase, store.RelUses},
		EdgeDecl{TypeGlueDataQualityRuleset, TypeGlueTable, store.RelUses},
	)
}

// resolveGlueTableToDatabase wires each glue:table to its parent
// glue:database via NativeID `table/{db}/{tbl}`.
func resolveGlueTableToDatabase(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueTable}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dbSet, err := scannedIDSet(acct, st, TypeGlueDatabase)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = ":table/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		tail := r.NativeID[i+len(seg):]
		end := strings.IndexByte(tail, '/')
		if end < 0 {
			continue
		}
		db := tail[:end]
		dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/%s", sv(r.Region), acct.ID, db)
		tgtID := store.ResourceID("aws", acct.ID, TypeGlueDatabase, dbARN)
		if !dbSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue table→db: %w", err)
		}
	}
	return nil
}

// resolveGluePartitionToTable wires each partition to its parent table via
// NativeID `partition/{db}/{tbl}/...`.
func resolveGluePartitionToTable(acct *account, st *store.Store) error {
	return resolveGlueTableSubchild(acct, st, TypeGluePartition, "partition")
}

// resolveGlueTableOptimizerToTable wires each table-optimizer to its parent
// table via NativeID `table-optimizer/{db}/{tbl}/{type}`.
func resolveGlueTableOptimizerToTable(acct *account, st *store.Store) error {
	return resolveGlueTableSubchild(acct, st, TypeGlueTableOptimizer, "table-optimizer")
}

func resolveGlueTableSubchild(acct *account, st *store.Store, sourceType, kind string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tblSet, err := scannedIDSet(acct, st, TypeGlueTable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		seg := ":" + kind + "/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		parts := strings.SplitN(r.NativeID[i+len(seg):], "/", 3)
		if len(parts) < 2 {
			continue
		}
		db, tbl := parts[0], parts[1]
		tblARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/%s/%s", sv(r.Region), acct.ID, db, tbl)
		tgtID := store.ResourceID("aws", acct.ID, TypeGlueTable, tblARN)
		if !tblSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue %s→table: %w", kind, err)
		}
	}
	return nil
}

// resolveGlueSchemaVersionToSchema wires schema-version to its parent schema
// by stripping the trailing `/version/{vid}` from the NativeID.
func resolveGlueSchemaVersionToSchema(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueSchemaVersion}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	schSet, err := scannedIDSet(acct, st, TypeGlueSchema)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = "/version/"
		i := strings.LastIndex(r.NativeID, seg)
		if i < 0 {
			continue
		}
		schARN := r.NativeID[:i]
		tgtID := store.ResourceID("aws", acct.ID, TypeGlueSchema, schARN)
		if !schSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert glue schema-version→schema: %w", err)
		}
	}
	return nil
}

// resolveGlueDataQualityRulesetTargets wires each ruleset to the database +
// table named in `TargetTable`.
func resolveGlueDataQualityRulesetTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeGlueDataQualityRuleset}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	dbSet, err := scannedIDSet(acct, st, TypeGlueDatabase)
	if err != nil {
		return err
	}
	tblSet, err := scannedIDSet(acct, st, TypeGlueTable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TargetTable *struct {
				DatabaseName *string `json:"DatabaseName"`
				TableName    *string `json:"TableName"`
			} `json:"TargetTable"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.TargetTable == nil {
			continue
		}
		region := sv(r.Region)
		db := sv(attrs.TargetTable.DatabaseName)
		if db != "" {
			dbARN := fmt.Sprintf("arn:aws:glue:%s:%s:database/%s", region, acct.ID, db)
			if dbID := store.ResourceID("aws", acct.ID, TypeGlueDatabase, dbARN); dbSet[dbID] {
				if err := st.UpsertRelationship(r.ID, dbID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue dq→db: %w", err)
				}
			}
		}
		if tbl := sv(attrs.TargetTable.TableName); tbl != "" && db != "" {
			tblARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/%s/%s", region, acct.ID, db, tbl)
			if tblID := store.ResourceID("aws", acct.ID, TypeGlueTable, tblARN); tblSet[tblID] {
				if err := st.UpsertRelationship(r.ID, tblID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert glue dq→table: %w", err)
				}
			}
		}
	}
	return nil
}
