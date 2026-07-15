package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolvePCACAdConnectorRefs,
		EdgeDecl{TypePCAConnectorADConnector, TypeACMPrivateCA, store.RelUses},
		EdgeDecl{TypePCAConnectorADConnector, TypeDSMicrosoftAD, store.RelAttachedTo},
		EdgeDecl{TypePCAConnectorADConnector, TypeDSSimpleAD, store.RelAttachedTo},
		EdgeDecl{TypePCAConnectorADConnector, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolvePCACAdDirRegRefs,
		EdgeDecl{TypePCAConnectorADDirectoryRegistration, TypeDSMicrosoftAD, store.RelAttachedTo},
		EdgeDecl{TypePCAConnectorADDirectoryRegistration, TypeDSSimpleAD, store.RelAttachedTo},
	)
}

// pcacAdLookupDirectory returns the scanned DS row's resourceID whose NativeID
// ends with `/{directoryId}`; ok=false if no matching row.
func pcacAdLookupDirectory(acct *account, st *store.Store, directoryID string) (string, bool, error) {
	for _, dt := range []string{TypeDSMicrosoftAD, TypeDSSimpleAD} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{dt}, Limit: util.AllResources,
		})
		if err != nil {
			return "", false, err
		}
		for _, r := range rows {
			var a struct {
				DirectoryID *string `json:"DirectoryId"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
				continue
			}
			if sv(a.DirectoryID) == directoryID {
				return r.ID, true, nil
			}
		}
	}
	return "", false, nil
}

// resolvePCACAdConnectorRefs wires connector → ACM private CA, AD directory,
// and security groups (via VpcInformation).
func resolvePCACAdConnectorRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePCAConnectorADConnector}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	caSet, err := scannedIDSet(acct, st, TypeACMPrivateCA)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			CertificateAuthorityArn *string `json:"CertificateAuthorityArn"`
			DirectoryID             *string `json:"DirectoryId"`
			VpcInformation          *struct {
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
			} `json:"VpcInformation"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if c := sv(attrs.CertificateAuthorityArn); c != "" {
			tgtID := store.ResourceID("aws", acct.ID, c)
			if caSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert pca-ad conn→ca: %w", err)
				}
			}
		}
		if d := sv(attrs.DirectoryID); d != "" {
			if dirID, ok, lerr := pcacAdLookupDirectory(acct, st, d); lerr != nil {
				return lerr
			} else if ok {
				if err := st.UpsertRelationship(r.ID, dirID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert pca-ad conn→ds: %w", err)
				}
			}
		}
		if attrs.VpcInformation != nil {
			for _, sg := range attrs.VpcInformation.SecurityGroupIDs {
				if sg == "" {
					continue
				}
				sgARN := ec2ARN(region, acct.ID, "security-group", sg)
				tgtID := store.ResourceID("aws", acct.ID, sgARN)
				if sgSet[tgtID] {
					if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert pca-ad conn→sg: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolvePCACAdDirRegRefs wires directory-registration → AD directory
// (DirectoryID).
func resolvePCACAdDirRegRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypePCAConnectorADDirectoryRegistration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			DirectoryID *string `json:"DirectoryId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if d := sv(attrs.DirectoryID); d != "" {
			if dirID, ok, lerr := pcacAdLookupDirectory(acct, st, d); lerr != nil {
				return lerr
			} else if ok {
				if err := st.UpsertRelationship(r.ID, dirID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert pca-ad dirreg→ds: %w", err)
				}
			}
		}
	}
	return nil
}
