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
		resolveCodeCommitRepoKMS,
		EdgeDecl{TypeCodeCommitRepository, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveCodeCommitTemplateRepos,
		EdgeDecl{TypeCodeCommitApprovalRuleTemplate, TypeCodeCommitRepository, store.RelAttachedTo},
	)
	registerResolver(
		resolveCodeCommitTriggerDestinations,
		EdgeDecl{TypeCodeCommitRepositoryTrigger, TypeSNSTopic, store.RelRoutesTo},
		EdgeDecl{TypeCodeCommitRepositoryTrigger, TypeLambdaFunction, store.RelRoutesTo},
	)
	registerResolver(
		noopCodeCommitHierarchy,
		EdgeDecl{TypeCodeCommitRepository, TypeCodeCommitRepositoryTrigger, store.RelContains},
	)
}

// noopCodeCommitHierarchy only declares the contains edges the scanner records
// through childFanOut (scanCCRepositoryTriggers), so coverage tooling sees them
// as wired.
func noopCodeCommitHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveCodeCommitTemplateRepos wires each approval rule template to the
// scanned repositories it is associated with (AssociatedRepositoryNames, listed
// per template at scan time). Templates and repositories are regional, so a
// name resolves in the template's own region.
func resolveCodeCommitTemplateRepos(acct *account, st *store.Store) error {
	rows, err := listAccountRows(acct, st, TypeCodeCommitApprovalRuleTemplate)
	if err != nil || len(rows) == 0 {
		return err
	}
	repoIDs, err := scannedIDSet(acct, st, TypeCodeCommitRepository)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		var attrs struct {
			AssociatedRepositoryNames []string
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			errs = append(errs, fmt.Errorf("codecommit template %s attributes: %w", r.NativeID, err))
			continue
		}
		for _, name := range attrs.AssociatedRepositoryNames {
			repoID := store.ResourceID("aws", acct.ID, codeCommitRepoARN(sv(r.Region), acct.ID, name))
			if !repoIDs[repoID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, repoID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert codecommit template→repository: %w", err)
			}
		}
	}
	return errors.Join(errs...)
}

// codeCommitTriggerTarget returns the NativeID and type of the scanned
// resource a trigger's DestinationArn names, or ok=false for any other
// destination. A Lambda destination may carry a version or alias qualifier,
// which the function's NativeID does not.
func codeCommitTriggerTarget(arn string) (nativeID, rtype string, ok bool) {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 || parts[0] != "arn" {
		return "", "", false
	}
	switch {
	case parts[2] == "sns" && len(parts) == 6:
		return arn, TypeSNSTopic, true
	case parts[2] == "lambda" && len(parts) >= 7 && parts[5] == "function":
		return lambdaStripQualifier(arn), TypeLambdaFunction, true
	}
	return "", "", false
}

// resolveCodeCommitTriggerDestinations wires each repository trigger to the
// scanned SNS topic or Lambda function it delivers repository events to.
func resolveCodeCommitTriggerDestinations(acct *account, st *store.Store) error {
	rows, err := listAccountRows(acct, st, TypeCodeCommitRepositoryTrigger)
	if err != nil || len(rows) == 0 {
		return err
	}
	scanned := map[string]map[string]bool{}
	for _, t := range []string{TypeSNSTopic, TypeLambdaFunction} {
		if scanned[t], err = scannedIDSet(acct, st, t); err != nil {
			return err
		}
	}
	var errs []error
	for _, r := range rows {
		var attrs struct {
			DestinationArn *string
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			errs = append(errs, fmt.Errorf("codecommit trigger %s attributes: %w", r.NativeID, err))
			continue
		}
		nativeID, rtype, ok := codeCommitTriggerTarget(sv(attrs.DestinationArn))
		if !ok {
			continue
		}
		targetID := store.ResourceID("aws", acct.ID, nativeID)
		if !scanned[rtype][targetID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, targetID, store.RelRoutesTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert codecommit trigger→%s: %w", rtype, err)
		}
	}
	return errors.Join(errs...)
}

// resolveCodeCommitRepoKMS wires each repository to its CMEK
// (RepositoryMetadata.KmsKeyId; defaults to AWS-managed key when empty).
func resolveCodeCommitRepoKMS(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeCodeCommitRepository}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := loadKMSResolveIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			KmsKeyID *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		ref := sv(attrs.KmsKeyID)
		if ref == "" {
			continue
		}
		if keyID, ok := idx.resolveKMSKeyID(ref, sv(r.Region), acct.ID); ok {
			if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert codecommit→kms: %w", err)
			}
		}
	}
	return nil
}
