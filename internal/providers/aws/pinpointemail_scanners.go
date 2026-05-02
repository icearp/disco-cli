package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/pinpointemail"
)

func init() {
	registerService(serviceEntry{
		name: "aws:pinpoint-email",
		fn:   scanPinpointEmailV1,
		emits: []coverage.TypeDecl{
			{Service: "pinpoint-email", DiscoType: TypePinpointEmailConfigurationSet},
			{Service: "pinpoint-email", DiscoType: TypePinpointEmailConfigurationSetEventDestination},
			{Service: "pinpoint-email", DiscoType: TypePinpointEmailDedicatedIpPool},
			{Service: "pinpoint-email", DiscoType: TypePinpointEmailIdentity},
		},
	})
}

type pinpointEmailAPI interface {
	ListConfigurationSets(context.Context, *pinpointemail.ListConfigurationSetsInput, ...func(*pinpointemail.Options)) (*pinpointemail.ListConfigurationSetsOutput, error)
	GetConfigurationSetEventDestinations(context.Context, *pinpointemail.GetConfigurationSetEventDestinationsInput, ...func(*pinpointemail.Options)) (*pinpointemail.GetConfigurationSetEventDestinationsOutput, error)
	ListDedicatedIpPools(context.Context, *pinpointemail.ListDedicatedIpPoolsInput, ...func(*pinpointemail.Options)) (*pinpointemail.ListDedicatedIpPoolsOutput, error)
	ListEmailIdentities(context.Context, *pinpointemail.ListEmailIdentitiesInput, ...func(*pinpointemail.Options)) (*pinpointemail.ListEmailIdentitiesOutput, error)
}

// scanPinpointEmailV1 discovers Pinpoint Email configuration sets, per-set
// event destinations, dedicated IP pools, and email identities. List APIs
// return only names — synthesize ARN per (account, region, kind, name).
func scanPinpointEmailV1(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := pinpointemail.NewFromConfig(acct.cfg, func(o *pinpointemail.Options) { o.Region = region })

	cfgSets, t, i, ferr := scanPESConfigurationSets(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, cs := range cfgSets {
		t, i, ferr = scanPESEventDestinations(ctx, client, acct, region, st, scanID, cs)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}

	t, i, ferr = scanPESDedicatedIpPools(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	t, i, ferr = scanPESIdentities(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i
	return total, inserted, nil
}

func scanPESConfigurationSets(ctx context.Context, client pinpointEmailAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := pinpointemail.NewListConfigurationSetsPaginator(client, &pinpointemail.ListConfigurationSetsInput{})
	var batch []*store.Resource
	var names []string
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "pinpointemail:ListConfigurationSets", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("pinpointemail:ListConfigurationSets: %w", err)
		}
		for _, name := range out.ConfigurationSets {
			if name == "" {
				continue
			}
			names = append(names, name)
			arn := fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s", region, acct.ID, name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePinpointEmailConfigurationSet, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"ConfigurationSetName": name}), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "pinpoint-email configuration-sets")
	return names, t, i, err
}

func scanPESEventDestinations(ctx context.Context, client pinpointEmailAPI, acct *account, region string, st *store.Store, scanID string, configSet string) (int, int, error) {
	cs := configSet
	out, err := client.GetConfigurationSetEventDestinations(ctx, &pinpointemail.GetConfigurationSetEventDestinationsInput{ConfigurationSetName: &cs})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "NotFoundException", "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("pinpointemail:GetConfigurationSetEventDestinations: %w", err)
	}
	var batch []*store.Resource
	for _, ed := range out.EventDestinations {
		name := sv(ed.Name)
		if name == "" {
			continue
		}
		arn := fmt.Sprintf("arn:aws:ses:%s:%s:configuration-set/%s/event-destination/%s", region, acct.ID, cs, name)
		batch = append(batch, &store.Resource{
			Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
			Type: TypePinpointEmailConfigurationSetEventDestination, NativeID: arn,
			Name: &name, Region: &region,
			AttributesJSON: mustJSON(ed), DiscoveredBy: scanID,
		})
	}
	return upsertBatch(st, batch, "pinpoint-email configuration-set-event-destinations")
}

func scanPESDedicatedIpPools(ctx context.Context, client pinpointEmailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointemail.NewListDedicatedIpPoolsPaginator(client, &pinpointemail.ListDedicatedIpPoolsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pinpointemail:ListDedicatedIpPools", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pinpointemail:ListDedicatedIpPools: %w", err)
		}
		for _, name := range out.DedicatedIpPools {
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ses:%s:%s:dedicated-ip-pool/%s", region, acct.ID, name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePinpointEmailDedicatedIpPool, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(map[string]string{"PoolName": name}), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "pinpoint-email dedicated-ip-pools")
}

func scanPESIdentities(ctx context.Context, client pinpointEmailAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := pinpointemail.NewListEmailIdentitiesPaginator(client, &pinpointemail.ListEmailIdentitiesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "pinpointemail:ListEmailIdentities", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("pinpointemail:ListEmailIdentities: %w", err)
		}
		for _, ii := range out.EmailIdentities {
			name := sv(ii.IdentityName)
			if name == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:ses:%s:%s:identity/%s", region, acct.ID, name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypePinpointEmailIdentity, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(ii), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "pinpoint-email identities")
}
