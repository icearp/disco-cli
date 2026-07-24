package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveBigQueryRelationships,
		EdgeDecl{TypeBQDataset, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeBQDataset, TypeBQTable, store.RelUses},
		EdgeDecl{TypeBQDataset, TypeBQRoutine, store.RelUses},
		EdgeDecl{TypeBQDataset, TypeBQDataset, store.RelUses},
	)
	registerResolver(resolveBigQueryRowAccessPolicyRelationships,
		EdgeDecl{TypeBQRowAccessPolicy, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeBQRowAccessPolicy, TypeWorkspaceUser, store.RelUses},
		EdgeDecl{TypeBQRowAccessPolicy, TypeCloudIdentityGroup, store.RelUses},
	)
	registerResolver(resolveBigQueryRoutineConnectionRelationships,
		EdgeDecl{TypeBQRoutine, TypeBQConnection, store.RelUses},
	)
}

// datasetAccessEntry mirrors bigquery.DatasetAccess's `view`/`routine`/
// `dataset` oneof fields (the other member kinds — domain/group/user/special
// group/IAM member — are ordinary IAM-shaped grants, not resource
// references, and aren't resolved here).
type datasetAccessEntry struct {
	View *struct {
		ProjectID string `json:"projectId"`
		DatasetID string `json:"datasetId"`
		TableID   string `json:"tableId"`
	} `json:"view"`
	Routine *struct {
		ProjectID string `json:"projectId"`
		DatasetID string `json:"datasetId"`
		RoutineID string `json:"routineId"`
	} `json:"routine"`
	Dataset *struct {
		Dataset *struct {
			ProjectID string `json:"projectId"`
			DatasetID string `json:"datasetId"`
		} `json:"dataset"`
		TargetTypes []string `json:"targetTypes"`
	} `json:"dataset"`
}

// bqRefIndex builds a map from a resource's own reference-field triple (as
// extracted by refKey) to that resource's ID, scoped to one project. Table's
// NativeID is a genuinely opaque SDK-assigned ID with no documented,
// reconstructable format; Dataset's and Routine's NativeIDs actually ARE
// reconstructable (`{projectId}:{datasetId}` and
// `projects/{p}/datasets/{d}/routines/{r}` respectively — see their own
// scanner comments in `bigquery_scanners.go`), but this resolver matches all
// three grant kinds the same way for one uniform, simpler code path rather
// than special-casing the two that happen to be reconstructable.
func bqRefIndex(st *store.Store, accountID, rtype string, refKey func(attrsJSON string) (string, bool)) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: accountID, Types: []string{rtype}, Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		if key, ok := refKey(r.AttributesJSON); ok {
			idx[key] = r.ID
		}
	}
	return idx, nil
}

func bqTableRefKey(attrsJSON string) (string, bool) {
	var a struct {
		TableReference *struct {
			ProjectID string `json:"projectId"`
			DatasetID string `json:"datasetId"`
			TableID   string `json:"tableId"`
		} `json:"tableReference"`
	}
	if err := json.Unmarshal([]byte(attrsJSON), &a); err != nil || a.TableReference == nil {
		return "", false
	}
	return a.TableReference.ProjectID + "/" + a.TableReference.DatasetID + "/" + a.TableReference.TableID, true
}

func bqDatasetRefKey(attrsJSON string) (string, bool) {
	var a struct {
		DatasetReference *struct {
			ProjectID string `json:"projectId"`
			DatasetID string `json:"datasetId"`
		} `json:"datasetReference"`
	}
	if err := json.Unmarshal([]byte(attrsJSON), &a); err != nil || a.DatasetReference == nil {
		return "", false
	}
	return a.DatasetReference.ProjectID + "/" + a.DatasetReference.DatasetID, true
}

// resolveBigQueryRelationships derives dataset -[uses]-> cryptoKey CMEK
// edges from `defaultEncryptionConfiguration.kmsKeyName` (the dataset CMEK
// applies to every newly-created table within unless overridden — useful
// pivot for "all data in this dataset is CMEK-encrypted with rotation
// interval N"), plus dataset -[uses]-> {table,routine,dataset} authorized-
// access edges from `access[]` — all already fetched by the same
// `Datasets.Get` call, no extra API cost. Scope: same-project only; a
// cross-project authorized-view/routine/dataset reference is silently
// skipped (real GCP feature, but cross-project resource sharing is a
// follow-up, not this wave).
//
// Per-table CMEK edges deferred — table list-shape lacks encryption config;
// would need a Tables.Get fan-out per table, deferred until rule-engine
// demand justifies the expense.
//
// Edge direction note: for the authorized-access edges, the DATA actually
// flows the other way — per the SDK doc, "queries executed against that
// view/routine will have read access to ... this dataset", i.e. the
// referenced view/routine/dataset reads FROM this one, not vice versa. The
// edge is still emitted dataset -[uses]-> {table,routine,dataset} to match
// this file's existing "the resource whose attrs hold the reference is the
// FROM side" convention (same as the CMEK edge above). A rule walking "what
// external resources can read this sensitive dataset" should walk OUTBOUND
// from the dataset, not inbound — mirrors the firewall -[uses]-> instance
// direction note in `internal/providers/gcp/CLAUDE.md`.
func resolveBigQueryRelationships(p *project, st *store.Store) error {
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyIDByNative := make(map[string]string, len(keys))
	for _, k := range keys {
		keyIDByNative[k.NativeID] = k.ID
	}

	dss, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBQDataset},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}

	var tableIDByRef, routineIDByRef, datasetIDByRef map[string]string

	for _, d := range dss {
		var a struct {
			DefaultEncryptionConfiguration struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"defaultEncryptionConfiguration"`
			Access []datasetAccessEntry `json:"access"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		if key := stripCryptoKeyVersion(a.DefaultEncryptionConfiguration.KmsKeyName); key != "" {
			if keyID, ok := keyIDByNative[key]; ok {
				if err := st.UpsertRelationship(d.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert dataset→cryptoKey: %w", err)
				}
			}
		}

		for _, ac := range a.Access {
			switch {
			case ac.View != nil && ac.View.ProjectID == p.ID:
				if tableIDByRef == nil {
					if tableIDByRef, err = bqRefIndex(st, p.ID, TypeBQTable, bqTableRefKey); err != nil {
						return err
					}
				}
				refKey := ac.View.ProjectID + "/" + ac.View.DatasetID + "/" + ac.View.TableID
				if toID, ok := tableIDByRef[refKey]; ok {
					if err := st.UpsertRelationship(d.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dataset→authorized view: %w", err)
					}
				}
			case ac.Routine != nil && ac.Routine.ProjectID == p.ID:
				if routineIDByRef == nil {
					if routineIDByRef, err = bqRefIndex(st, p.ID, TypeBQRoutine, bqRoutineRefKey); err != nil {
						return err
					}
				}
				refKey := ac.Routine.ProjectID + "/" + ac.Routine.DatasetID + "/" + ac.Routine.RoutineID
				if toID, ok := routineIDByRef[refKey]; ok {
					if err := st.UpsertRelationship(d.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert dataset→authorized routine: %w", err)
					}
				}
			case ac.Dataset != nil && ac.Dataset.Dataset != nil && ac.Dataset.Dataset.ProjectID == p.ID:
				if datasetIDByRef == nil {
					if datasetIDByRef, err = bqRefIndex(st, p.ID, TypeBQDataset, bqDatasetRefKey); err != nil {
						return err
					}
				}
				refKey := ac.Dataset.Dataset.ProjectID + "/" + ac.Dataset.Dataset.DatasetID
				toID, ok := datasetIDByRef[refKey]
				if !ok || toID == d.ID {
					continue
				}
				attrs := mustJSON(map[string]any{"targetTypes": ac.Dataset.TargetTypes})
				if err := st.UpsertRelationship(d.ID, toID, store.RelUses, "directed", &attrs); err != nil {
					return fmt.Errorf("upsert dataset→authorized dataset: %w", err)
				}
			}
		}
	}
	return nil
}

func bqRoutineRefKey(attrsJSON string) (string, bool) {
	var a struct {
		RoutineReference *struct {
			ProjectID string `json:"projectId"`
			DatasetID string `json:"datasetId"`
			RoutineID string `json:"routineId"`
		} `json:"routineReference"`
	}
	if err := json.Unmarshal([]byte(attrsJSON), &a); err != nil || a.RoutineReference == nil {
		return "", false
	}
	return a.RoutineReference.ProjectID + "/" + a.RoutineReference.DatasetID + "/" + a.RoutineReference.RoutineID, true
}

// resolveBigQueryRowAccessPolicyRelationships wires the real grantees the
// scanner fetches via a per-policy `RowAccessPolicies.GetIamPolicy` call
// (embedded as `iamPolicy.bindings[].members[]` — `RowAccessPolicy.Grantees`
// itself is doc'd "Input only" and never populated by List, see the scanner
// header comment). Same `serviceAccount:`/`user:`/`group:` member-prefix
// shape as an ordinary IAM policy binding, so this reuses the exact same
// email-keyed indexes `resolveIAMPolicyRelationships` already builds
// (buildSAEmailIndex/buildWorkspaceUserEmailIndex/buildCloudIdentityGroupEmailIndex).
// `domain:`, `allUsers`, `allAuthenticatedUsers` members skip — no resource
// row.
func resolveBigQueryRowAccessPolicyRelationships(p *project, st *store.Store) error {
	policies, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBQRowAccessPolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(policies) == 0 {
		return nil
	}

	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	var userByEmail, groupByEmail map[string]string

	for _, rap := range policies {
		var a struct {
			IamPolicy *struct {
				Bindings []struct {
					Members []string `json:"members"`
				} `json:"bindings"`
			} `json:"iamPolicy"`
		}
		if err := json.Unmarshal([]byte(rap.AttributesJSON), &a); err != nil || a.IamPolicy == nil {
			continue
		}
		for _, b := range a.IamPolicy.Bindings {
			for _, member := range b.Members {
				switch {
				case strings.HasPrefix(member, "serviceAccount:"):
					email := strings.TrimPrefix(member, "serviceAccount:")
					toID, ok := saByEmail[email]
					if !ok {
						continue
					}
					if err := st.UpsertRelationship(rap.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert rowAccessPolicy→serviceAccount: %w", err)
					}
				case strings.HasPrefix(member, "user:"):
					email := strings.ToLower(strings.TrimPrefix(member, "user:"))
					if userByEmail == nil {
						if userByEmail, err = buildWorkspaceUserEmailIndex(st); err != nil {
							return err
						}
					}
					toID, ok := userByEmail[email]
					if !ok {
						continue
					}
					if err := st.UpsertRelationship(rap.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert rowAccessPolicy→workspaceUser: %w", err)
					}
				case strings.HasPrefix(member, "group:"):
					email := strings.ToLower(strings.TrimPrefix(member, "group:"))
					if groupByEmail == nil {
						if groupByEmail, err = buildCloudIdentityGroupEmailIndex(st); err != nil {
							return err
						}
					}
					toID, ok := groupByEmail[email]
					if !ok {
						continue
					}
					if err := st.UpsertRelationship(rap.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert rowAccessPolicy→cloudIdentityGroup: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveBigQueryRoutineConnectionRelationships wires routine -[uses]->
// connection edges from `remoteFunctionOptions.connection` (always
// populated by a bare Routines.List) and `sparkOptions.connection` (only
// populated since Wave R30's ReadMask addition) — both store the
// connection's full resource name verbatim
// (`projects/{p}/locations/{loc}/connections/{id}`), an exact match against
// TypeBQConnection's own NativeID, no reconstruction needed.
func resolveBigQueryRoutineConnectionRelationships(p *project, st *store.Store) error {
	routines, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBQRoutine},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(routines) == 0 {
		return nil
	}

	connIDByNative, err := nativeIDIndex(p, st, TypeBQConnection)
	if err != nil {
		return err
	}
	if len(connIDByNative) == 0 {
		return nil
	}

	for _, rt := range routines {
		var a struct {
			RemoteFunctionOptions *struct {
				Connection string `json:"connection"`
			} `json:"remoteFunctionOptions"`
			SparkOptions *struct {
				Connection string `json:"connection"`
			} `json:"sparkOptions"`
		}
		if err := json.Unmarshal([]byte(rt.AttributesJSON), &a); err != nil {
			continue
		}
		var conn string
		switch {
		case a.RemoteFunctionOptions != nil && a.RemoteFunctionOptions.Connection != "":
			conn = a.RemoteFunctionOptions.Connection
		case a.SparkOptions != nil && a.SparkOptions.Connection != "":
			conn = a.SparkOptions.Connection
		default:
			continue
		}
		toID, ok := connIDByNative[conn]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(rt.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert routine→connection: %w", err)
		}
	}
	return nil
}
