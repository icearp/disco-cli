package aws

import (
	"cmp"
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/configservice"
	cfgtypes "github.com/aws/aws-sdk-go-v2/service/configservice/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeConfigRule, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigRecorder, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigDeliveryChannel, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigAggregationAuthorization, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigConfigurationAggregator, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigConformancePack, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigOrganizationConfigRule, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigOrganizationConformancePack, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigRemediationConfiguration, Service: "config", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeConfigStoredQuery, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigConnector, Service: "config"})
	registerType(restype.Descriptor{Type: TypeConfigRemediationException, Service: "config"})
	// Not Managed despite being one per region: it exists only after
	// PutRetentionConfiguration and DeleteRetentionConfiguration removes it, so
	// it is user-created like the recorder and delivery channel.
	registerType(restype.Descriptor{Type: TypeConfigRetentionConfiguration, Service: "config"})
	registerService(serviceEntry{
		name: "aws:config",
		fn:   scanConfig,
	})
}

// configserviceAPI is the narrow set of AWS Config operations the Config
// scan calls.
type configserviceAPI interface {
	DescribeConfigurationRecorders(context.Context, *configservice.DescribeConfigurationRecordersInput, ...func(*configservice.Options)) (*configservice.DescribeConfigurationRecordersOutput, error)
	DescribeDeliveryChannels(context.Context, *configservice.DescribeDeliveryChannelsInput, ...func(*configservice.Options)) (*configservice.DescribeDeliveryChannelsOutput, error)
	DescribeConfigRules(context.Context, *configservice.DescribeConfigRulesInput, ...func(*configservice.Options)) (*configservice.DescribeConfigRulesOutput, error)
	DescribeAggregationAuthorizations(context.Context, *configservice.DescribeAggregationAuthorizationsInput, ...func(*configservice.Options)) (*configservice.DescribeAggregationAuthorizationsOutput, error)
	DescribeConfigurationAggregators(context.Context, *configservice.DescribeConfigurationAggregatorsInput, ...func(*configservice.Options)) (*configservice.DescribeConfigurationAggregatorsOutput, error)
	DescribeConformancePacks(context.Context, *configservice.DescribeConformancePacksInput, ...func(*configservice.Options)) (*configservice.DescribeConformancePacksOutput, error)
	DescribeOrganizationConfigRules(context.Context, *configservice.DescribeOrganizationConfigRulesInput, ...func(*configservice.Options)) (*configservice.DescribeOrganizationConfigRulesOutput, error)
	DescribeOrganizationConformancePacks(context.Context, *configservice.DescribeOrganizationConformancePacksInput, ...func(*configservice.Options)) (*configservice.DescribeOrganizationConformancePacksOutput, error)
	DescribeRemediationConfigurations(context.Context, *configservice.DescribeRemediationConfigurationsInput, ...func(*configservice.Options)) (*configservice.DescribeRemediationConfigurationsOutput, error)
	ListStoredQueries(context.Context, *configservice.ListStoredQueriesInput, ...func(*configservice.Options)) (*configservice.ListStoredQueriesOutput, error)
	DescribeRetentionConfigurations(context.Context, *configservice.DescribeRetentionConfigurationsInput, ...func(*configservice.Options)) (*configservice.DescribeRetentionConfigurationsOutput, error)
	ListConnectors(context.Context, *configservice.ListConnectorsInput, ...func(*configservice.Options)) (*configservice.ListConnectorsOutput, error)
	GetConnector(context.Context, *configservice.GetConnectorInput, ...func(*configservice.Options)) (*configservice.GetConnectorOutput, error)
	DescribeRemediationExceptions(context.Context, *configservice.DescribeRemediationExceptionsInput, ...func(*configservice.Options)) (*configservice.DescribeRemediationExceptionsOutput, error)
	ListTagsForResource(context.Context, *configservice.ListTagsForResourceInput, ...func(*configservice.Options)) (*configservice.ListTagsForResourceOutput, error)
}

const (
	configTagsOp = "config:ListTagsForResource"
	// configTagPageCap is ListTagsForResource's Limit. The op's doc says the
	// maximum is 50 and a larger number is refused, although the model's
	// range trait allows 100; the doc's lower bound is sent.
	configTagPageCap = 50
	// configPageCap is the Limit (MaxResults) range maximum of the listings
	// whose model gives 100.
	configPageCap = 100
	// configConformancePackPageCap is DescribeConformancePacks' Limit range
	// maximum.
	configConformancePackPageCap = 20
)

// configNotFoundCodes is the not-found code ListTagsForResource and GetConnector
// model, for a resource deleted after it was listed.
var configNotFoundCodes = []string{"ResourceNotFoundException"}

// configTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, which is the ARN its listing returned. Its doc and the Service
// Reference both accept every type this scanner tags: recorders, rules,
// organization rules, conformance packs, organization conformance packs,
// aggregators, aggregation authorizations, stored queries and connectors.
// No type is read under otherAccountGap: each listing returns only the
// account's own resources (the organization listings only those deployed
// from this account, per their docs), and an aggregation authorization's
// ARN carries this account, not the authorized one.
func configTags(client configserviceAPI) rowTagReader {
	return rowTagReader{
		op:       configTagsOp,
		notFound: configNotFoundCodes,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]cfgtypes.Tag, *string, error) {
				out, err := client.ListTagsForResource(ctx, &configservice.ListTagsForResourceInput{ResourceArn: &arn, Limit: configTagPageCap, NextToken: token})
				if err != nil {
					return nil, nil, err
				}
				return out.Tags, out.NextToken, nil
			})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(tags, func(t cfgtypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanConfig discovers AWS Config resources in one region. Every listing
// runs as its own phase, one at a time: a phase's error does not stop the
// later ones; a failed write and ctx's end do (runHostPhases). The
// remediation phases take the rules the rule phase stored.
func scanConfig(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := configservice.NewFromConfig(acct.cfg, func(o *configservice.Options) { o.Region = region })
	tags := configTags(client)
	var rules []childParent
	return runHostPhases(ctx, st, acct, region, "config", 0, []hostPhase{
		{op: "config:DescribeConfigurationRecorders", run: func(p *phaseTotals) bool {
			return scanConfigRecorders(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "config:DescribeDeliveryChannels", func() (int, int, error) {
			return scanConfigDeliveryChannels(ctx, client, acct, region, st, scanID)
		}),
		{op: "config:DescribeConfigRules", run: func(p *phaseTotals) (failed bool) {
			rules, failed = scanConfigRules(ctx, client, acct, region, st, scanID, tags, p)
			return failed
		}},
		{op: "config:DescribeAggregationAuthorizations", run: func(p *phaseTotals) bool {
			return scanConfigAggAuthz(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "config:DescribeConfigurationAggregators", run: func(p *phaseTotals) bool {
			return scanConfigAggregators(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "config:DescribeConformancePacks", run: func(p *phaseTotals) bool {
			return scanConfigConformancePacks(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "config:DescribeOrganizationConfigRules", run: func(p *phaseTotals) bool {
			return scanConfigOrgConfigRules(ctx, client, acct, region, st, scanID, tags, p)
		}},
		{op: "config:DescribeOrganizationConformancePacks", run: func(p *phaseTotals) bool {
			return scanConfigOrgConformancePacks(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "config:DescribeRemediationConfigurations", func() (int, int, error) {
			return scanConfigRemediationConfigs(ctx, client, rules, acct, region, st, scanID)
		}),
		{op: "config:ListStoredQueries", run: func(p *phaseTotals) bool {
			return scanConfigStoredQueries(ctx, client, acct, region, st, scanID, tags, p)
		}},
		countedPhase(0, "config:DescribeRetentionConfigurations", func() (int, int, error) {
			return scanConfigRetentionConfigurations(ctx, client, acct, region, st, scanID)
		}),
		countedPhase(0, "config:DescribeRemediationExceptions", func() (int, int, error) {
			return scanConfigRemediationExceptions(ctx, client, rules, acct, region, st, scanID)
		}),
		// Connectors go last on an assumption not verified live: as the newest
		// feature, they may be missing in some regions with an unmodelled
		// error, and last is where such an error costs the least.
		{op: "config:ListConnectors", run: func(p *phaseTotals) bool {
			return scanConfigConnectors(ctx, client, acct, region, st, scanID, tags, p)
		}},
	})
}

// configRow is the row of a listed element whose NativeID is nativeID. An
// element without one is not stored; what names the elements in the
// warning, and key names the element.
func configRow(acct *account, region, scanID, typ, what, nativeID, key string, name *string, item any) (*store.Resource, string, string) {
	if nativeID == "" {
		return nil, what, cmp.Or(key, "(an element with no identifier)")
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: nativeID,
		Name: name, Region: &region, AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// scanConfigRecorders stores the region's configuration recorders with their
// tags. A recorder listed without an Arn is stored under an ARN built from
// its name, which is not the Service Reference's
// configuration-recorder/{name}/{id} form, so its tags are not read: it is
// stored without tags, with one warning.
func scanConfigRecorders(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	const op = "config:DescribeConfigurationRecorders"
	var (
		skips notStoredSkips
		noARN skipTally
		rows  []*store.Resource
	)
	// Filled while the rows are built, before the tag reads start, and only
	// read by them.
	synthetic := map[*store.Resource]bool{}
	out, err := client.DescribeConfigurationRecorders(ctx, &configservice.DescribeConfigurationRecordersInput{})
	if err == nil {
		rows = buildListedRows(out.ConfigurationRecorders, func(r cfgtypes.ConfigurationRecorder) (*store.Resource, string, string) {
			if arn := sv(r.Arn); arn != "" {
				return configRow(acct, region, scanID, TypeConfigRecorder, "", arn, "", r.Name, r)
			}
			name := sv(r.Name)
			if name == "" {
				return nil, "configuration recorders have neither an Arn nor a name", "(a recorder with no identifier)"
			}
			row, _, _ := configRow(acct, region, scanID, TypeConfigRecorder, "", arnFor(acct.partition(), "config", region, acct.ID, "config-recorder/"+name), "", r.Name, r)
			synthetic[row] = true
			noARN.skipped(row.NativeID, nil)
			return row, "", ""
		}, nil, &skips)
	}
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		if synthetic[r] {
			return nil, nil
		}
		return read(ctx, r)
	}
	return storeRowsTags(ctx, st, acct, region, op, rows, err, func() {
		skips.warn(st, op, acct, region)
		if noARN.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: configTagsOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d configuration recorder(s) stored without tags: listed without the Arn the tag read is sent; first %s", noARN.count, noARN.first),
			})
		}
	}, tags, p)
}

// scanConfigDeliveryChannels stores the region's delivery channels, which
// are not taggable (neither ListTagsForResource nor TagResource accepts
// them). The API returns no ARN, so the id is built from the name.
func scanConfigDeliveryChannels(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "config:DescribeDeliveryChannels"
	var (
		skips notStoredSkips
		rows  []*store.Resource
	)
	out, err := client.DescribeDeliveryChannels(ctx, &configservice.DescribeDeliveryChannelsInput{})
	if err == nil {
		rows = buildListedRows(out.DeliveryChannels, func(d cfgtypes.DeliveryChannel) (*store.Resource, string, string) {
			id := ""
			if name := sv(d.Name); name != "" {
				id = arnFor(acct.partition(), "config", region, acct.ID, "delivery-channel/"+name)
			}
			return configRow(acct, region, scanID, TypeConfigDeliveryChannel, "delivery channels have no name", id, sv(d.S3BucketName), d.Name, d)
		}, nil, &skips)
	}
	return storeReadRows(st, acct, region, op, rows, err, func() { skips.warn(st, op, acct, region) })
}

// scanConfigRules stores the region's rules with their tags and returns the
// stored ones, keyed by ConfigRuleName, the parents of the remediation
// phases. DescribeConfigRules takes no page size.
func scanConfigRules(ctx context.Context, client configserviceAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) ([]childParent, bool) {
	linked := newConfigServiceLinked("config rule(s)")
	rules, failed := storeParentListTags(ctx, st, acct, region, "config:DescribeConfigRules", "config rules", "ConfigRuleName",
		func(ctx context.Context, token *string) ([]cfgtypes.ConfigRule, *string, error) {
			out, err := client.DescribeConfigRules(ctx, &configservice.DescribeConfigRulesInput{NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ConfigRules, out.NextToken, nil
		},
		func(r cfgtypes.ConfigRule) (*store.Resource, string, string) {
			row, why, key := configRow(acct, region, scanID, TypeConfigRule, "config rules have no ConfigRuleArn", sv(r.ConfigRuleArn), sv(r.ConfigRuleName), r.ConfigRuleName, r)
			if row != nil {
				status := string(r.ConfigRuleState)
				row.Status = &status
				linked.add(row, r.CreatedBy)
			}
			return row, why, key
		},
		func(r cfgtypes.ConfigRule) string { return sv(r.ConfigRuleName) },
		linked.reader(tags), p)
	if !failed {
		linked.warn(st, acct, region)
	}
	return rules, failed
}

// configServiceLinked tracks the rows of one listing whose element names the
// service that created it (CreatedBy: Security Hub, Firewall Manager, an
// organization rule's member copy). ListTagsForResource's doc does not
// exclude service-linked rules or packs, but whether it accepts them is not
// verified live. Were it to refuse them with ValidationException, every
// account running such a service would fail each scan, so for these rows
// only that code keeps the row without tags and is warned about once, after
// the write (backup's InvalidParameterValueException precedent for recovery
// points). On any other row ValidationException stays a hard error.
type configServiceLinked struct {
	what string
	// rows and linked are filled while the rows are built, before the tag
	// reads start, and only read by them.
	rows    []*store.Resource
	linked  map[*store.Resource]bool
	mu      sync.Mutex
	refused map[*store.Resource]error
}

func newConfigServiceLinked(what string) *configServiceLinked {
	return &configServiceLinked{what: what, linked: map[*store.Resource]bool{}, refused: map[*store.Resource]error{}}
}

// add records r as service-linked when createdBy names a service.
func (s *configServiceLinked) add(r *store.Resource, createdBy *string) {
	if sv(createdBy) == "" {
		return
	}
	s.rows = append(s.rows, r)
	s.linked[r] = true
}

// reader is tags with a ValidationException on a service-linked row read as
// no tags, the refusal kept for warn.
func (s *configServiceLinked) reader(tags rowTagReader) rowTagReader {
	read := tags.read
	tags.read = func(ctx context.Context, r *store.Resource) (*string, error) {
		got, err := read(ctx, r)
		if s.linked[r] && isAPIErrorCode(err, "ValidationException") {
			s.mu.Lock()
			s.refused[r] = err
			s.mu.Unlock()
			return nil, nil
		}
		return got, err
	}
	return tags
}

// warn warns once under the tag op about the service-linked rows whose tag
// read was refused, naming the first in listing order. Call it only after
// the write succeeded.
func (s *configServiceLinked) warn(st *store.Store, acct *account, region string) {
	var t skipTally
	for _, r := range s.rows {
		if err, ok := s.refused[r]; ok {
			t.skipped(r.NativeID, err)
		}
	}
	if t.count == 0 {
		return
	}
	st.ReportWarning(store.ScanWarning{
		Provider: "aws", Service: configTagsOp, Scope: acct.ID + "/" + region,
		Message: fmt.Sprintf("%d service-linked %s stored without tags: ListTagsForResource refused them; first %s: %v", t.count, s.what, t.first, t.err),
	})
}
