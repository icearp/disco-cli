package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/emr"
	emrtypes "github.com/aws/aws-sdk-go-v2/service/emr/types"
	"github.com/icearp/disco-cli/internal/redact"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	// The Kerberos passwords are read back by DescribeCluster's deserializer
	// and the model marks none of them sensitive.
	registerType(restype.Descriptor{Type: TypeEMRCluster, Service: "emr", Redact: append([]redact.Rule{
		{Path: "KerberosAttributes.KdcAdminPassword", Mode: redact.RedactScalar},
		{Path: "KerberosAttributes.ADDomainJoinPassword", Mode: redact.RedactScalar},
		{Path: "KerberosAttributes.CrossRealmTrustPrincipalPassword", Mode: redact.RedactScalar},
	}, emrConfigurationRules("")...)})
	registerType(restype.Descriptor{Type: TypeEMRInstanceFleet, Service: "emr", Redact: emrConfigurationRules("InstanceTypeSpecifications[*].")})
	registerType(restype.Descriptor{Type: TypeEMRInstanceGroup, Service: "emr", Redact: append(
		emrConfigurationRules(""), emrConfigurationRules("LastSuccessfullyApplied")...)})
	registerType(restype.Descriptor{Type: TypeEMRSecurityConfig, Service: "emr"})
	// A step's Properties and Args can carry credentials. Each is judged by
	// its key name, or for an arg by the name in a name=value pair
	// ("spark.hadoop.fs.s3a.secret.key=…", "-Djavax.jdo.option.ConnectionPassword=…").
	// A secret passed as a separate arg after its flag ("--password", "hunter2")
	// has no name of its own, so nothing here can tell it from a plain arg.
	registerType(restype.Descriptor{Type: TypeEMRStep, Service: "emr", Redact: []redact.Rule{
		{Path: "Config.Properties.*", Mode: redact.RedactIfSensitive},
		{Path: "Config.Args[*]", Mode: redact.RedactIfSensitive},
	}})
	registerType(restype.Descriptor{Type: TypeEMRStudio, Service: "emr"})
	registerType(restype.Descriptor{Type: TypeEMRStudioSessionMapping, Service: "emr"})
	registerService(serviceEntry{
		name: "aws:emr",
		fn:   scanEMR,
	})
}

// emrConfigurationRules redacts the application configurations in the field
// named prefix+"Configurations", whose Properties can hold credentials
// (hive-site javax.jdo.option.ConnectionPassword, core-site
// fs.s3a.secret.key). The Properties of the top-level configurations and of
// their nested configurations are judged by key name, so settings such as
// hive.metastore.uris are kept. Paths have no recursive descent, so the
// configurations nested a level deeper are redacted whole, every scalar in
// them included, rather than left unjudged.
func emrConfigurationRules(prefix string) []redact.Rule {
	return []redact.Rule{
		{Path: prefix + "Configurations[*].Properties.*", Mode: redact.RedactIfSensitive},
		{Path: prefix + "Configurations[*].Configurations[*].Properties.*", Mode: redact.RedactIfSensitive},
		{Path: prefix + "Configurations[*].Configurations[*].Configurations", Mode: redact.RedactIfSensitive},
	}
}

type emrAPI interface {
	ListClusters(context.Context, *emr.ListClustersInput, ...func(*emr.Options)) (*emr.ListClustersOutput, error)
	DescribeCluster(context.Context, *emr.DescribeClusterInput, ...func(*emr.Options)) (*emr.DescribeClusterOutput, error)
	ListInstanceFleets(context.Context, *emr.ListInstanceFleetsInput, ...func(*emr.Options)) (*emr.ListInstanceFleetsOutput, error)
	ListInstanceGroups(context.Context, *emr.ListInstanceGroupsInput, ...func(*emr.Options)) (*emr.ListInstanceGroupsOutput, error)
	ListSecurityConfigurations(context.Context, *emr.ListSecurityConfigurationsInput, ...func(*emr.Options)) (*emr.ListSecurityConfigurationsOutput, error)
	ListSteps(context.Context, *emr.ListStepsInput, ...func(*emr.Options)) (*emr.ListStepsOutput, error)
	ListStudios(context.Context, *emr.ListStudiosInput, ...func(*emr.Options)) (*emr.ListStudiosOutput, error)
	ListStudioSessionMappings(context.Context, *emr.ListStudioSessionMappingsInput, ...func(*emr.Options)) (*emr.ListStudioSessionMappingsOutput, error)
}

func emrARN(partition, region, acct, kind, id string) string {
	return arnFor(partition, "elasticmapreduce", region, acct, kind+"/"+id)
}

func scanEMR(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := emr.NewFromConfig(acct.cfg, func(o *emr.Options) { o.Region = region })
	return scanEMRAll(ctx, client, acct, region, st, scanID)
}

const (
	emrListClustersOp    = "emr:ListClusters"
	emrDescribeClusterOp = "emr:DescribeCluster"
)

// scanEMRAll is the testable scan body. Every phase runs whatever an earlier
// one returned, except after a failed store write; the per-cluster phases get
// the clusters the cluster phase stored. The counts are summed and the errors
// joined through phaseTotals, which demotes a transient error to a warning
// only beside a hard one. The cluster phase is labelled "emr", not by one op,
// because its error may come from ListClusters or DescribeCluster.
func scanEMRAll(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var p phaseTotals
	clusters, t, i, err := scanEMRClusters(ctx, client, acct, region, st, scanID)
	if p.addPhase("emr", t, i, err) {
		return p.result(st, acct, region)
	}
	for _, cl := range clusters {
		t, i, err := scanEMRInstanceFleets(ctx, client, acct, region, st, scanID, cl.id, cl.arn)
		if p.addPhase("emr:ListInstanceFleets", t, i, err) {
			return p.result(st, acct, region)
		}
		t, i, err = scanEMRInstanceGroups(ctx, client, acct, region, st, scanID, cl.id, cl.arn)
		if p.addPhase("emr:ListInstanceGroups", t, i, err) {
			return p.result(st, acct, region)
		}
		t, i, err = scanEMRSteps(ctx, client, acct, region, st, scanID, cl.id, cl.arn)
		if p.addPhase("emr:ListSteps", t, i, err) {
			return p.result(st, acct, region)
		}
	}
	t, i, err = scanEMRSecurityConfigs(ctx, client, acct, region, st, scanID)
	if p.addPhase("emr:ListSecurityConfigurations", t, i, err) {
		return p.result(st, acct, region)
	}
	studioIDs, t, i, err := scanEMRStudios(ctx, client, acct, region, st, scanID)
	if p.addPhase("emr:ListStudios", t, i, err) {
		return p.result(st, acct, region)
	}
	for _, sid := range studioIDs {
		t, i, err := scanEMRStudioSessionMappings(ctx, client, acct, region, st, scanID, sid)
		if p.addPhase("emr:ListStudioSessionMappings", t, i, err) {
			break
		}
	}
	return p.result(st, acct, region)
}

// emrClusterRead is what one listed cluster's DescribeCluster returned.
type emrClusterRead struct {
	cluster *emrtypes.Cluster
	err     error
}

// scanEMRClusters stores every listed cluster, those read before a failed
// ListClusters page included, and returns the stored clusters that carry an
// Id (none after a failed write). ListClusters is sent no state filter, so it
// lists terminated clusters too. Each cluster is stored from its
// DescribeCluster body, the only source of its tags. DescribeCluster models
// no not-found code (only InternalServerException and InvalidRequestException),
// so no listed cluster is skipped: one whose DescribeCluster fails is stored
// as its ListClusters element, without tags. A denial is warned about once;
// every other error is returned after the rows are stored, except that a
// transient one beside a hard error is warned about under its op. A cluster
// whose DescribeCluster returns no Cluster is stored the same way and warned
// about. A listed cluster with no ClusterArn is not stored and is warned
// about; one with no Id cannot be described or have its children listed, so
// it is stored as its ListClusters element and warned about.
func scanEMRClusters(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	listed, listErr := readTokenPages(ctx, func(ctx context.Context, token *string) ([]emrtypes.ClusterSummary, *string, error) {
		out, err := client.ListClusters(ctx, &emr.ListClustersInput{Marker: token})
		if err != nil {
			return nil, nil, laterPageErr(token, "clusters", err)
		}
		return out.Clusters, out.Marker, nil
	})
	var (
		keyed          []emrtypes.ClusterSummary
		rows           []*store.Resource
		unkeyed, noIDs skipTally
	)
	for _, c := range listed {
		switch {
		case sv(c.ClusterArn) == "":
			unkeyed.skipped(sv(c.Id), nil)
		case sv(c.Id) == "":
			noIDs.skipped(sv(c.ClusterArn), nil)
			rows = append(rows, emrClusterRow(acct, region, scanID, c, nil))
		default:
			keyed = append(keyed, c)
		}
	}
	reads, stopErr := readEachKey(ctx, keyed, func(ctx context.Context, c emrtypes.ClusterSummary) emrClusterRead {
		out, err := client.DescribeCluster(ctx, &emr.DescribeClusterInput{ClusterId: c.Id})
		if err != nil {
			return emrClusterRead{err: err}
		}
		return emrClusterRead{cluster: out.Cluster}
	})
	var errs []error
	if stopErr != nil {
		errs = append(errs, fmt.Errorf("%s: %w", emrDescribeClusterOp, stopErr))
	}
	var (
		parents []childParent
		noBody  skipTally
	)
	described := cognitoKeptReads{op: emrDescribeClusterOp}
	for i, r := range reads {
		c := keyed[i]
		row := emrClusterRow(acct, region, scanID, c, r.cluster)
		rows = append(rows, row)
		parents = append(parents, childParent{id: sv(c.Id), arn: row.NativeID})
		described.add(sv(c.Id), r.err)
		if r.err == nil && r.cluster == nil {
			noBody.skipped(sv(c.Id), nil)
		}
	}
	const kept = "cluster(s) stored as their ListClusters element, without tags"
	total, inserted, err := storeReadRows(st, acct, region, emrListClustersOp, rows, listErr, func() {
		described.warnDenied(st, acct, region, kept)
		if noBody.count > 0 {
			st.ReportWarning(store.ScanWarning{
				Provider: "aws", Service: emrDescribeClusterOp, Scope: acct.ID + "/" + region,
				Message: fmt.Sprintf("%d %s: DescribeCluster returned no Cluster, first %s", noBody.count, kept, noBody.first),
			})
		}
		warnNotStored(st, emrListClustersOp, acct, region, &unkeyed, "clusters have an empty ClusterArn")
		warnNoChildID(st, emrListClustersOp, acct, region, &noIDs, "cluster(s), as their ListClusters element without tags,", "Id")
	}, errs...)
	if errors.Is(err, store.ErrStoreWrite) {
		return nil, total, inserted, err
	}
	var phase phaseErrs
	phase.add(emrListClustersOp, err)
	described.addFailed(&phase, kept)
	return parents, total, inserted, phase.join(st, acct, region)
}

// emrClusterRow builds a cluster's row from its DescribeCluster body, or from
// its ListClusters element when described is nil.
func emrClusterRow(acct *account, region, scanID string, listed emrtypes.ClusterSummary, described *emrtypes.Cluster) *store.Resource {
	label := sv(listed.Name)
	if label == "" {
		label = sv(listed.Id)
	}
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: TypeEMRCluster, NativeID: sv(listed.ClusterArn),
		Name: &label, Region: &region, AttributesJSON: mustJSON(listed), DiscoveredBy: scanID,
	}
	if described != nil {
		r.AttributesJSON = mustJSON(described)
		r.TagsJSON = kvTagsJSON(described.Tags, func(t emrtypes.Tag) (*string, *string) { return t.Key, t.Value })
	}
	return r
}

func scanEMRInstanceFleets(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, clusterID, clusterARN string) (int, int, error) {
	cid := clusterID
	pager := emr.NewListInstanceFleetsPaginator(client, &emr.ListInstanceFleetsInput{ClusterId: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) || isAPIErrorCode(perr, "InvalidRequestException") {
				_ = skipIfAccessDenied(st, "emr:ListInstanceFleets", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListInstanceFleets: %w", perr)
		}
		for _, f := range out.InstanceFleets {
			id := sv(f.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/instance-fleet/%s", clusterARN, id)
			label := sv(f.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRInstanceFleet, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr instance-fleets")
}

func scanEMRInstanceGroups(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, clusterID, clusterARN string) (int, int, error) {
	cid := clusterID
	pager := emr.NewListInstanceGroupsPaginator(client, &emr.ListInstanceGroupsInput{ClusterId: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) || isAPIErrorCode(perr, "InvalidRequestException") {
				_ = skipIfAccessDenied(st, "emr:ListInstanceGroups", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListInstanceGroups: %w", perr)
		}
		for _, g := range out.InstanceGroups {
			id := sv(g.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/instance-group/%s", clusterARN, id)
			label := sv(g.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRInstanceGroup, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr instance-groups")
}

func scanEMRSteps(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, clusterID, clusterARN string) (int, int, error) {
	cid := clusterID
	pager := emr.NewListStepsPaginator(client, &emr.ListStepsInput{ClusterId: &cid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListSteps", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListSteps: %w", perr)
		}
		for _, s := range out.Steps {
			id := sv(s.Id)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("%s/step/%s", clusterARN, id)
			label := sv(s.Name)
			if label == "" {
				label = id
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRStep, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr steps")
}

func scanEMRSecurityConfigs(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := emr.NewListSecurityConfigurationsPaginator(client, &emr.ListSecurityConfigurationsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListSecurityConfigurations", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListSecurityConfigurations: %w", perr)
		}
		for _, sc := range out.SecurityConfigurations {
			name := sv(sc.Name)
			if name == "" {
				continue
			}
			arn := emrARN(acct.partition(), region, acct.ID, "security-configuration", name)
			label := name
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRSecurityConfig, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(sc), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr security-configurations")
}

func scanEMRStudios(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
	pager := emr.NewListStudiosPaginator(client, &emr.ListStudiosInput{})
	var batch []*store.Resource
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListStudios", acct.ID, region, perr)
				return nil, 0, 0, nil
			}
			return nil, 0, 0, fmt.Errorf("emr:ListStudios: %w", perr)
		}
		for _, s := range out.Studios {
			id := sv(s.StudioId)
			if id == "" {
				continue
			}
			arn := emrARN(acct.partition(), region, acct.ID, "studio", id)
			label := sv(s.Name)
			if label == "" {
				label = id
			}
			ids = append(ids, id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRStudio, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "emr studios")
	return ids, t, i, err
}

func scanEMRStudioSessionMappings(ctx context.Context, client emrAPI, acct *account, region string, st *store.Store, scanID, studioID string) (int, int, error) {
	sid := studioID
	pager := emr.NewListStudioSessionMappingsPaginator(client, &emr.ListStudioSessionMappingsInput{StudioId: &sid})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "emr:ListStudioSessionMappings", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("emr:ListStudioSessionMappings: %w", perr)
		}
		for _, sm := range out.SessionMappings {
			iid := sv(sm.IdentityId)
			if iid == "" {
				continue
			}
			arn := arnFor(acct.partition(), "elasticmapreduce", region, acct.ID, "studio/"+studioID+"/identity/"+iid)
			label := sv(sm.IdentityName)
			if label == "" {
				label = iid
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeEMRStudioSessionMapping, NativeID: arn,
				Name: &label, Region: &region, AttributesJSON: mustJSON(sm), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "emr studio-session-mappings")
}
