package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/lexmodelsv2"
)

func init() {
	registerService(serviceEntry{
		name: "aws:lex",
		fn:   scanLex,
		emits: []coverage.TypeDecl{
			{Service: "lex", DiscoType: TypeLexBot},
			{Service: "lex", DiscoType: TypeLexBotAlias},
			{Service: "lex", DiscoType: TypeLexBotVersion},
			{Service: "lex", DiscoType: TypeLexResourcePolicy},
		},
	})
}

type lexAPI interface {
	ListBots(context.Context, *lexmodelsv2.ListBotsInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListBotsOutput, error)
	ListBotAliases(context.Context, *lexmodelsv2.ListBotAliasesInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListBotAliasesOutput, error)
	ListBotVersions(context.Context, *lexmodelsv2.ListBotVersionsInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.ListBotVersionsOutput, error)
	DescribeResourcePolicy(context.Context, *lexmodelsv2.DescribeResourcePolicyInput, ...func(*lexmodelsv2.Options)) (*lexmodelsv2.DescribeResourcePolicyOutput, error)
}

// scanLex discovers Lex V2 bots, aliases, versions, and per-bot resource
// policies. List APIs return only IDs; ARNs synthesized per AWS Lex shape.
func scanLex(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := lexmodelsv2.NewFromConfig(acct.cfg, func(o *lexmodelsv2.Options) { o.Region = region })

	type botRef struct{ id, arn string }
	bots, t, i, ferr := scanLexBots(ctx, client, acct, region, st, scanID)
	if ferr != nil {
		return total, inserted, ferr
	}
	total += t
	inserted += i

	for _, b := range bots {
		_ = botRef{}
		t, i, ferr = scanLexBotAliases(ctx, client, acct, region, st, scanID, b.id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanLexBotVersions(ctx, client, acct, region, st, scanID, b.id)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i

		t, i, ferr = scanLexResourcePolicy(ctx, client, acct, region, st, scanID, b.arn)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

type lexBot struct{ id, arn string }

func scanLexBots(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string) ([]lexBot, int, int, error) {
	pager := lexmodelsv2.NewListBotsPaginator(client, &lexmodelsv2.ListBotsInput{})
	var batch []*store.Resource
	var bots []lexBot
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "lexmodelsv2:ListBots", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("lexmodelsv2:ListBots: %w", err)
		}
		for _, b := range out.BotSummaries {
			id := sv(b.BotId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:lex:%s:%s:bot/%s", region, acct.ID, id)
			bots = append(bots, lexBot{id, arn})
			status := string(b.BotStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBot, NativeID: arn,
				Name: b.BotName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "lex bots")
	return bots, t, i, err
}

func scanLexBotAliases(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, botID string) (int, int, error) {
	bid := botID
	pager := lexmodelsv2.NewListBotAliasesPaginator(client, &lexmodelsv2.ListBotAliasesInput{BotId: &bid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lexmodelsv2:ListBotAliases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lexmodelsv2:ListBotAliases: %w", err)
		}
		for _, a := range out.BotAliasSummaries {
			aid := sv(a.BotAliasId)
			if aid == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:lex:%s:%s:bot-alias/%s/%s", region, acct.ID, bid, aid)
			status := string(a.BotAliasStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBotAlias, NativeID: arn,
				Name: a.BotAliasName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lex bot-aliases")
}

func scanLexBotVersions(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, botID string) (int, int, error) {
	bid := botID
	pager := lexmodelsv2.NewListBotVersionsPaginator(client, &lexmodelsv2.ListBotVersionsInput{BotId: &bid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "lexmodelsv2:ListBotVersions", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("lexmodelsv2:ListBotVersions: %w", err)
		}
		for _, v := range out.BotVersionSummaries {
			ver := sv(v.BotVersion)
			if ver == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:lex:%s:%s:bot/%s/version/%s", region, acct.ID, bid, ver)
			label := ver
			status := string(v.BotStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeLexBotVersion, NativeID: arn,
				Name: &label, Region: &region, Status: &status,
				AttributesJSON: mustJSON(v), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "lex bot-versions")
}

func scanLexResourcePolicy(ctx context.Context, client lexAPI, acct *account, region string, st *store.Store, scanID string, botARN string) (int, int, error) {
	out, err := client.DescribeResourcePolicy(ctx, &lexmodelsv2.DescribeResourcePolicyInput{ResourceArn: &botARN})
	if err != nil {
		if isAccessDenied(err) || isAPIErrorCode(err, "ResourceNotFoundException") {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("lexmodelsv2:DescribeResourcePolicy: %w", err)
	}
	if sv(out.Policy) == "" {
		return 0, 0, nil
	}
	arn := botARN + "/policy"
	label := "policy"
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeLexResourcePolicy, NativeID: arn,
		Name: &label, Region: &region,
		AttributesJSON: mustJSON(out), DiscoveredBy: scanID,
	}
	return upsertBatch(st, []*store.Resource{r}, "lex resource-policies")
}
