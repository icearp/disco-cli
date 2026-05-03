package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveSecretsManagerResourcePolicyToSecret,
		EdgeDecl{TypeSecretsManagerResourcePolicy, TypeSecretsManagerSecret, store.RelAttachedTo},
	)
	registerResolver(resolveSecretsManagerRotationScheduleRefs,
		EdgeDecl{TypeSecretsManagerRotationSchedule, TypeSecretsManagerSecret, store.RelAttachedTo},
		EdgeDecl{TypeSecretsManagerRotationSchedule, TypeLambdaFunction, store.RelUses},
	)
}

// resolveSecretsManagerResourcePolicyToSecret wires resource-policy → secret
// via NativeID `{secretARN}/policy` strip.
func resolveSecretsManagerResourcePolicyToSecret(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSecretsManagerResourcePolicy}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parent := strings.TrimSuffix(r.NativeID, "/policy")
		if parent == r.NativeID {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeSecretsManagerSecret, parent)
		if !secretSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sm rp→secret: %w", err)
		}
	}
	return nil
}

// resolveSecretsManagerRotationScheduleRefs wires rotation-schedule → secret
// (SecretId attr) and rotation-lambda (RotationLambdaARN attr).
func resolveSecretsManagerRotationScheduleRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSecretsManagerRotationSchedule}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	secretSet, err := scannedIDSet(acct, st, TypeSecretsManagerSecret)
	if err != nil {
		return err
	}
	fnSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SecretId          *string `json:"SecretId"`
			RotationLambdaARN *string `json:"RotationLambdaARN"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if s := sv(attrs.SecretId); s != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeSecretsManagerSecret, s)
			if secretSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sm rs→secret: %w", err)
				}
			}
		}
		if l := sv(attrs.RotationLambdaARN); l != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeLambdaFunction, l)
			if fnSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert sm rs→lambda: %w", err)
				}
			}
		}
	}
	return nil
}
