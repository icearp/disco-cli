package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveChimeAppInstanceChildren,
		EdgeDecl{TypeChimeAppInstanceBot, TypeChimeAppInstance, store.RelAttachedTo},
		EdgeDecl{TypeChimeAppInstanceUser, TypeChimeAppInstance, store.RelAttachedTo},
		EdgeDecl{TypeChimeChannelFlow, TypeChimeAppInstance, store.RelAttachedTo},
	)
	registerResolver(
		resolveChimeVoiceProfileDomain,
		EdgeDecl{TypeChimeVoiceProfile, TypeChimeVoiceProfileDomain, store.RelAttachedTo},
		EdgeDecl{TypeChimeVoiceProfileDomain, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveChimeSipMediaApplicationLambda,
		EdgeDecl{TypeChimeSipMediaApplication, TypeLambdaFunction, store.RelUses},
	)
}

// resolveChimeAppInstanceChildren wires bots, users and channel flows to their
// app-instance. Bot/user ARNs embed the app-instance (…/bot/{id}, …/user/{id});
// channel flows carry the owning ARN in the stored wrapper. FK-safe.
func resolveChimeAppInstanceChildren(acct *account, st *store.Store) error {
	aiSet, err := scannedIDSet(acct, st, TypeChimeAppInstance)
	if err != nil {
		return err
	}
	if len(aiSet) == 0 {
		return nil
	}
	edge := func(srcID, aiARN string) error {
		if aiARN == "" {
			return nil
		}
		tgtID := store.ResourceID("aws", acct.ID, aiARN)
		if !aiSet[tgtID] {
			return nil
		}
		if err := st.UpsertRelationship(srcID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert chime child→app-instance: %w", err)
		}
		return nil
	}

	for _, pair := range []struct{ typ, sep string }{
		{TypeChimeAppInstanceBot, "/bot/"},
		{TypeChimeAppInstanceUser, "/user/"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{pair.typ}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if i := strings.LastIndex(r.NativeID, pair.sep); i >= 0 {
				if err := edge(r.ID, r.NativeID[:i]); err != nil {
					return err
				}
			}
		}
	}

	flows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeChannelFlow}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range flows {
		var attrs struct {
			AppInstanceArn *string `json:"appInstanceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := edge(r.ID, sv(attrs.AppInstanceArn)); err != nil {
			return err
		}
	}
	return nil
}

// resolveChimeVoiceProfileDomain wires voice profiles to their domain (by the
// domain id each profile carries) and each domain to its KMS key (from the
// enriched GetVoiceProfileDomain body). FK-safe.
func resolveChimeVoiceProfileDomain(acct *account, st *store.Store) error {
	domains, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeVoiceProfileDomain}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(domains) == 0 {
		return nil
	}
	kmsIdx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	// Index domains by their VoiceProfileDomainId → resource id (shape-agnostic).
	domainByID := make(map[string]string, len(domains))
	for _, d := range domains {
		var a struct {
			VoiceProfileDomainID              *string `json:"VoiceProfileDomainId"`
			ServerSideEncryptionConfiguration *struct {
				KmsKeyArn *string `json:"KmsKeyArn"`
			} `json:"ServerSideEncryptionConfiguration"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		if id := sv(a.VoiceProfileDomainID); id != "" {
			domainByID[id] = d.ID
		}
		if a.ServerSideEncryptionConfiguration != nil {
			if ref := sv(a.ServerSideEncryptionConfiguration.KmsKeyArn); ref != "" {
				if keyID, ok := kmsIdx.resolveKMSKeyID(ref, sv(d.Region), acct.ID); ok {
					if err := st.UpsertRelationship(d.ID, keyID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert chime voice-profile-domain→kms: %w", err)
					}
				}
			}
		}
	}

	profiles, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeVoiceProfile}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, p := range profiles {
		var a struct {
			VoiceProfileDomainID *string `json:"VoiceProfileDomainId"`
		}
		if err := json.Unmarshal([]byte(p.AttributesJSON), &a); err != nil {
			continue
		}
		domID, ok := domainByID[sv(a.VoiceProfileDomainID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(p.ID, domID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert chime voice-profile→domain: %w", err)
		}
	}
	return nil
}

// resolveChimeSipMediaApplicationLambda wires each SIP media application to the
// Lambda function(s) backing its endpoints. FK-safe.
func resolveChimeSipMediaApplicationLambda(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeChimeSipMediaApplication}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	lambdaSet, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var a struct {
			Endpoints []struct {
				LambdaArn *string `json:"LambdaArn"`
			} `json:"Endpoints"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, ep := range a.Endpoints {
			arn := sv(ep.LambdaArn)
			if arn == "" || seen[arn] {
				continue
			}
			seen[arn] = true
			tgtID := store.ResourceID("aws", acct.ID, arn)
			if !lambdaSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert chime sip-media-application→lambda: %w", err)
			}
		}
	}
	return nil
}
