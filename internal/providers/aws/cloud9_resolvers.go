package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveCloud9EnvOwner,
		EdgeDecl{TypeCloud9EnvironmentEC2, TypeIAMRole, store.RelAttachedTo},
		EdgeDecl{TypeCloud9EnvironmentEC2, TypeIAMUser, store.RelAttachedTo},
	)
	registerResolver(
		resolveCloud9MembershipUsers,
		EdgeDecl{TypeCloud9EnvironmentMembership, TypeIAMUser, store.RelAttachedTo},
	)
	registerResolver(
		noopCloud9Hierarchy,
		EdgeDecl{TypeCloud9EnvironmentEC2, TypeCloud9EnvironmentMembership, store.RelContains},
	)
}

// noopCloud9Hierarchy only carries the EdgeDecl metadata: the scanner records
// the environment → membership contains pairs itself (scanCloud9Memberships).
func noopCloud9Hierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveCloud9MembershipUsers links each membership to its member when the
// member is a scanned IAM user. A UserArn that is an IAM user ARN is that
// user's NativeID. The other member shapes emit no edge: account root has no
// scanned row, a federated user is no IAM resource, and an assumed-role ARN
// drops the role's path, so the role's ARN cannot be rebuilt from it.
func resolveCloud9MembershipUsers(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloud9EnvironmentMembership}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	userSet, err := scannedIDSet(acct, st, TypeIAMUser)
	if err != nil {
		return err
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			UserArn *string `json:"UserArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode cloud9 membership %s: %w", r.NativeID, err))
			continue
		}
		tgt := store.ResourceID("aws", acct.ID, sv(attrs.UserArn))
		if !userSet[tgt] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloud9 membership→user: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveCloud9EnvOwner wires each environment to its owner principal
// (OwnerArn): IAM role, IAM user, or account root (`arn:aws:iam::<acct>:root`,
// skipped — no scanned target). Dispatched by ARN segment substring.
func resolveCloud9EnvOwner(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCloud9EnvironmentEC2}, Limit: util.AllResources,
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
	userSet, err := scannedIDSet(acct, st, TypeIAMUser)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			OwnerArn *string `json:"OwnerArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		oa := sv(attrs.OwnerArn)
		if oa == "" {
			continue
		}
		var tgt string
		switch {
		case strings.Contains(oa, ":role/") || strings.Contains(oa, ":assumed-role/"):
			t := store.ResourceID("aws", acct.ID, oa)
			if !roleSet[t] {
				continue
			}
			tgt = t
		case strings.Contains(oa, ":user/"):
			t := store.ResourceID("aws", acct.ID, oa)
			if !userSet[t] {
				continue
			}
			tgt = t
		default:
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgt, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert cloud9 env→owner: %w", err)
		}
	}
	return nil
}
