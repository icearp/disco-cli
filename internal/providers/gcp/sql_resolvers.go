package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R9 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Cloud SQL. DatabaseInstance's own edges (private VPC
// attachment, CMEK, IAM service account, master-instance replication link),
// BackupRun's CMEK edge, and User's IAM-authentication service-account edge —
// all read straight off already-scanned AttributesJSON.
func init() {
	registerResolver(resolveSQLInstanceRelationships,
		EdgeDecl{TypeSQLInstance, TypeComputeNetwork, store.RelAttachedTo},
		EdgeDecl{TypeSQLInstance, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeSQLInstance, TypeIAMServiceAccount, store.RelUses},
		EdgeDecl{TypeSQLInstance, TypeSQLInstance, store.RelAttachedTo},
	)
	registerResolver(resolveSQLBackupRunRelationships,
		EdgeDecl{TypeSQLBackupRun, TypeKMSCryptoKey, store.RelUses},
	)
	registerResolver(resolveSQLUserRelationships,
		EdgeDecl{TypeSQLUser, TypeIAMServiceAccount, store.RelUses},
	)
}

// sqlInstanceNameIndex maps a Cloud SQL instance's bare name (the segment
// after ".../instances/" in its composite NativeID — see scanCloudSQL) to its
// resource ID, for masterInstanceName references, which name the primary
// instance bare, not via the composite NativeID format.
func sqlInstanceNameIndex(p *project, st *store.Store) (map[string]string, error) {
	return bareNameIndex(p, st, TypeSQLInstance)
}

// networkNameIndex maps a Network's bare name (the last self-link segment)
// to its resource ID. Cloud SQL's ipConfiguration.privateNetwork is a
// relative resource link ("/projects/p/global/networks/default" per the
// sqladmin API's own doc example), not the fully-qualified
// "https://www.googleapis.com/compute/v1/..." self-link format Compute's own
// NativeID uses — the two never match on an exact-string ResourceID lookup,
// so this cross-API reference is resolved by bare name instead, mirroring
// the existing ResourcePolicy/BackendBucket bare-name precedents.
func networkNameIndex(p *project, st *store.Store) (map[string]string, error) {
	return bareNameIndex(p, st, TypeComputeNetwork)
}

func resolveSQLInstanceRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSQLInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	instByName, err := sqlInstanceNameIndex(p, st)
	if err != nil {
		return err
	}
	netByName, err := networkNameIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Settings struct {
				IPConfiguration struct {
					PrivateNetwork string `json:"privateNetwork"`
				} `json:"ipConfiguration"`
			} `json:"settings"`
			DiskEncryptionConfiguration *struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"diskEncryptionConfiguration"`
			ServiceAccountEmailAddress string `json:"serviceAccountEmailAddress"`
			MasterInstanceName         string `json:"masterInstanceName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if netName := attrs.Settings.IPConfiguration.PrivateNetwork; netName != "" {
			if netID, ok := netByName[lastSegment(netName)]; ok {
				if err := st.UpsertRelationship(r.ID, netID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sqlInstance→network: %w", err)
				}
			}
		}
		if attrs.DiskEncryptionConfiguration != nil {
			if keyID, ok := keyIDByNative[stripCryptoKeyVersion(attrs.DiskEncryptionConfiguration.KmsKeyName)]; ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sqlInstance→cryptoKey: %w", err)
				}
			}
		}
		if attrs.ServiceAccountEmailAddress != "" {
			if saID, ok := saByEmail[attrs.ServiceAccountEmailAddress]; ok {
				if err := st.UpsertRelationship(r.ID, saID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sqlInstance→serviceAccount: %w", err)
				}
			}
		}
		if attrs.MasterInstanceName != "" {
			if masterID, ok := instByName[attrs.MasterInstanceName]; ok {
				if err := st.UpsertRelationship(r.ID, masterID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sqlInstance→masterInstance: %w", err)
				}
			}
		}
	}
	return nil
}

func resolveSQLBackupRunRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSQLBackupRun},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	if len(keyIDByNative) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			DiskEncryptionConfiguration *struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"diskEncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.DiskEncryptionConfiguration == nil {
			continue
		}
		keyID, ok := keyIDByNative[stripCryptoKeyVersion(attrs.DiskEncryptionConfiguration.KmsKeyName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert backupRun→cryptoKey: %w", err)
		}
	}
	return nil
}

// resolveSQLUserRelationships wires User -> its IAM ServiceAccount
// (`iamEmail`), populated only for MySQL IAM database authentication users.
func resolveSQLUserRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSQLUser},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}
	if len(saByEmail) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			IamEmail string `json:"iamEmail"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.IamEmail == "" {
			continue
		}
		saID, ok := saByEmail[attrs.IamEmail]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, saID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert sqlUser→serviceAccount: %w", err)
		}
	}
	return nil
}
