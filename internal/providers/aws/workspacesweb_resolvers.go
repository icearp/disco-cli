package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveWSWPortalRefs,
		EdgeDecl{TypeWSWPortal, TypeWSWBrowserSettings, store.RelUses},
		EdgeDecl{TypeWSWPortal, TypeWSWDataProtectionSettings, store.RelUses},
		EdgeDecl{TypeWSWPortal, TypeWSWIPAccessSettings, store.RelUses},
		EdgeDecl{TypeWSWPortal, TypeWSWNetworkSettings, store.RelUses},
		EdgeDecl{TypeWSWPortal, TypeWSWSessionLogger, store.RelUses},
		EdgeDecl{TypeWSWPortal, TypeWSWTrustStore, store.RelUses},
		EdgeDecl{TypeWSWPortal, TypeWSWUserAccessLoggingSettings, store.RelUses},
		EdgeDecl{TypeWSWPortal, TypeWSWUserSettings, store.RelUses},
	)
	registerResolver(
		resolveWSWNetworkSettingsRefs,
		EdgeDecl{TypeWSWNetworkSettings, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeWSWNetworkSettings, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeWSWNetworkSettings, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveWSWUserAccessLoggingKinesis,
		EdgeDecl{TypeWSWUserAccessLoggingSettings, TypeKinesisStream, store.RelRoutesTo},
	)
	registerResolver(
		resolveWSWIdentityProviderPortal,
		EdgeDecl{TypeWSWIdentityProvider, TypeWSWPortal, store.RelAttachedTo},
	)
}

// resolveWSWPortalRefs wires each portal to the eight settings ARNs it
// references on its own attrs. FK-safe per target type.
func resolveWSWPortalRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWSWPortal}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	bsSet, err := scannedIDSet(acct, st, TypeWSWBrowserSettings)
	if err != nil {
		return err
	}
	dpSet, err := scannedIDSet(acct, st, TypeWSWDataProtectionSettings)
	if err != nil {
		return err
	}
	ipSet, err := scannedIDSet(acct, st, TypeWSWIPAccessSettings)
	if err != nil {
		return err
	}
	nsSet, err := scannedIDSet(acct, st, TypeWSWNetworkSettings)
	if err != nil {
		return err
	}
	slSet, err := scannedIDSet(acct, st, TypeWSWSessionLogger)
	if err != nil {
		return err
	}
	tsSet, err := scannedIDSet(acct, st, TypeWSWTrustStore)
	if err != nil {
		return err
	}
	ualSet, err := scannedIDSet(acct, st, TypeWSWUserAccessLoggingSettings)
	if err != nil {
		return err
	}
	usSet, err := scannedIDSet(acct, st, TypeWSWUserSettings)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			BrowserSettingsArn           *string `json:"BrowserSettingsArn"`
			DataProtectionSettingsArn    *string `json:"DataProtectionSettingsArn"`
			IPAccessSettingsArn          *string `json:"IpAccessSettingsArn"`
			NetworkSettingsArn           *string `json:"NetworkSettingsArn"`
			SessionLoggerArn             *string `json:"SessionLoggerArn"`
			TrustStoreArn                *string `json:"TrustStoreArn"`
			UserAccessLoggingSettingsArn *string `json:"UserAccessLoggingSettingsArn"`
			UserSettingsArn              *string `json:"UserSettingsArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		pairs := []struct {
			arn  string
			ttyp string
			set  map[string]bool
		}{
			{sv(attrs.BrowserSettingsArn), TypeWSWBrowserSettings, bsSet},
			{sv(attrs.DataProtectionSettingsArn), TypeWSWDataProtectionSettings, dpSet},
			{sv(attrs.IPAccessSettingsArn), TypeWSWIPAccessSettings, ipSet},
			{sv(attrs.NetworkSettingsArn), TypeWSWNetworkSettings, nsSet},
			{sv(attrs.SessionLoggerArn), TypeWSWSessionLogger, slSet},
			{sv(attrs.TrustStoreArn), TypeWSWTrustStore, tsSet},
			{sv(attrs.UserAccessLoggingSettingsArn), TypeWSWUserAccessLoggingSettings, ualSet},
			{sv(attrs.UserSettingsArn), TypeWSWUserSettings, usSet},
		}
		for _, p := range pairs {
			if p.arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, p.ttyp, p.arn)
			if !p.set[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert wsw portal→%s: %w", p.ttyp, err)
			}
		}
	}
	return nil
}

// resolveWSWNetworkSettingsRefs wires network-settings → VPC + subnets + SGs.
func resolveWSWNetworkSettingsRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWSWNetworkSettings},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			VpcID            *string  `json:"VpcId"`
			SubnetIDs        []string `json:"SubnetIds"`
			SecurityGroupIDs []string `json:"SecurityGroupIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if vpc := sv(attrs.VpcID); vpc != "" {
			vARN := ec2ARN(region, acct.ID, "vpc", vpc)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2VPC, vARN)
			if vpcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert wsw ns→vpc: %w", err)
				}
			}
		}
		for _, sid := range attrs.SubnetIDs {
			sARN := ec2ARN(region, acct.ID, "subnet", sid)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, sARN)
			if subnetSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert wsw ns→subnet: %w", err)
				}
			}
		}
		for _, sg := range attrs.SecurityGroupIDs {
			sgARN := ec2ARN(region, acct.ID, "security-group", sg)
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
			if sgSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert wsw ns→sg: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveWSWUserAccessLoggingKinesis wires user-access-logging-settings →
// Kinesis stream via `KinesisStreamArn`.
func resolveWSWUserAccessLoggingKinesis(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWSWUserAccessLoggingSettings},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ksSet, err := scannedIDSet(acct, st, TypeKinesisStream)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KinesisStreamArn *string `json:"KinesisStreamArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ks := sv(attrs.KinesisStreamArn)
		if ks == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeKinesisStream, ks)
		if !ksSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert wsw ual→kinesis: %w", err)
		}
	}
	return nil
}

// resolveWSWIdentityProviderPortal links each identity-provider to its parent
// portal by parsing the `portal/{portalID}` segment from the IdP NativeID.
// AWS encodes IdP ARNs as
// `arn:aws:workspaces-web:r:a:identityProvider/{portalUUID}/{idpUUID}` —
// rebuild the portal ARN by replacing `identityProvider/{a}/{b}` with
// `portal/{a}`.
func resolveWSWIdentityProviderPortal(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWSWIdentityProvider},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	portalSet, err := scannedIDSet(acct, st, TypeWSWPortal)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = "identityProvider/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		tail := r.NativeID[i+len(seg):]
		end := strings.IndexByte(tail, '/')
		if end < 0 {
			continue
		}
		portalARN := r.NativeID[:i] + "portal/" + tail[:end]
		tgtID := store.ResourceID("aws", acct.ID, TypeWSWPortal, portalARN)
		if !portalSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert wsw idp→portal: %w", err)
		}
	}
	return nil
}

func init() {
	registerResolver(
		resolveWSWSettingsKMS,
		EdgeDecl{TypeWSWBrowserSettings, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeWSWDataProtectionSettings, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeWSWIPAccessSettings, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeWSWSessionLogger, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeWSWUserSettings, TypeKMSKey, store.RelUses},
	)
}

// resolveWSWSettingsKMS wires each WorkSpaces Web settings type to its
// CustomerManagedKey CMEK. TrustStore has no CMK (no KmsKeyArn field on
// the SDK type). All settings types share AssociatedPortalArns but the
// reverse Portal→Settings edge is already wired by resolveWSWPortalRefs.
func resolveWSWSettingsKMS(acct *account, st *store.Store) error {
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, t := range []string{
		TypeWSWBrowserSettings,
		TypeWSWDataProtectionSettings,
		TypeWSWIPAccessSettings,
		TypeWSWSessionLogger,
		TypeWSWUserSettings,
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var attrs struct {
				CustomerManagedKey *string `json:"CustomerManagedKey"`
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
				continue
			}
			ref := sv(attrs.CustomerManagedKey)
			if ref == "" {
				continue
			}
			if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
				if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert wsw-%s→kms: %w", t, err)
				}
			}
		}
	}
	return nil
}
