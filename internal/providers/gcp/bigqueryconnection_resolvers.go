package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveBQConnectionRelationships,
		EdgeDecl{TypeBQConnection, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeBQConnection, TypeSQLInstance, store.RelUses},
		EdgeDecl{TypeBQConnection, TypeSpannerDatabase, store.RelUses},
	)
}

// resolveBQConnectionRelationships wires connection -[uses]-> {cryptoKey,
// sqlInstance, spannerDatabase} edges from fields already fetched by the
// scanner's List call:
//   - kmsKeyName → CMEK key, same stripCryptoKeyVersion + exact-match pattern
//     used everywhere else in this package.
//   - cloudSql.instanceId is `project:region:instance` (colon-separated, NOT
//     the resource-name form) — reformatted to SQLInstance's own NativeID
//     shape (`projects/{project}/instances/{instance}`) before matching.
//   - cloudSpanner.database is confirmed (per live BigQuery docs — the
//     bigqueryconnection discovery doc's own field description
//     "project/instance/database" is misleadingly terse) to hold the full
//     canonical Spanner database resource name
//     (`projects/{p}/instances/{i}/databases/{d}`), i.e. exactly
//     TypeSpannerDatabase's own NativeID — no reconstruction needed.
//
// cloudResource.serviceAccountId is deliberately NOT wired: it's a
// Google-managed service agent
// (`...@gcp-sa-bigquery-cloudresource.iam.gserviceaccount.com`), never a row
// disco scans.
func resolveBQConnectionRelationships(p *project, st *store.Store) error {
	conns, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBQConnection},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(conns) == 0 {
		return nil
	}

	var keyIDByNative, sqlIDByNative, spannerIDByNative map[string]string

	for _, c := range conns {
		var a struct {
			KmsKeyName string `json:"kmsKeyName"`
			CloudSql   *struct {
				InstanceId string `json:"instanceId"`
			} `json:"cloudSql"`
			CloudSpanner *struct {
				Database string `json:"database"`
			} `json:"cloudSpanner"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &a); err != nil {
			continue
		}

		if key := stripCryptoKeyVersion(a.KmsKeyName); key != "" {
			if keyIDByNative == nil {
				if keyIDByNative, err = nativeIDIndex(p, st, TypeKMSCryptoKey); err != nil {
					return err
				}
			}
			if toID, ok := keyIDByNative[key]; ok {
				if err := st.UpsertRelationship(c.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert connection→cryptoKey: %w", err)
				}
			}
		}

		if a.CloudSql != nil && a.CloudSql.InstanceId != "" {
			if native, ok := sqlInstanceNativeFromColonID(a.CloudSql.InstanceId); ok {
				if sqlIDByNative == nil {
					if sqlIDByNative, err = nativeIDIndex(p, st, TypeSQLInstance); err != nil {
						return err
					}
				}
				if toID, ok := sqlIDByNative[native]; ok {
					if err := st.UpsertRelationship(c.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert connection→sqlInstance: %w", err)
					}
				}
			}
		}

		if a.CloudSpanner != nil && a.CloudSpanner.Database != "" {
			if spannerIDByNative == nil {
				if spannerIDByNative, err = nativeIDIndex(p, st, TypeSpannerDatabase); err != nil {
					return err
				}
			}
			if toID, ok := spannerIDByNative[a.CloudSpanner.Database]; ok {
				if err := st.UpsertRelationship(c.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert connection→spannerDatabase: %w", err)
				}
			}
		}
	}
	return nil
}

// sqlInstanceNativeFromColonID reformats CloudSqlProperties.InstanceId
// (`project:region:instance`) into SQLInstance's own NativeID shape
// (`projects/{project}/instances/{instance}` — see sql_scanners.go). Region
// is discarded; SQLInstance's NativeID doesn't carry it.
func sqlInstanceNativeFromColonID(id string) (string, bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return "", false
	}
	return fmt.Sprintf("projects/%s/instances/%s", parts[0], parts[2]), true
}
