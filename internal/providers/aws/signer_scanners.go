package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/restype"
	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/signer"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSignerSigningProfile, Service: "signer", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSignerProfilePermission, Service: "signer"})
	registerService(serviceEntry{
		name: "aws:signer",
		fn:   scanSigner,
	})
}

type signerAPI interface {
	ListSigningProfiles(context.Context, *signer.ListSigningProfilesInput, ...func(*signer.Options)) (*signer.ListSigningProfilesOutput, error)
	ListProfilePermissions(context.Context, *signer.ListProfilePermissionsInput, ...func(*signer.Options)) (*signer.ListProfilePermissionsOutput, error)
}

// scanSigner discovers Signer signing profiles and per-profile cross-account
// permissions. ProfilePermission has no native ARN; synth as
// {profileArn}/permission/{statementId}.
func scanSigner(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := signer.NewFromConfig(acct.cfg, func(o *signer.Options) { o.Region = region })

	profiles, t, i, ferr := scanSignerSigningProfiles(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanSignerProfilePermissions(ctx, client, profiles, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

type signerProfileRef struct {
	Name string
	ARN  string
}

func scanSignerSigningProfiles(ctx context.Context, client signerAPI, acct *account, region string, st *store.Store, scanID string) ([]signerProfileRef, int, int, error) {
	var batch []*store.Resource
	var refs []signerProfileRef
	var nextToken *string
	for {
		out, err := client.ListSigningProfiles(ctx, &signer.ListSigningProfilesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "signer:ListSigningProfiles", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("signer:ListSigningProfiles: %w", err)
		}
		for _, p := range out.Profiles {
			arn := sv(p.Arn)
			name := sv(p.ProfileName)
			if arn == "" || name == "" {
				continue
			}
			refs = append(refs, signerProfileRef{Name: name, ARN: arn})
			status := string(p.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeSignerSigningProfile, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	t, i, err := upsertBatch(st, batch, "signer signing-profiles")
	return refs, t, i, err
}

func scanSignerProfilePermissions(ctx context.Context, client signerAPI, profiles []signerProfileRef, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	for _, p := range profiles {
		profile := p
		var nextToken *string
		for {
			out, err := client.ListProfilePermissions(ctx, &signer.ListProfilePermissionsInput{
				ProfileName: &profile.Name,
				NextToken:   nextToken,
			})
			if err != nil {
				if isAccessDenied(err) {
					_ = skipIfAccessDenied(st, "signer:ListProfilePermissions", acct.ID, region, err)
					break
				}
				if isAPIErrorCode(err, "ResourceNotFoundException") {
					break
				}
				return 0, 0, fmt.Errorf("signer:ListProfilePermissions p=%s: %w", profile.Name, err)
			}
			for _, perm := range out.Permissions {
				stID := sv(perm.StatementId)
				if stID == "" {
					continue
				}
				arn := profile.ARN + "/permission/" + stID
				label := stID
				batch = append(batch, &store.Resource{
					Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
					Type: TypeSignerProfilePermission, NativeID: arn,
					Name: &label, Region: &region,
					AttributesJSON: mustJSON(perm), DiscoveredBy: scanID,
				})
			}
			if out.NextToken == nil || *out.NextToken == "" {
				break
			}
			nextToken = out.NextToken
		}
	}
	return upsertBatch(st, batch, "signer profile-permissions")
}
