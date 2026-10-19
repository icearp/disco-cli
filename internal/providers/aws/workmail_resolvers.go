package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	workmailtypes "github.com/aws/aws-sdk-go-v2/service/workmail/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveWorkMailAvailabilityLambda,
		EdgeDecl{TypeWorkMailAvailabilityConfiguration, TypeLambdaFunction, store.RelUses},
	)
	registerResolver(
		noopWorkMailHierarchy,
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailUser, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailGroup, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailResource, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailMailDomain, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailAccessControlRule, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailAvailabilityConfiguration, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailImpersonationRole, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailMobileDeviceAccessRule, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailMobileDeviceAccessOverride, store.RelContains},
		EdgeDecl{TypeWorkMailOrganization, TypeWorkMailPersonalAccessToken, store.RelContains},
		EdgeDecl{TypeWorkMailUser, TypeWorkMailMailboxPermission, store.RelContains},
		EdgeDecl{TypeWorkMailResource, TypeWorkMailMailboxPermission, store.RelContains},
	)
	registerResolver(
		resolveWorkMailMailboxPermissionGrantees,
		EdgeDecl{TypeWorkMailMailboxPermission, TypeWorkMailUser, store.RelUses},
		EdgeDecl{TypeWorkMailMailboxPermission, TypeWorkMailGroup, store.RelUses},
	)
}

// noopWorkMailHierarchy only declares the organization→child and
// mailbox→permission contains edges, which scanWorkMailIn records through
// childFanOut.
func noopWorkMailHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveWorkMailAvailabilityLambda links each Lambda-backed availability
// configuration to the function it invokes (LambdaProvider.LambdaArn), when
// that function was scanned. A qualified ARN links to its function.
func resolveWorkMailAvailabilityLambda(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWorkMailAvailabilityConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	functions, err := scannedIDSet(acct, st, TypeLambdaFunction)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LambdaProvider *struct {
				LambdaArn *string `json:"LambdaArn"`
			} `json:"LambdaProvider"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			return fmt.Errorf("decode workmail availability configuration %s: %w", r.NativeID, err)
		}
		if attrs.LambdaProvider == nil || sv(attrs.LambdaProvider.LambdaArn) == "" {
			continue
		}
		fnID := store.ResourceID("aws", acct.ID, lambdaStripQualifier(sv(attrs.LambdaProvider.LambdaArn)))
		if !functions[fnID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, fnID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert workmail availability configuration→lambda: %w", err)
		}
	}
	return nil
}

// errWorkMailChildNativeID reports a stored WorkMail child row whose
// NativeID lacks the {orgARN}/{kind}/{id} shape its scanner writes.
var errWorkMailChildNativeID = errors.New("workmail child NativeID does not have the shape its scanner writes")

// resolveWorkMailMailboxPermissionGrantees links each mailbox permission to
// the user or group it grants, when that grantee is stored in the same
// organization. A permission whose attributes or NativeID do not decode is
// reported and costs no other permission its edge.
func resolveWorkMailMailboxPermissionGrantees(acct *account, st *store.Store) error {
	perms, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeWorkMailMailboxPermission}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(perms) == 0 {
		return nil
	}
	users, err := scannedIDSet(acct, st, TypeWorkMailUser)
	if err != nil {
		return err
	}
	groups, err := scannedIDSet(acct, st, TypeWorkMailGroup)
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range perms {
		var perm workmailtypes.Permission
		if err := json.Unmarshal([]byte(p.AttributesJSON), &perm); err != nil {
			errs = append(errs, fmt.Errorf("decode workmail mailbox permission %s: %w", p.NativeID, err))
			continue
		}
		orgARN, ok := workMailOrgARN(p.NativeID)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s %q", errWorkMailChildNativeID, p.Type, p.NativeID))
			continue
		}
		var (
			kind    string
			scanned map[string]bool
		)
		switch perm.GranteeType {
		case workmailtypes.MemberTypeUser:
			kind, scanned = workMailUserKind, users
		case workmailtypes.MemberTypeGroup:
			kind, scanned = workMailGroupKind, groups
		default:
			continue
		}
		target := store.ResourceID("aws", acct.ID, workMailChildNativeID(orgARN, kind, sv(perm.GranteeId)))
		if !scanned[target] {
			continue
		}
		if err := st.UpsertRelationship(p.ID, target, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert workmail mailbox permission→grantee: %w", err)
		}
	}
	return errors.Join(errs...)
}
