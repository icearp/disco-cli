package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveTransferAgreementRefs,
		EdgeDecl{TypeTransferAgreement, TypeTransferServer, store.RelAttachedTo},
		EdgeDecl{TypeTransferAgreement, TypeTransferProfile, store.RelUses},
	)
	registerResolver(
		resolveTransferUserParent,
		EdgeDecl{TypeTransferUser, TypeTransferServer, store.RelAttachedTo},
	)
	registerResolver(
		resolveTransferUserRole,
		EdgeDecl{TypeTransferUser, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveTransferServerLoggingRole,
		EdgeDecl{TypeTransferServer, TypeIAMRole, store.RelAssumes},
	)
}

// transferServerIDIndex maps ServerID → resource ID. Server List items carry
// ServerID, so we read it from attrs.
func transferServerIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeTransferServer},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			ServerID *string `json:"ServerId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.ServerID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// transferProfileIDIndex maps ProfileID → resource ID.
func transferProfileIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeTransferProfile},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			ProfileID *string `json:"ProfileId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.ProfileID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// resolveTransferAgreementRefs wires each agreement → server (ServerID) +
// local + partner profile (ProfileID via index).
func resolveTransferAgreementRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeTransferAgreement}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	srvIdx, err := transferServerIDIndex(acct, st)
	if err != nil {
		return err
	}
	profIdx, err := transferProfileIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ServerID         *string `json:"ServerId"`
			LocalProfileID   *string `json:"LocalProfileId"`
			PartnerProfileID *string `json:"PartnerProfileId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if sid := sv(attrs.ServerID); sid != "" {
			if tgtID, ok := srvIdx[sid]; ok {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert transfer agreement→server: %w", err)
				}
			}
		}
		for _, pid := range []string{sv(attrs.LocalProfileID), sv(attrs.PartnerProfileID)} {
			if pid == "" {
				continue
			}
			tgtID, ok := profIdx[pid]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert transfer agreement→profile: %w", err)
			}
		}
	}
	return nil
}

// resolveTransferUserParent wires each user to its parent server. AWS Transfer
// user ARN shape: `arn:aws:transfer:r:a:user/{serverID}/{userName}`.
func resolveTransferUserParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeTransferUser}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	srvIdx, err := transferServerIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		const seg = ":user/"
		i := strings.Index(r.NativeID, seg)
		if i < 0 {
			continue
		}
		tail := r.NativeID[i+len(seg):]
		end := strings.IndexByte(tail, '/')
		if end < 0 {
			continue
		}
		sid := tail[:end]
		tgtID, ok := srvIdx[sid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert transfer user→server: %w", err)
		}
	}
	return nil
}

// resolveTransferUserRole wires user → IAM role via `Role` attr.
func resolveTransferUserRole(acct *account, st *store.Store) error {
	return resolveTransferRoleEdge(acct, st, TypeTransferUser, "Role")
}

// resolveTransferServerLoggingRole wires server → IAM role via `LoggingRole`.
func resolveTransferServerLoggingRole(acct *account, st *store.Store) error {
	return resolveTransferRoleEdge(acct, st, TypeTransferServer, "LoggingRole")
}

func resolveTransferRoleEdge(acct *account, st *store.Store, sourceType, fieldName string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var raw map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &raw); err != nil {
			continue
		}
		v, ok := raw[fieldName].(string)
		if !ok || v == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, v)
		if !roleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert transfer %s→role: %w", sourceType, err)
		}
	}
	return nil
}
