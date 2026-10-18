package aws

import (
	"context"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/accountaccess"
	aatypes "github.com/aws/aws-sdk-go-v2/service/accountaccess/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

const (
	accountAccessGetApplicationOp   = "account-access:GetApplication"
	accountAccessListEntitlementsOp = "account-access:ListEntitlements"
)

func init() {
	registerType(restype.Descriptor{Type: TypeAccountAccessApplication, Service: "account-access"})
	registerType(restype.Descriptor{Type: TypeAccountAccessEntitlement, Service: "account-access"})
	registerService(serviceEntry{
		name: "aws:account-access",
		fn: func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
			client := accountaccess.NewFromConfig(acct.cfg, func(o *accountaccess.Options) { o.Region = region })
			return scanAccountAccess(ctx, client, acct, region, st, scanID)
		},
	})
}

// accountAccessAPI is the narrow surface scanAccountAccess uses.
type accountAccessAPI interface {
	accountaccess.ListApplicationsAPIClient
	accountaccess.ListEntitlementsAPIClient
	GetApplication(context.Context, *accountaccess.GetApplicationInput, ...func(*accountaccess.Options)) (*accountaccess.GetApplicationOutput, error)
}

// scanAccountAccess discovers the region's account access manager
// applications, then the entitlements of each stored application. The
// entitlement phase needs the applications as parents, so a hard error in the
// application phase ends the scan.
func scanAccountAccess(ctx context.Context, client accountAccessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	total, inserted, apps, err := scanAccountAccessApplications(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	t, i, err := scanAccountAccessEntitlements(ctx, client, acct, region, st, scanID, apps)
	return total + t, inserted + i, err
}

// accountAccessApps are the applications stored this scan: parents for the
// entitlement phase, and the ones whose status is not ACTIVE.
type accountAccessApps struct {
	parents   []childParent
	notActive map[string]bool
}

// scanAccountAccessApplications lists the region's applications, then reads
// each with GetApplication: only it carries the status, identity source and
// tags. A denial mid-listing warns and keeps the applications listed so far;
// an application deleted between the list and its Get is skipped.
func scanAccountAccessApplications(ctx context.Context, client accountAccessAPI, acct *account, region string, st *store.Store, scanID string) (int, int, accountAccessApps, error) {
	apps := accountAccessApps{notActive: map[string]bool{}}
	var arns []string
	pager := accountaccess.NewListApplicationsPaginator(client, &accountaccess.ListApplicationsInput{MaxResults: sdkaws.Int32(100)},
		func(o *accountaccess.ListApplicationsPaginatorOptions) { o.StopOnDuplicateToken = true })
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "account-access:ListApplications", acct.ID, region, err)
				break
			}
			return 0, 0, apps, fmt.Errorf("account-access:ListApplications: %w", err)
		}
		for _, a := range out.Applications {
			if arn := sv(a.ApplicationArn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	rows, err := describeFanOut(ctx, st, acct, region, accountAccessGetApplicationOp, arns, isGone,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, err := client.GetApplication(ctx, &accountaccess.GetApplicationInput{ApplicationArn: &arn})
			if err != nil {
				return nil, err
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAccountAccessApplication, NativeID: arn, Region: &region,
				Status: nonEmptyPtr(string(out.Status)), CreatedAt: tp(out.CreatedAt),
				AttributesJSON: mustJSON(flattenAccountAccessApplication(out)),
				TagsJSON:       mapTagsJSON(out.Tags),
				DiscoveredBy:   scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, apps, err
	}
	total, inserted, err := upsertBatch(st, rows, "account-access applications")
	if err != nil {
		return 0, 0, apps, err
	}
	for _, r := range rows {
		apps.parents = append(apps.parents, childParent{id: r.NativeID, arn: r.NativeID})
		if sv(r.Status) != string(aatypes.StatusActive) {
			apps.notActive[r.NativeID] = true
		}
	}
	return total, inserted, apps, nil
}

// scanAccountAccessEntitlements lists the entitlements of each application
// with an empty filter, which the model requires to be present but whose
// criteria are all optional. Per application: one deleted after it was
// listed is skipped silently; one whose call still fails transiently after
// the SDK's retries is skipped with one warning. An application that is not
// ACTIVE (creating, deleting, or failed) may refuse the listing with
// ValidationException (not observed live), so that code skips it with one
// warning; from an ACTIVE application the code is returned, since the only
// inputs are the listed ARN, the empty filter and a fixed page size.
func scanAccountAccessEntitlements(ctx context.Context, client accountAccessAPI, acct *account, region string, st *store.Store, scanID string, apps accountAccessApps) (int, int, error) {
	isGone := func(err error) bool { return isAPIErrorCode(err, "ResourceNotFoundException") }
	var notReady skipTally
	total, inserted, err := childFanOutSkipTransient(ctx, st, acct, region, accountAccessListEntitlementsOp, apps.parents, isGone, byParentARN,
		func(ctx context.Context, p childParent) ([]*store.Resource, error) {
			rows, err := listAccountAccessEntitlements(ctx, client, acct, region, scanID, p)
			if apps.notActive[p.arn] && notReady.dropCode("ValidationException", p.arn, err) {
				return nil, nil
			}
			return rows, err
		})
	notReady.warnCode(st, accountAccessListEntitlementsOp, acct, region, "ValidationException", "application(s)",
		"an application that is not ACTIVE may refuse its entitlement listing")
	return total, inserted, err
}

// listAccountAccessEntitlements returns the entitlements of one application.
// An entitlement has no ARN; its NativeID is the application ARN plus
// "/entitlement/" and the entitlement id.
func listAccountAccessEntitlements(ctx context.Context, client accountAccessAPI, acct *account, region, scanID string, app childParent) ([]*store.Resource, error) {
	var rows []*store.Resource
	pager := accountaccess.NewListEntitlementsPaginator(client, &accountaccess.ListEntitlementsInput{
		ApplicationArn: &app.arn, Filter: &aatypes.EntitlementFilter{}, MaxResults: sdkaws.Int32(100),
	}, func(o *accountaccess.ListEntitlementsPaginatorOptions) { o.StopOnDuplicateToken = true })
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range out.Entitlements {
			id := sv(e.EntitlementId)
			if id == "" {
				continue
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeAccountAccessEntitlement, NativeID: accountAccessEntitlementNativeID(app.arn, id), Region: &region,
				CreatedAt:      tp(e.CreatedAt),
				AttributesJSON: mustJSON(flattenAccountAccessEntitlement(e)),
				DiscoveredBy:   scanID,
			})
		}
	}
	return rows, nil
}

func accountAccessEntitlementNativeID(appARN, entitlementID string) string {
	return appARN + "/entitlement/" + entitlementID
}

// The SDK marshals each union member as an unlabelled {"Value": ...}, so a
// user principal and a group principal would store identically. The attrs
// types below key every union by member name; a member newer than the pinned
// SDK is kept under UnknownMember with its tag (precedent: acmAcmeEndpointAttrs).

// accountAccessApplicationAttrs is the GetApplication body.
type accountAccessApplicationAttrs struct {
	*accountaccess.GetApplicationOutput
	IdentitySource accountAccessIdentitySourceAttrs
}

type accountAccessIdentitySourceAttrs struct {
	IdentityCenter *aatypes.IdentityCenterDetails `json:",omitempty"`
	UnknownMember  *aatypes.UnknownUnionMember    `json:",omitempty"`
}

func flattenAccountAccessApplication(out *accountaccess.GetApplicationOutput) accountAccessApplicationAttrs {
	attrs := accountAccessApplicationAttrs{GetApplicationOutput: out}
	switch s := out.IdentitySource.(type) {
	case *aatypes.IdentitySourceDetailsMemberIdentityCenter:
		attrs.IdentitySource.IdentityCenter = &s.Value
	case *aatypes.UnknownUnionMember:
		attrs.IdentitySource.UnknownMember = s
	}
	return attrs
}

// accountAccessEntitlementAttrs is the ListEntitlements element.
type accountAccessEntitlementAttrs struct {
	aatypes.EntitlementsListMember
	Entitlement accountAccessEntitlementSummaryAttrs
}

type accountAccessEntitlementSummaryAttrs struct {
	PrincipalRole *accountAccessPrincipalRoleAttrs `json:",omitempty"`
	UnknownMember *aatypes.UnknownUnionMember      `json:",omitempty"`
}

type accountAccessPrincipalRoleAttrs struct {
	aatypes.PrincipalRoleEntitlementSummary
	Principal accountAccessPrincipalAttrs
}

type accountAccessPrincipalAttrs struct {
	IdentityCenter *accountAccessIdentityCenterPrincipalAttrs `json:",omitempty"`
	UnknownMember  *aatypes.UnknownUnionMember                `json:",omitempty"`
}

type accountAccessIdentityCenterPrincipalAttrs struct {
	UserID        *string                     `json:"UserId,omitempty"`
	GroupID       *string                     `json:"GroupId,omitempty"`
	UnknownMember *aatypes.UnknownUnionMember `json:",omitempty"`
}

func flattenAccountAccessEntitlement(e aatypes.EntitlementsListMember) accountAccessEntitlementAttrs {
	attrs := accountAccessEntitlementAttrs{EntitlementsListMember: e}
	switch s := e.Entitlement.(type) {
	case *aatypes.EntitlementSummaryMemberPrincipalRole:
		attrs.Entitlement.PrincipalRole = &accountAccessPrincipalRoleAttrs{
			PrincipalRoleEntitlementSummary: s.Value,
			Principal:                       flattenAccountAccessPrincipal(s.Value.Principal),
		}
	case *aatypes.UnknownUnionMember:
		attrs.Entitlement.UnknownMember = s
	}
	return attrs
}

func flattenAccountAccessPrincipal(p aatypes.Principal) accountAccessPrincipalAttrs {
	var attrs accountAccessPrincipalAttrs
	switch m := p.(type) {
	case *aatypes.PrincipalMemberIdentityCenter:
		ic := &accountAccessIdentityCenterPrincipalAttrs{}
		switch v := m.Value.(type) {
		case *aatypes.IdentityCenterPrincipalMemberUserId:
			ic.UserID = &v.Value
		case *aatypes.IdentityCenterPrincipalMemberGroupId:
			ic.GroupID = &v.Value
		case *aatypes.UnknownUnionMember:
			ic.UnknownMember = v
		}
		attrs.IdentityCenter = ic
	case *aatypes.UnknownUnionMember:
		attrs.UnknownMember = m
	}
	return attrs
}
