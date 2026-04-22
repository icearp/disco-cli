package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
)

func init() { registerResolver(resolveOrganizationsSCPTargets) }

// resolveOrganizationsSCPTargets attaches each SCP to the roots, OUs, and
// accounts it applies to. ListTargetsForPolicy returns native ids
// (r-*/ou-*/12-digit account); translate each back to the stable ResourceID
// the scanner used (keyed by ARN) via an index rebuilt from the store.
func resolveOrganizationsSCPTargets(acct *account, st *store.Store) error {
	scps, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeOrganizationsSCP},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(scps) == 0 {
		return nil
	}

	arnByID, typeByID, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}

	client := organizations.NewFromConfig(acct.cfg)
	ctx := context.Background()
	for _, scp := range scps {
		policyID := extractPolicyID(scp.NativeID)
		if policyID == nil {
			continue
		}
		pager := organizations.NewListTargetsForPolicyPaginator(client, &organizations.ListTargetsForPolicyInput{
			PolicyId: policyID,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if isAccessDenied(err) {
					break
				}
				return fmt.Errorf("organizations:ListTargetsForPolicy %s: %w", scp.NativeID, err)
			}
			for _, tgt := range page.Targets {
				id := sv(tgt.TargetId)
				arn, ok := arnByID[id]
				if !ok {
					continue
				}
				targetResID := store.ResourceID("aws", acct.ID, typeByID[id], arn)
				if err := st.UpsertRelationship(scp.ID, targetResID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert scp→target: %w", err)
				}
			}
		}
	}
	return nil
}

// loadOrgTargetIndex returns two maps keyed by Organizations-native id
// (r-*, ou-*, or 12-digit account): the ARN and the disco type.
func loadOrgTargetIndex(acct *account, st *store.Store) (arnByID, typeByID map[string]string, err error) {
	arnByID = map[string]string{}
	typeByID = map[string]string{}
	for _, t := range []string{TypeOrganizationsOU, TypeOrganizationsAccount} {
		rs, err := st.ListResources(store.ResourceFilter{
			Provider:  "aws",
			AccountID: acct.ID,
			Types:     []string{t},
			Limit:     util.AllResources,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rs {
			var a struct {
				Id *string `json:"Id"`
			}
			if jerr := json.Unmarshal([]byte(r.AttributesJSON), &a); jerr != nil || a.Id == nil {
				continue
			}
			arnByID[*a.Id] = r.NativeID
			typeByID[*a.Id] = r.Type
		}
	}
	return arnByID, typeByID, nil
}

// extractPolicyID pulls the p-xxxx identifier out of an SCP ARN of the form
// arn:aws:organizations::ACCOUNT:policy/ORGID/service_control_policy/p-xxxx.
func extractPolicyID(arn string) *string {
	idx := strings.LastIndex(arn, "/")
	if idx < 0 || idx == len(arn)-1 {
		return nil
	}
	id := arn[idx+1:]
	return &id
}
