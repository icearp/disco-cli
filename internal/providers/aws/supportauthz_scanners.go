package aws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/supportauthz"
	satypes "github.com/aws/aws-sdk-go-v2/service/supportauthz/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSupportAuthZSupportPermit, Service: "supportauthz"})
	registerService(serviceEntry{
		name: "aws:supportauthz",
		fn: func(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
			client := supportauthz.NewFromConfig(acct.cfg, func(o *supportauthz.Options) { o.Region = region })
			return scanSupportPermits(ctx, client, acct, region, st, scanID)
		},
	})
}

// supportAuthZAPI is the narrow surface scanSupportPermits uses.
type supportAuthZAPI interface {
	supportauthz.ListSupportPermitsAPIClient
	GetSupportPermit(context.Context, *supportauthz.GetSupportPermitInput, ...func(*supportauthz.Options)) (*supportauthz.GetSupportPermitOutput, error)
}

// scanSupportPermits lists the region's support permits with no status filter
// (the model documents no default), then reads each with GetSupportPermit:
// only the full permit carries its Description and Tags. A denial mid-listing
// warns and keeps the permits listed so far.
func scanSupportPermits(ctx context.Context, client supportAuthZAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var arns []string
	pager := supportauthz.NewListSupportPermitsPaginator(client, &supportauthz.ListSupportPermitsInput{},
		func(o *supportauthz.ListSupportPermitsPaginatorOptions) {
			o.Limit = 100
			o.StopOnDuplicateToken = true
		})
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "supportauthz:ListSupportPermits", acct.ID, region, err)
				break
			}
			return 0, 0, fmt.Errorf("supportauthz:ListSupportPermits: %w", err)
		}
		for _, p := range out.SupportPermits {
			if arn := sv(p.Arn); arn != "" {
				arns = append(arns, arn)
			}
		}
	}
	rows, err := describeFanOut(ctx, st, acct, region, "supportauthz:GetSupportPermit", arns, isSupportPermitNotFound,
		func(ctx context.Context, arn string) (*store.Resource, error) {
			out, err := client.GetSupportPermit(ctx, &supportauthz.GetSupportPermitInput{SupportPermitIdentifier: &arn})
			if err != nil {
				return nil, err
			}
			return &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSupportAuthZSupportPermit, NativeID: arn, Name: out.Name, Region: &region,
				Status: nonEmptyPtr(string(out.Status)), CreatedAt: tp(out.CreatedAt),
				AttributesJSON: mustJSON(flattenSupportPermit(out)),
				TagsJSON:       mapTagsJSON(out.Tags),
				DiscoveredBy:   scanID,
			}, nil
		})
	if err != nil {
		return 0, 0, err
	}
	return upsertBatch(st, rows, "supportauthz support-permits")
}

// isSupportPermitNotFound matches a permit deleted between its list call and
// GetSupportPermit.
func isSupportPermitNotFound(err error) bool {
	return isAPIErrorCode(err, "ResourceNotFoundException")
}

// supportPermitAttrs is the GetSupportPermit body with its unions keyed by
// member name. Each union member marshals as an unlabelled {"Value": ...}, so
// an AllowAfter and an AllowBefore condition would both store as
// {"Value":"<time>"}, and the unit members AllActions and
// AllResourcesInRegion as a bare {"Value":{}}. A member newer than the pinned
// SDK is kept under UnknownMember with its tag (precedent:
// acmAcmeEndpointAttrs).
type supportPermitAttrs struct {
	*supportauthz.GetSupportPermitOutput
	Permit         *supportPermitPermitAttrs
	SigningKeyInfo supportPermitSigningKeyAttrs
}

type supportPermitPermitAttrs struct {
	Actions    supportPermitActionSetAttrs
	Resources  supportPermitResourceSetAttrs
	Conditions []supportPermitConditionAttrs
}

type supportPermitActionSetAttrs struct {
	AllActions    *satypes.Unit               `json:",omitempty"`
	Actions       []string                    `json:",omitempty"`
	UnknownMember *satypes.UnknownUnionMember `json:",omitempty"`
}

type supportPermitResourceSetAttrs struct {
	AllResourcesInRegion *satypes.Unit               `json:",omitempty"`
	Resources            []string                    `json:",omitempty"`
	UnknownMember        *satypes.UnknownUnionMember `json:",omitempty"`
}

type supportPermitConditionAttrs struct {
	AllowAfter    *time.Time                  `json:",omitempty"`
	AllowBefore   *time.Time                  `json:",omitempty"`
	UnknownMember *satypes.UnknownUnionMember `json:",omitempty"`
}

type supportPermitSigningKeyAttrs struct {
	KmsKey        *string                     `json:",omitempty"`
	UnknownMember *satypes.UnknownUnionMember `json:",omitempty"`
}

func flattenSupportPermit(out *supportauthz.GetSupportPermitOutput) supportPermitAttrs {
	attrs := supportPermitAttrs{GetSupportPermitOutput: out}
	switch k := out.SigningKeyInfo.(type) {
	case *satypes.SigningKeyInfoMemberKmsKey:
		attrs.SigningKeyInfo.KmsKey = &k.Value
	case *satypes.UnknownUnionMember:
		attrs.SigningKeyInfo.UnknownMember = k
	}
	if out.Permit == nil {
		return attrs
	}
	p := &supportPermitPermitAttrs{}
	switch a := out.Permit.Actions.(type) {
	case *satypes.ActionSetMemberAllActions:
		p.Actions.AllActions = &a.Value
	case *satypes.ActionSetMemberActions:
		p.Actions.Actions = a.Value
	case *satypes.UnknownUnionMember:
		p.Actions.UnknownMember = a
	}
	switch r := out.Permit.Resources.(type) {
	case *satypes.ResourceSetMemberAllResourcesInRegion:
		p.Resources.AllResourcesInRegion = &r.Value
	case *satypes.ResourceSetMemberResources:
		p.Resources.Resources = r.Value
	case *satypes.UnknownUnionMember:
		p.Resources.UnknownMember = r
	}
	for _, c := range out.Permit.Conditions {
		var ca supportPermitConditionAttrs
		switch m := c.(type) {
		case *satypes.ConditionMemberAllowAfter:
			ca.AllowAfter = &m.Value
		case *satypes.ConditionMemberAllowBefore:
			ca.AllowBefore = &m.Value
		case *satypes.UnknownUnionMember:
			ca.UnknownMember = m
		}
		p.Conditions = append(p.Conditions, ca)
	}
	attrs.Permit = p
	return attrs
}
