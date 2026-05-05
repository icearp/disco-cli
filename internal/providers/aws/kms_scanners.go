package aws

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"golang.org/x/sync/errgroup"
)

func init() {
	registerService(serviceEntry{
		name: "aws:kms",
		fn:   scanKMS,
		emits: []coverage.TypeDecl{
			{Service: "kms", DiscoType: TypeKMSKey},
			{Service: "kms", DiscoType: TypeKMSAlias},
			// KMS grants have no CloudFormation type — synthetic NativeID,
			// disco-only resource.
			{Service: "kms", DiscoType: TypeKMSGrant, Synthetic: true},
		},
	})
}

// kmsAPI is the narrow set of KMS operations called by scanKMSKeys.
type kmsAPI interface {
	ListKeys(context.Context, *kms.ListKeysInput, ...func(*kms.Options)) (*kms.ListKeysOutput, error)
	DescribeKey(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	GetKeyPolicy(context.Context, *kms.GetKeyPolicyInput, ...func(*kms.Options)) (*kms.GetKeyPolicyOutput, error)
	GetKeyRotationStatus(context.Context, *kms.GetKeyRotationStatusInput, ...func(*kms.Options)) (*kms.GetKeyRotationStatusOutput, error)
	ListAliases(context.Context, *kms.ListAliasesInput, ...func(*kms.Options)) (*kms.ListAliasesOutput, error)
	ListGrants(context.Context, *kms.ListGrantsInput, ...func(*kms.Options)) (*kms.ListGrantsOutput, error)
}

// kmsKeyAttrs is the stored attribute shape for a KMS key — DescribeKey metadata
// plus the key policy and rotation status, which each require their own API call.
// Rule predicates like "rotation disabled on customer key" read these fields.
type kmsKeyAttrs struct {
	Metadata           types.KeyMetadata
	Policy             *string
	KeyRotationEnabled *bool
}

// scanKMS discovers customer-managed KMS keys and aliases in one region. ListKeys
// returns KeyId only; DescribeKey + GetKeyPolicy + GetKeyRotationStatus fan out
// concurrently per page. AWS-managed keys are skipped — their policies are
// boilerplate and they can't be configured by the user.
func scanKMS(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := kms.NewFromConfig(acct.cfg, func(o *kms.Options) { o.Region = region })
	return scanKMSKeys(ctx, client, acct, region, st, scanID)
}

// scanKMSKeys holds the testable scan body.
func scanKMSKeys(ctx context.Context, client kmsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	pager := kms.NewListKeysPaginator(client, &kms.ListKeysInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "kms:ListKeys", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("kms:ListKeys: %w", err)
		}
		t, i, perr := scanKMSKeyPage(ctx, client, acct, region, st, scanID, page.Keys)
		total += t
		inserted += i
		if perr != nil {
			return total, inserted, perr
		}
	}
	t, i, perr := scanKMSAliases(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	return total, inserted, perr
}

// scanKMSKeyPage fans out per-key Describe/Policy/Rotation/Grants over one
// ListKeys page and persists the resulting key + grant resources.
func scanKMSKeyPage(ctx context.Context, client kmsAPI, acct *account, region string, st *store.Store, scanID string, keys []types.KeyListEntry) (total, inserted int, err error) {
	var (
		mu         sync.Mutex
		batch      []*store.Resource
		grantBatch []*store.Resource
		grantPairs [][2]string
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range keys {
		g.Go(func() error {
			r, grants, pairs, kerr := describeKMSKey(gctx, client, acct, region, scanID, k)
			if kerr != nil {
				return kerr
			}
			if r == nil {
				return nil
			}
			mu.Lock()
			batch = append(batch, r)
			grantBatch = append(grantBatch, grants...)
			grantPairs = append(grantPairs, pairs...)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert KMS keys: %w", uerr)
		}
		total += len(batch)
		inserted += n
	}
	if len(grantBatch) > 0 {
		n, uerr := st.UpsertResources(grantBatch)
		if uerr != nil {
			return total, inserted, fmt.Errorf("upsert KMS grants: %w", uerr)
		}
		total += len(grantBatch)
		inserted += n
		if cerr := st.RecordHierarchyBatch(grantPairs); cerr != nil {
			return total, inserted, fmt.Errorf("closure KMS grants: %w", cerr)
		}
	}
	return total, inserted, nil
}

// describeKMSKey fetches one key's metadata + policy + rotation status, then
// lists its grants. Returns nil resource on access-denied / missing metadata so
// the caller can skip the entry without failing siblings.
func describeKMSKey(ctx context.Context, client kmsAPI, acct *account, region, scanID string, k types.KeyListEntry) (*store.Resource, []*store.Resource, [][2]string, error) {
	desc, err := client.DescribeKey(ctx, &kms.DescribeKeyInput{KeyId: k.KeyId})
	if err != nil {
		if isAccessDenied(err) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, fmt.Errorf("kms:DescribeKey %s: %w", sv(k.KeyId), err)
	}
	md := desc.KeyMetadata
	if md == nil {
		return nil, nil, nil, nil
	}

	policy, _ := client.GetKeyPolicy(ctx, &kms.GetKeyPolicyInput{
		KeyId:      md.KeyId,
		PolicyName: sp("default"),
	})
	attrs := kmsKeyAttrs{Metadata: *md}
	if policy != nil {
		attrs.Policy = policy.Policy
	}

	rot, rerr := client.GetKeyRotationStatus(ctx, &kms.GetKeyRotationStatusInput{KeyId: md.KeyId})
	if rerr == nil {
		b := rot.KeyRotationEnabled
		attrs.KeyRotationEnabled = &b
	} else if !isAPIErrorCode(rerr, "UnsupportedOperationException") && !isAccessDenied(rerr) {
		return nil, nil, nil, fmt.Errorf("kms:GetKeyRotationStatus %s: %w", sv(md.KeyId), rerr)
	}

	enabled := "Disabled"
	if md.Enabled {
		enabled = "Enabled"
	}
	keyARN := sv(md.Arn)
	r := &store.Resource{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeKMSKey,
		NativeID:       keyARN,
		Name:           md.KeyId,
		Region:         &region,
		CreatedAt:      tp(md.CreationDate),
		Status:         &enabled,
		AttributesJSON: mustJSON(attrs),
		DiscoveredBy:   scanID,
		// KeyManager "AWS" marks AWS-managed default keys (aws/<service>
		// aliases) present in every account.
		ManagedByProvider: md.KeyManager == types.KeyManagerTypeAws,
	}
	keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, keyARN)
	grants, pairs, gerr := listKMSGrants(ctx, client, acct, region, scanID, sv(md.KeyId), keyARN, keyID)
	if gerr != nil {
		return nil, nil, nil, gerr
	}
	return r, grants, pairs, nil
}

// listKMSGrants pages ListGrants for one key. AccessDenied per-key tolerated so
// one locked-down key doesn't fail the scan.
func listKMSGrants(ctx context.Context, client kmsAPI, acct *account, region, scanID, keyID, keyARN, keyResourceID string) ([]*store.Resource, [][2]string, error) {
	gpager := kms.NewListGrantsPaginator(client, &kms.ListGrantsInput{KeyId: &keyID})
	var grants []*store.Resource
	var pairs [][2]string
	for gpager.HasMorePages() {
		gpage, gerr := gpager.NextPage(ctx)
		if gerr != nil {
			if isAccessDenied(gerr) {
				break
			}
			return nil, nil, fmt.Errorf("kms:ListGrants %s: %w", keyID, gerr)
		}
		for _, ge := range gpage.Grants {
			// KMS grants have no AWS-issued ARN. Synthesize a stable NativeID
			// under the key's ARN so ResourceID + hierarchy are deterministic
			// across rescans.
			gid := sv(ge.GrantId)
			if gid == "" {
				continue
			}
			arn := keyARN + "/grant/" + gid
			name := ge.Name
			if name == nil || *name == "" {
				name = ge.GrantId
			}
			gr := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeKMSGrant,
				NativeID:       arn,
				Name:           name,
				Region:         &region,
				CreatedAt:      tp(ge.CreationDate),
				AttributesJSON: mustJSON(ge),
				DiscoveredBy:   scanID,
			}
			grantID := store.ResourceID("aws", acct.ID, TypeKMSGrant, arn)
			grants = append(grants, gr)
			pairs = append(pairs, [2]string{grantID, keyResourceID})
		}
	}
	return grants, pairs, nil
}

// scanKMSAliases pages ListAliases for the region and upserts aliases.
func scanKMSAliases(ctx context.Context, client kmsAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	aliasPager := kms.NewListAliasesPaginator(client, &kms.ListAliasesInput{})
	for aliasPager.HasMorePages() {
		page, perr := aliasPager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return total, inserted, skipIfAccessDenied(st, "kms:ListAliases", acct.ID, region, perr)
			}
			return total, inserted, fmt.Errorf("kms:ListAliases: %w", perr)
		}
		var batch []*store.Resource
		for _, a := range page.Aliases {
			if a.TargetKeyId == nil {
				continue
			}
			arn := sv(a.AliasArn)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeKMSAlias,
				NativeID:       arn,
				Name:           a.AliasName,
				Region:         &region,
				CreatedAt:      tp(a.CreationDate),
				AttributesJSON: mustJSON(a),
				DiscoveredBy:   scanID,
				// Aliases prefixed "alias/aws/" point at AWS-managed default
				// keys (alias/aws/s3, alias/aws/ebs, ...) present in every
				// account.
				ManagedByProvider: strings.HasPrefix(sv(a.AliasName), "alias/aws/"),
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			n, uerr := st.UpsertResources(batch)
			if uerr != nil {
				return total, inserted, fmt.Errorf("upsert KMS aliases: %w", uerr)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}
