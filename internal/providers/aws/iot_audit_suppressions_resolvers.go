package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveIoTAuditSuppressionTargets,
		EdgeDecl{TypeIoTAuditSuppression, TypeIoTCertificate, store.RelAttachedTo},
		EdgeDecl{TypeIoTAuditSuppression, TypeIoTCACertificate, store.RelAttachedTo},
		EdgeDecl{TypeIoTAuditSuppression, TypeIoTPolicy, store.RelAttachedTo},
		EdgeDecl{TypeIoTAuditSuppression, TypeIoTRoleAlias, store.RelAttachedTo},
		EdgeDecl{TypeIoTAuditSuppression, TypeIAMRole, store.RelAttachedTo},
	)
}

// resolveIoTAuditSuppressionTargets links a suppression to the noncompliant
// resource its ResourceIdentifier names, when disco scanned that resource.
// Members with no disco type (account, clientId, issuer certificate) and the
// Cognito identity pool (its NativeID builder is not shared) emit nothing.
func resolveIoTAuditSuppressionTargets(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeIoTAuditSuppression},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned := map[string]bool{}
	for _, typ := range []string{TypeIoTCertificate, TypeIoTCACertificate, TypeIoTPolicy, TypeIoTRoleAlias, TypeIAMRole} {
		set, serr := scannedIDSet(acct, st, typ)
		if serr != nil {
			return serr
		}
		for id := range set {
			scanned[id] = true
		}
	}
	for _, r := range rows {
		var attrs struct {
			ResourceIdentifier *struct {
				CaCertificateID         *string `json:"CaCertificateId"`
				DeviceCertificateArn    *string `json:"DeviceCertificateArn"`
				DeviceCertificateID     *string `json:"DeviceCertificateId"`
				IamRoleArn              *string `json:"IamRoleArn"`
				RoleAliasArn            *string `json:"RoleAliasArn"`
				PolicyVersionIdentifier *struct {
					PolicyName *string `json:"PolicyName"`
				} `json:"PolicyVersionIdentifier"`
			} `json:"ResourceIdentifier"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode iot audit suppression %s: %w", r.NativeID, err)
		}
		ri := attrs.ResourceIdentifier
		if ri == nil {
			continue
		}
		region := sv(r.Region)
		var targets []string
		if id := sv(ri.CaCertificateID); id != "" {
			targets = append(targets, iotARN(region, acct.ID, "cacert", id))
		}
		if id := sv(ri.DeviceCertificateID); id != "" {
			targets = append(targets, iotARN(region, acct.ID, "cert", id))
		}
		if p := ri.PolicyVersionIdentifier; p != nil && sv(p.PolicyName) != "" {
			targets = append(targets, iotARN(region, acct.ID, "policy", sv(p.PolicyName)))
		}
		targets = append(targets, sv(ri.DeviceCertificateArn), sv(ri.IamRoleArn), sv(ri.RoleAliasArn))
		for _, arn := range targets {
			if arn == "" {
				continue
			}
			tid := store.ResourceID("aws", acct.ID, arn)
			if !scanned[tid] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tid, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert iot-audit-suppression→%s: %w", arn, err)
			}
		}
	}
	return nil
}
