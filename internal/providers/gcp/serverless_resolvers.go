package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveServerlessRelationships,
		EdgeDecl{TypeCloudFunction, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeCloudFunction, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeCloudRunSvc, TypeIAMServiceAccount, store.RelUses},
	)
	registerResolver(resolveCloudRunChildRelationships,
		EdgeDecl{TypeCloudRunRevision, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeCloudRunRevision, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeCloudRunWorkerPool, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeCloudRunWorkerPool, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeCloudRunInstance, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeCloudRunInstance, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeCloudRunDomainMapping, TypeCloudRunSvc, store.RelRoutesTo},
		EdgeDecl{TypeCloudRunExecution, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeCloudRunExecution, TypeKMSCryptoKey, store.RelUses},
	)
}

// resolveServerlessRelationships derives runtime-identity edges for the
// serverless surfaces:
//
//   - cloudFunction -[uses]-> service-account (via serviceConfig.serviceAccountEmail)
//   - cloudFunction -[uses]-> cryptoKey       (via kmsKeyName)
//   - run.service   -[uses]-> service-account (via template.serviceAccount)
//
// VPC-connector edges deferred — separate scanner surface
// (`vpcaccess.googleapis.com`) not yet landed. EventTrigger → Pub/Sub topic /
// Cloud Storage bucket edges deferred — Pub/Sub topic scanner (R4.11) lands
// next iteration; storage edge needs parsing EventTrigger.eventFilters[]. SA
// edges land now, reusing the SA index shape from R4.1.
func resolveServerlessRelationships(p *project, st *store.Store) error {
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}

	// KMS index for function CMEK.
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

	// Cloud Functions.
	fns, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudFunction},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, f := range fns {
		var a struct {
			KmsKeyName    string `json:"kmsKeyName"`
			ServiceConfig struct {
				ServiceAccountEmail string `json:"serviceAccountEmail"`
			} `json:"serviceConfig"`
		}
		if err := json.Unmarshal([]byte(f.AttributesJSON), &a); err != nil {
			continue
		}
		if a.ServiceConfig.ServiceAccountEmail != "" {
			if saID, ok := saByEmail[a.ServiceConfig.ServiceAccountEmail]; ok {
				if err := st.UpsertRelationship(f.ID, saID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert function→SA: %w", err)
				}
			}
		}
		if a.KmsKeyName != "" {
			keyName := stripCryptoKeyVersion(a.KmsKeyName)
			if keyID, ok := keyIDByNative[keyName]; ok {
				if err := st.UpsertRelationship(f.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert function→cryptoKey: %w", err)
				}
			}
		}
	}

	// Cloud Run services.
	runs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudRunSvc},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, s := range runs {
		var a struct {
			Template struct {
				ServiceAccount string `json:"serviceAccount"`
			} `json:"template"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Template.ServiceAccount == "" {
			continue
		}
		saID, ok := saByEmail[a.Template.ServiceAccount]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(s.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert run.service→SA: %w", err)
		}
	}
	return nil
}

// resolveCloudRunChildRelationships derives runtime-identity edges for
// run/v2's per-Service children — Wave R14 of the resolver-implementation
// backlog:
//
//   - run.revision   -[uses]-> service-account (flat `serviceAccount`)
//   - run.revision   -[uses]-> cryptoKey       (flat `encryptionKey`)
//   - run.workerPool -[uses]-> service-account (`template.serviceAccount`)
//   - run.workerPool -[uses]-> cryptoKey       (`template.encryptionKey`)
//   - run.instance   -[uses]-> service-account (flat `serviceAccount`)
//   - run.instance   -[uses]-> cryptoKey       (flat `encryptionKey`)
//   - run.domainMapping -[routes-to]-> run.service (`spec.routeName`, a bare
//     Knative route name — matches the Service's bare name, not its full
//     run/v2 resource name, so resolved via bareNameIndex)
//   - run.execution  -[uses]-> service-account (`template.serviceAccount`,
//     identical shape to run.workerPool — Resolver Wave R25)
//   - run.execution  -[uses]-> cryptoKey       (`template.encryptionKey`)
//
// VpcAccess.Connector (present on Revision/WorkerPool/Instance/Execution)
// deferred —
// same "vpcaccess.googleapis.com not yet landed" reason as the Service-level
// edge above.
func resolveCloudRunChildRelationships(p *project, st *store.Store) error {
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}

	emitIdentityEdges := func(fromID, sa, kmsKey string) error {
		if sa != "" {
			if saID, ok := saByEmail[sa]; ok {
				if err := st.UpsertRelationship(fromID, saID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert run child→SA: %w", err)
				}
			}
		}
		if kmsKey != "" {
			if keyID, ok := keyIDByNative[stripCryptoKeyVersion(kmsKey)]; ok {
				if err := st.UpsertRelationship(fromID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert run child→cryptoKey: %w", err)
				}
			}
		}
		return nil
	}

	// Revision and Instance: flat serviceAccount/encryptionKey fields.
	for _, rtype := range []string{TypeCloudRunRevision, TypeCloudRunInstance} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{rtype},
			Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var a struct {
				ServiceAccount string `json:"serviceAccount"`
				EncryptionKey  string `json:"encryptionKey"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
				continue
			}
			if err := emitIdentityEdges(r.ID, a.ServiceAccount, a.EncryptionKey); err != nil {
				return err
			}
		}
	}

	// WorkerPool and Execution (run/v2 Jobs' per-run child, Resolver Wave
	// R25): both nest an identical serviceAccount/encryptionKey shape under
	// `template`. Execution.Job (the parent Job's resource name) is NOT
	// wired here — already covered by the scanner's own hierarchy-closure
	// parent (jobs_scanners.go's upsertWithParent), so a separate edge would
	// be redundant.
	tplRows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudRunWorkerPool, TypeCloudRunExecution},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, row := range tplRows {
		var a struct {
			Template struct {
				ServiceAccount string `json:"serviceAccount"`
				EncryptionKey  string `json:"encryptionKey"`
			} `json:"template"`
		}
		if err := json.Unmarshal([]byte(row.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitIdentityEdges(row.ID, a.Template.ServiceAccount, a.Template.EncryptionKey); err != nil {
			return err
		}
	}

	// DomainMapping -> Service, by bare Knative route name.
	dms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeCloudRunDomainMapping},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(dms) > 0 {
		svcByName, err := bareNameIndex(p, st, TypeCloudRunSvc)
		if err != nil {
			return err
		}
		for _, dm := range dms {
			var a struct {
				Spec struct {
					RouteName string `json:"routeName"`
				} `json:"spec"`
			}
			if err := json.Unmarshal([]byte(dm.AttributesJSON), &a); err != nil {
				continue
			}
			if a.Spec.RouteName == "" {
				continue
			}
			if svcID, ok := svcByName[a.Spec.RouteName]; ok {
				if err := st.UpsertRelationship(dm.ID, svcID, store.RelRoutesTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert domainMapping→service: %w", err)
				}
			}
		}
	}
	return nil
}
