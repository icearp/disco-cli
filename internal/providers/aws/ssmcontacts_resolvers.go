package aws

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveSSMContactsChannelToContact,
		EdgeDecl{TypeSSMContactsContactChannel, TypeSSMContactsContact, store.RelAttachedTo},
		EdgeDecl{TypeSSMContactsContactChannel, TypeSSMContactsPlan, store.RelAttachedTo},
	)
	registerResolver(
		resolveSSMContactsRotationContacts,
		EdgeDecl{TypeSSMContactsRotation, TypeSSMContactsContact, store.RelAttachedTo},
		EdgeDecl{TypeSSMContactsRotation, TypeSSMContactsPlan, store.RelAttachedTo},
	)
}

// resolveSSMContactsChannelToContact wires contact-channel → contact (or plan)
// via ContactArn.
func resolveSSMContactsChannelToContact(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSSMContactsContactChannel}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	contactSet, err := scannedIDSet(acct, st, TypeSSMContactsContact)
	if err != nil {
		return err
	}
	planSet, err := scannedIDSet(acct, st, TypeSSMContactsPlan)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ContactArn *string `json:"ContactArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		c := sv(attrs.ContactArn)
		if c == "" {
			continue
		}
		cID := store.ResourceID("aws", acct.ID, c)
		pID := store.ResourceID("aws", acct.ID, c)
		if contactSet[cID] {
			if err := st.UpsertRelationship(r.ID, cID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sc cc→contact: %w", err)
			}
		} else if planSet[pID] {
			if err := st.UpsertRelationship(r.ID, pID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sc cc→plan: %w", err)
			}
		}
	}
	return nil
}

// resolveSSMContactsRotationContacts wires rotation → contacts[]/plans[] via
// ContactIDs.
func resolveSSMContactsRotationContacts(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSSMContactsRotation}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	contactSet, err := scannedIDSet(acct, st, TypeSSMContactsContact)
	if err != nil {
		return err
	}
	planSet, err := scannedIDSet(acct, st, TypeSSMContactsPlan)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ContactIDs []string `json:"ContactIds"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, c := range attrs.ContactIDs {
			if c == "" {
				continue
			}
			cID := store.ResourceID("aws", acct.ID, c)
			pID := store.ResourceID("aws", acct.ID, c)
			if contactSet[cID] {
				if err := st.UpsertRelationship(r.ID, cID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sc rot→contact: %w", err)
				}
			} else if planSet[pID] {
				if err := st.UpsertRelationship(r.ID, pID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert sc rot→plan: %w", err)
				}
			}
		}
	}
	return nil
}
