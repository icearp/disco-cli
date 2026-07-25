package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/paymentcryptography"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypePaymentCryptographyKey, Service: "payment-cryptography", Upstream: "AWS::PaymentCryptography::Key", Leaf: true})
	registerType(restype.Descriptor{Type: TypePaymentCryptographyAlias, Service: "payment-cryptography", Upstream: "AWS::PaymentCryptography::Alias"})
	registerService(serviceEntry{
		name: "aws:payment-cryptography",
		fn:   scanPaymentCryptography,
	})
}

type paymentCryptographyAPI interface {
	ListKeys(context.Context, *paymentcryptography.ListKeysInput, ...func(*paymentcryptography.Options)) (*paymentcryptography.ListKeysOutput, error)
	ListAliases(context.Context, *paymentcryptography.ListAliasesInput, ...func(*paymentcryptography.Options)) (*paymentcryptography.ListAliasesOutput, error)
}

// scanPaymentCryptography discovers Payment Cryptography keys and aliases.
// Alias has no native ARN; synth as arn:aws:payment-cryptography:{r}:{a}:alias/{name}.
func scanPaymentCryptography(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := paymentcryptography.NewFromConfig(acct.cfg, func(o *paymentcryptography.Options) { o.Region = region })

	t, i, ferr := scanPCKeys(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanPCAliases(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanPCKeys(ctx context.Context, client paymentCryptographyAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListKeys(ctx, &paymentcryptography.ListKeysInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "payment-cryptography:ListKeys", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("payment-cryptography:ListKeys: %w", err)
		}
		for _, k := range out.Keys {
			arn := sv(k.KeyArn)
			if arn == "" {
				continue
			}
			status := string(k.KeyState)
			label := arn
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePaymentCryptographyKey, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(k), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "payment-cryptography keys")
}

func scanPCAliases(ctx context.Context, client paymentCryptographyAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListAliases(ctx, &paymentcryptography.ListAliasesInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "payment-cryptography:ListAliases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("payment-cryptography:ListAliases: %w", err)
		}
		for _, a := range out.Aliases {
			name := sv(a.AliasName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:payment-cryptography:%s:%s:%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePaymentCryptographyAlias, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "payment-cryptography aliases")
}
