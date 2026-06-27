package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// SageMaker resolver registry — extended sources beyond endpoint/endpoint-config/
// model. One resolver per source disco type so each function stays well below the
// gocognit threshold; common target id-sets are loaded once via sagemakerEdgeSets.
//
// All scanners persist `mustJSON(DescribeXOutput)` so the JSON keys match the
// SDK PascalCase field names (per aws/CLAUDE.md).

func init() {
	registerResolver(
		resolveSageMakerNotebookInstance,
		EdgeDecl{TypeSageMakerNotebookInstance, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerNotebookInstance, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerNotebookInstance, TypeEC2SecurityGroup, store.RelUses},
		EdgeDecl{TypeSageMakerNotebookInstance, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSageMakerNotebookInstance, TypeSageMakerNotebookInstanceLifecycleConfig, store.RelUses},
		EdgeDecl{TypeSageMakerNotebookInstance, TypeSageMakerCodeRepository, store.RelUses},
	)
	registerResolver(
		resolveSageMakerDomain,
		EdgeDecl{TypeSageMakerDomain, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerDomain, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerDomain, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSageMakerDomain, TypeEFSFileSystem, store.RelUses},
	)
	registerResolver(
		resolveSageMakerUserProfile,
		EdgeDecl{TypeSageMakerUserProfile, TypeSageMakerDomain, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerSpace,
		EdgeDecl{TypeSageMakerSpace, TypeSageMakerDomain, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerApp,
		EdgeDecl{TypeSageMakerApp, TypeSageMakerDomain, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerApp, TypeSageMakerUserProfile, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerApp, TypeSageMakerSpace, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerApp, TypeSageMakerImageVersion, store.RelUses},
	)
	registerResolver(
		resolveSageMakerImageVersionParent,
		EdgeDecl{TypeSageMakerImageVersion, TypeSageMakerImage, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerImageRefs,
		EdgeDecl{TypeSageMakerImage, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveSageMakerFeatureGroup,
		EdgeDecl{TypeSageMakerFeatureGroup, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerFeatureGroup, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSageMakerFeatureGroup, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveSageMakerModelPackageGroup,
		EdgeDecl{TypeSageMakerModelPackageGroup, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveSageMakerModelPackage,
		EdgeDecl{TypeSageMakerModelPackage, TypeSageMakerModelPackageGroup, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModelPackage, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeSageMakerModelPackage, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveSageMakerMonitoringSchedule,
		EdgeDecl{TypeSageMakerMonitoringSchedule, TypeSageMakerEndpoint, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerDataQualityJobDefinition,
		EdgeDecl{TypeSageMakerDataQualityJobDefinition, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerDataQualityJobDefinition, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSageMakerDataQualityJobDefinition, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSageMakerDataQualityJobDefinition, TypeSageMakerEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerDataQualityJobDefinition, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerDataQualityJobDefinition, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveSageMakerModelBiasJobDefinition,
		EdgeDecl{TypeSageMakerModelBiasJobDefinition, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerModelBiasJobDefinition, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSageMakerModelBiasJobDefinition, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSageMakerModelBiasJobDefinition, TypeSageMakerEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModelBiasJobDefinition, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModelBiasJobDefinition, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveSageMakerModelExplainabilityJobDefinition,
		EdgeDecl{TypeSageMakerModelExplainabilityJobDefinition, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerModelExplainabilityJobDefinition, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSageMakerModelExplainabilityJobDefinition, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSageMakerModelExplainabilityJobDefinition, TypeSageMakerEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModelExplainabilityJobDefinition, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModelExplainabilityJobDefinition, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveSageMakerModelQualityJobDefinition,
		EdgeDecl{TypeSageMakerModelQualityJobDefinition, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerModelQualityJobDefinition, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSageMakerModelQualityJobDefinition, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSageMakerModelQualityJobDefinition, TypeSageMakerEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModelQualityJobDefinition, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModelQualityJobDefinition, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveSageMakerProcessingJob,
		EdgeDecl{TypeSageMakerProcessingJob, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerProcessingJob, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeSageMakerProcessingJob, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSageMakerProcessingJob, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerProcessingJob, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveSageMakerPipeline,
		EdgeDecl{TypeSageMakerPipeline, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(resolveSageMakerProject) // Project has no edge-bearing attributes beyond the service-catalog
	// product details, which point at non-disco resource types.

	registerResolver(
		resolveSageMakerMlflowTrackingServer,
		EdgeDecl{TypeSageMakerMlflowTrackingServer, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerMlflowTrackingServer, TypeS3Bucket, store.RelUses},
	)
	registerResolver(
		resolveSageMakerPartnerApp,
		EdgeDecl{TypeSageMakerPartnerApp, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerPartnerApp, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveSageMakerCluster,
		EdgeDecl{TypeSageMakerCluster, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerCluster, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerCluster, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveSageMakerDeviceFleet,
		EdgeDecl{TypeSageMakerDeviceFleet, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerDeviceFleet, TypeS3Bucket, store.RelUses},
		EdgeDecl{TypeSageMakerDeviceFleet, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveSageMakerDevice,
		EdgeDecl{TypeSageMakerDevice, TypeSageMakerDeviceFleet, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerInferenceComponent,
		EdgeDecl{TypeSageMakerInferenceComponent, TypeSageMakerEndpoint, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerInferenceExperiment,
		EdgeDecl{TypeSageMakerInferenceExperiment, TypeSageMakerEndpoint, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerInferenceExperiment, TypeSageMakerModel, store.RelUses},
		EdgeDecl{TypeSageMakerInferenceExperiment, TypeKMSKey, store.RelUses},
	)
	registerResolver(
		resolveSageMakerModelCard,
		EdgeDecl{TypeSageMakerModelCard, TypeKMSKey, store.RelUses},
	)
	// model-package, app-image-config, code-repository, mlflow-tracking-server
	// are covered above. notebook-instance-lifecycle-config and studio-
	// lifecycle-config carry only inline shell scripts (no IAM/network/KMS
	// refs) — no resolver. workteam edges (Cognito user pool / OIDC config)
	// are intentionally skipped: workforces live behind a separate
	// DescribeWorkforce API not currently scanned, so the edges would dangle.
}

// sagemakerARN rebuilds the canonical ARN for an arbitrary SageMaker entity
// kind (`notebook-instance-lifecycle-config`, `code-repository`, `image`,
// `domain`, `endpoint`, …) given its name. SageMaker's ARN shape is uniform:
// `arn:aws:sagemaker:{r}:{a}:{kind}/{name}`.
func sagemakerARN(region, acctID, kind, name string) string {
	return fmt.Sprintf("arn:aws:sagemaker:%s:%s:%s/%s", region, acctID, kind, name)
}

// efsFileSystemARN rebuilds the canonical EFS file-system ARN from a bare
// fs-XXXXXXXX id. Mirrors the shape used by efs_scanners.go.
func efsFileSystemARN(region, acctID, fsID string) string {
	return fmt.Sprintf("arn:aws:elasticfilesystem:%s:%s:file-system/%s", region, acctID, fsID)
}

// sagemakerEdgeSets bundles the FK-safe target id-sets and KMS resolve-index
// shared by most extended resolvers below. Loaded once per resolver call.
type sagemakerEdgeSets struct {
	roles    map[string]bool
	subnets  map[string]bool
	sgs      map[string]bool
	vpcs     map[string]bool
	buckets  map[string]bool
	endpts   map[string]bool
	repos    map[string]bool
	efs      map[string]bool
	domains  map[string]bool
	users    map[string]bool
	spaces   map[string]bool
	models   map[string]bool
	imgs     map[string]bool
	imgvers  map[string]bool
	mpgroups map[string]bool
	lcconfs  map[string]bool
	coderepo map[string]bool
	fleets   map[string]bool
	kmsIdx   *kmsResolveIndex
}

// loadSagemakerEdgeSets builds every target id-set used by the extended
// SageMaker resolvers. Cheap (one ListResources per type) — every call site
// is one resolver, so the per-resolver duplication is intentional for clarity.
func loadSagemakerEdgeSets(acct *account, st *store.Store, types ...string) (*sagemakerEdgeSets, error) {
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[t] = true
	}
	s := &sagemakerEdgeSets{}
	loaders := []struct {
		t   string
		dst *map[string]bool
	}{
		{TypeIAMRole, &s.roles},
		{TypeEC2Subnet, &s.subnets},
		{TypeEC2SecurityGroup, &s.sgs},
		{TypeEC2VPC, &s.vpcs},
		{TypeS3Bucket, &s.buckets},
		{TypeSageMakerEndpoint, &s.endpts},
		{TypeECRRepository, &s.repos},
		{TypeEFSFileSystem, &s.efs},
		{TypeSageMakerDomain, &s.domains},
		{TypeSageMakerUserProfile, &s.users},
		{TypeSageMakerSpace, &s.spaces},
		{TypeSageMakerModel, &s.models},
		{TypeSageMakerImage, &s.imgs},
		{TypeSageMakerImageVersion, &s.imgvers},
		{TypeSageMakerModelPackageGroup, &s.mpgroups},
		{TypeSageMakerNotebookInstanceLifecycleConfig, &s.lcconfs},
		{TypeSageMakerCodeRepository, &s.coderepo},
		{TypeSageMakerDeviceFleet, &s.fleets},
	}
	for _, l := range loaders {
		if !want[l.t] {
			continue
		}
		set, err := scannedIDSet(acct, st, l.t)
		if err != nil {
			return nil, err
		}
		*l.dst = set
	}
	if want[TypeKMSKey] {
		idx, err := loadKMSResolveIndex(acct, st)
		if err != nil {
			return nil, err
		}
		s.kmsIdx = idx
	}
	return s, nil
}

// listSources lists every resource of one type for the resolver's account.
func listSources(acct *account, st *store.Store, rtype string) ([]store.Resource, error) {
	return st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{rtype},
		Limit: util.AllResources,
	})
}

// emitIAMRole emits a source → IAM role `assumes` edge if the role ARN is
// non-empty and the role row was scanned. Centralises the FK-safe lookup.
func emitIAMRole(st *store.Store, srcID, roleARN, acctID string, sets *sagemakerEdgeSets) error {
	if roleARN == "" {
		return nil
	}
	roleID := store.ResourceID("aws", acctID, TypeIAMRole, roleARN)
	if !sets.roles[roleID] {
		return nil
	}
	return st.UpsertRelationship(srcID, roleID, store.RelAssumes, "directed", nil)
}

// emitKMS emits a source → KMS key `uses` edge using the shared resolve index.
func emitKMS(st *store.Store, srcID, ref, region, acctID string, sets *sagemakerEdgeSets) error {
	if ref == "" || sets.kmsIdx == nil {
		return nil
	}
	keyID, ok := sets.kmsIdx.resolveKMSKeyID(ref, region, acctID)
	if !ok {
		return nil
	}
	return st.UpsertRelationship(srcID, keyID, store.RelUses, "directed", nil)
}

// emitS3Bucket emits a source → S3 bucket `uses` edge when the s3:// URL
// resolves to a scanned bucket.
func emitS3Bucket(st *store.Store, srcID, s3url, acctID string, sets *sagemakerEdgeSets) error {
	bucketARN := s3BucketARNFromS3URL(s3url)
	if bucketARN == "" {
		return nil
	}
	bID := store.ResourceID("aws", acctID, TypeS3Bucket, bucketARN)
	if !sets.buckets[bID] {
		return nil
	}
	return st.UpsertRelationship(srcID, bID, store.RelUses, "directed", nil)
}

// emitSubnetEdges emits subnet `attached-to` edges for a list of bare subnet ids.
func emitSubnetEdges(st *store.Store, srcID, region, acctID string, ids []string, sets *sagemakerEdgeSets) error {
	for _, sn := range ids {
		if sn == "" {
			continue
		}
		snID := store.ResourceID("aws", acctID, TypeEC2Subnet, ec2ARN(region, acctID, "subnet", sn))
		if !sets.subnets[snID] {
			continue
		}
		if err := st.UpsertRelationship(srcID, snID, store.RelAttachedTo, "directed", nil); err != nil {
			return err
		}
	}
	return nil
}

// emitSGEdges emits security-group `uses` edges for a list of bare sg ids.
func emitSGEdges(st *store.Store, srcID, region, acctID string, ids []string, sets *sagemakerEdgeSets) error {
	for _, sg := range ids {
		if sg == "" {
			continue
		}
		sgID := store.ResourceID("aws", acctID, TypeEC2SecurityGroup, ec2ARN(region, acctID, "security-group", sg))
		if !sets.sgs[sgID] {
			continue
		}
		if err := st.UpsertRelationship(srcID, sgID, store.RelUses, "directed", nil); err != nil {
			return err
		}
	}
	return nil
}

// emitEndpointByName emits an endpoint `attached-to` edge by synthesising the
// endpoint ARN from its bare name.
func emitEndpointByName(st *store.Store, srcID, region, acctID, name string, sets *sagemakerEdgeSets) error {
	if name == "" {
		return nil
	}
	epID := store.ResourceID("aws", acctID, TypeSageMakerEndpoint, sagemakerARN(region, acctID, "endpoint", name))
	if !sets.endpts[epID] {
		return nil
	}
	return st.UpsertRelationship(srcID, epID, store.RelAttachedTo, "directed", nil)
}

// resolveSageMakerNotebookInstance walks IAM role + subnet + SG + KMS +
// lifecycle-config + code-repository edges from each notebook instance.
func resolveSageMakerNotebookInstance(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerNotebookInstance)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st,
		TypeIAMRole, TypeEC2Subnet, TypeEC2SecurityGroup, TypeKMSKey,
		TypeSageMakerNotebookInstanceLifecycleConfig, TypeSageMakerCodeRepository)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			RoleArn                             *string  `json:"RoleArn"`
			SubnetID                            *string  `json:"SubnetId"`
			SecurityGroups                      []string `json:"SecurityGroups"`
			KmsKeyID                            *string  `json:"KmsKeyId"`
			NotebookInstanceLifecycleConfigName *string  `json:"NotebookInstanceLifecycleConfigName"`
			DefaultCodeRepository               *string  `json:"DefaultCodeRepository"`
			AdditionalCodeRepositories          []string `json:"AdditionalCodeRepositories"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitIAMRole(st, r.ID, sv(a.RoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("notebook→role: %w", err)
		}
		if err := emitSubnetEdges(st, r.ID, region, acct.ID, []string{sv(a.SubnetID)}, sets); err != nil {
			return fmt.Errorf("notebook→subnet: %w", err)
		}
		if err := emitSGEdges(st, r.ID, region, acct.ID, a.SecurityGroups, sets); err != nil {
			return fmt.Errorf("notebook→sg: %w", err)
		}
		if err := emitKMS(st, r.ID, sv(a.KmsKeyID), region, acct.ID, sets); err != nil {
			return fmt.Errorf("notebook→kms: %w", err)
		}
		if err := emitNotebookLCConfig(st, r.ID, region, acct.ID, sv(a.NotebookInstanceLifecycleConfigName), sets); err != nil {
			return err
		}
		repos := append([]string{sv(a.DefaultCodeRepository)}, a.AdditionalCodeRepositories...)
		if err := emitNotebookCodeRepos(st, r.ID, region, acct.ID, repos, sets); err != nil {
			return err
		}
	}
	return nil
}

// emitNotebookLCConfig links a notebook instance to its lifecycle config row.
func emitNotebookLCConfig(st *store.Store, srcID, region, acctID, name string, sets *sagemakerEdgeSets) error {
	if name == "" {
		return nil
	}
	cfgID := store.ResourceID("aws", acctID, TypeSageMakerNotebookInstanceLifecycleConfig,
		sagemakerARN(region, acctID, "notebook-instance-lifecycle-config", name))
	if !sets.lcconfs[cfgID] {
		return nil
	}
	return st.UpsertRelationship(srcID, cfgID, store.RelUses, "directed", nil)
}

// emitNotebookCodeRepos links to every scanned code-repository named on the
// notebook (default + additional). Bare names are synthesised; full URLs to
// remote git providers (which never resolve to a code-repository row) skip.
func emitNotebookCodeRepos(st *store.Store, srcID, region, acctID string, names []string, sets *sagemakerEdgeSets) error {
	for _, n := range names {
		if n == "" {
			continue
		}
		repoID := store.ResourceID("aws", acctID, TypeSageMakerCodeRepository,
			sagemakerARN(region, acctID, "code-repository", n))
		if !sets.coderepo[repoID] {
			continue
		}
		if err := st.UpsertRelationship(srcID, repoID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("notebook→code-repository: %w", err)
		}
	}
	return nil
}

// resolveSageMakerDomain walks Studio domain → VPC, subnets, KMS, EFS edges.
func resolveSageMakerDomain(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerDomain)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeEC2VPC, TypeEC2Subnet, TypeKMSKey, TypeEFSFileSystem)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			VpcID               *string  `json:"VpcId"`
			SubnetIDs           []string `json:"SubnetIds"`
			KmsKeyID            *string  `json:"KmsKeyId"`
			HomeEfsFileSystemID *string  `json:"HomeEfsFileSystemId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if vpc := sv(a.VpcID); vpc != "" {
			vpcID := store.ResourceID("aws", acct.ID, TypeEC2VPC, ec2ARN(region, acct.ID, "vpc", vpc))
			if sets.vpcs[vpcID] {
				if err := st.UpsertRelationship(r.ID, vpcID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("domain→vpc: %w", err)
				}
			}
		}
		if err := emitSubnetEdges(st, r.ID, region, acct.ID, a.SubnetIDs, sets); err != nil {
			return fmt.Errorf("domain→subnet: %w", err)
		}
		if err := emitKMS(st, r.ID, sv(a.KmsKeyID), region, acct.ID, sets); err != nil {
			return fmt.Errorf("domain→kms: %w", err)
		}
		if fs := sv(a.HomeEfsFileSystemID); fs != "" {
			fsID := store.ResourceID("aws", acct.ID, TypeEFSFileSystem, efsFileSystemARN(region, acct.ID, fs))
			if sets.efs[fsID] {
				if err := st.UpsertRelationship(r.ID, fsID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("domain→efs: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveSageMakerUserProfile links each user profile to its parent domain row.
func resolveSageMakerUserProfile(acct *account, st *store.Store) error {
	return resolveSagemakerDomainParent(acct, st, TypeSageMakerUserProfile)
}

// resolveSageMakerSpace links each space to its parent domain row.
func resolveSageMakerSpace(acct *account, st *store.Store) error {
	return resolveSagemakerDomainParent(acct, st, TypeSageMakerSpace)
}

// resolveSagemakerDomainParent emits a `attached-to` edge from each row of
// `srcType` to the domain referenced by its `DomainID` attr. Shared by
// user-profile and space resolvers — both have the identical shape.
func resolveSagemakerDomainParent(acct *account, st *store.Store, srcType string) error {
	rs, err := listSources(acct, st, srcType)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeSageMakerDomain)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			DomainID *string `json:"DomainId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		id := sv(a.DomainID)
		if id == "" {
			continue
		}
		region := sv(r.Region)
		domID := store.ResourceID("aws", acct.ID, TypeSageMakerDomain,
			sagemakerARN(region, acct.ID, "domain", id))
		if !sets.domains[domID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, domID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("%s→domain: %w", srcType, err)
		}
	}
	return nil
}

// resolveSageMakerApp walks app → domain, user-profile, space, image-version edges.
func resolveSageMakerApp(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerApp)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st,
		TypeSageMakerDomain, TypeSageMakerUserProfile, TypeSageMakerSpace, TypeSageMakerImageVersion)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			DomainID        *string `json:"DomainId"`
			UserProfileName *string `json:"UserProfileName"`
			SpaceName       *string `json:"SpaceName"`
			ResourceSpec    *struct {
				SageMakerImageVersionArn *string `json:"SageMakerImageVersionArn"`
			} `json:"ResourceSpec"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		dom := sv(a.DomainID)
		if dom != "" {
			domID := store.ResourceID("aws", acct.ID, TypeSageMakerDomain,
				sagemakerARN(region, acct.ID, "domain", dom))
			if sets.domains[domID] {
				if err := st.UpsertRelationship(r.ID, domID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("app→domain: %w", err)
				}
			}
		}
		if up := sv(a.UserProfileName); dom != "" && up != "" {
			upARN := sagemakerARN(region, acct.ID, "user-profile", dom+"/"+up)
			upID := store.ResourceID("aws", acct.ID, TypeSageMakerUserProfile, upARN)
			if sets.users[upID] {
				if err := st.UpsertRelationship(r.ID, upID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("app→user-profile: %w", err)
				}
			}
		}
		if sp := sv(a.SpaceName); dom != "" && sp != "" {
			spARN := sagemakerARN(region, acct.ID, "space", dom+"/"+sp)
			spID := store.ResourceID("aws", acct.ID, TypeSageMakerSpace, spARN)
			if sets.spaces[spID] {
				if err := st.UpsertRelationship(r.ID, spID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("app→space: %w", err)
				}
			}
		}
		if a.ResourceSpec != nil {
			if iv := sv(a.ResourceSpec.SageMakerImageVersionArn); iv != "" {
				ivID := store.ResourceID("aws", acct.ID, TypeSageMakerImageVersion, iv)
				if sets.imgvers[ivID] {
					if err := st.UpsertRelationship(r.ID, ivID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("app→image-version: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// resolveSageMakerImageVersionParent links each image-version to its parent
// image row via the `ImageArn` attr.
func resolveSageMakerImageVersionParent(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerImageVersion)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeSageMakerImage)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			ImageArn *string `json:"ImageArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		arn := sv(a.ImageArn)
		if arn == "" {
			continue
		}
		imgID := store.ResourceID("aws", acct.ID, TypeSageMakerImage, arn)
		if !sets.imgs[imgID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, imgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("image-version→image: %w", err)
		}
	}
	return nil
}

// resolveSageMakerImageRefs walks each image's RoleArn → IAM role.
func resolveSageMakerImageRefs(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerImage)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitIAMRole(st, r.ID, sv(a.RoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("image→role: %w", err)
		}
	}
	return nil
}

// resolveSageMakerFeatureGroup walks feature-group → IAM role, S3 offline
// store, KMS edges (offline-store + online-store-security-config).
func resolveSageMakerFeatureGroup(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerFeatureGroup)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeIAMRole, TypeS3Bucket, TypeKMSKey)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			RoleArn            *string `json:"RoleArn"`
			OfflineStoreConfig *struct {
				S3StorageConfig *struct {
					S3Uri    *string `json:"S3Uri"`
					KmsKeyID *string `json:"KmsKeyId"`
				} `json:"S3StorageConfig"`
			} `json:"OfflineStoreConfig"`
			OnlineStoreConfig *struct {
				SecurityConfig *struct {
					KmsKeyID *string `json:"KmsKeyId"`
				} `json:"SecurityConfig"`
			} `json:"OnlineStoreConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitIAMRole(st, r.ID, sv(a.RoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("feature-group→role: %w", err)
		}
		if a.OfflineStoreConfig != nil && a.OfflineStoreConfig.S3StorageConfig != nil {
			s3 := a.OfflineStoreConfig.S3StorageConfig
			if err := emitS3Bucket(st, r.ID, sv(s3.S3Uri), acct.ID, sets); err != nil {
				return fmt.Errorf("feature-group→s3: %w", err)
			}
			if err := emitKMS(st, r.ID, sv(s3.KmsKeyID), region, acct.ID, sets); err != nil {
				return fmt.Errorf("feature-group→kms (offline): %w", err)
			}
		}
		if a.OnlineStoreConfig != nil && a.OnlineStoreConfig.SecurityConfig != nil {
			if err := emitKMS(st, r.ID, sv(a.OnlineStoreConfig.SecurityConfig.KmsKeyID), region, acct.ID, sets); err != nil {
				return fmt.Errorf("feature-group→kms (online): %w", err)
			}
		}
	}
	return nil
}

// resolveSageMakerModelPackageGroup — model package groups themselves carry no
// KMS field on the standard Describe response; reserved for future settings.
// Currently emits no edges; resolver retained so registration test passes.
func resolveSageMakerModelPackageGroup(acct *account, st *store.Store) error {
	_ = acct
	_ = st
	return nil
}

// resolveSageMakerModelPackage links each model package to its group, the
// container ECR repos, and the validation IAM role.
func resolveSageMakerModelPackage(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerModelPackage)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st,
		TypeSageMakerModelPackageGroup, TypeECRRepository, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			ModelPackageGroupName  *string `json:"ModelPackageGroupName"`
			InferenceSpecification *struct {
				Containers []struct {
					Image *string `json:"Image"`
				} `json:"Containers"`
			} `json:"InferenceSpecification"`
			ValidationSpecification *struct {
				ValidationRole *string `json:"ValidationRole"`
			} `json:"ValidationSpecification"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if g := sv(a.ModelPackageGroupName); g != "" {
			gID := store.ResourceID("aws", acct.ID, TypeSageMakerModelPackageGroup,
				sagemakerARN(region, acct.ID, "model-package-group", g))
			if sets.mpgroups[gID] {
				if err := st.UpsertRelationship(r.ID, gID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("model-package→group: %w", err)
				}
			}
		}
		if a.InferenceSpecification != nil {
			for _, c := range a.InferenceSpecification.Containers {
				repoARN := apprunnerImageToRepoARN(sv(c.Image))
				if repoARN == "" {
					continue
				}
				rID := store.ResourceID("aws", acct.ID, TypeECRRepository, repoARN)
				if !sets.repos[rID] {
					continue
				}
				if err := st.UpsertRelationship(r.ID, rID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("model-package→ecr: %w", err)
				}
			}
		}
		if a.ValidationSpecification != nil {
			if err := emitIAMRole(st, r.ID, sv(a.ValidationSpecification.ValidationRole), acct.ID, sets); err != nil {
				return fmt.Errorf("model-package→role: %w", err)
			}
		}
	}
	return nil
}

// resolveSageMakerMonitoringSchedule links a schedule to the endpoint it
// monitors via its top-level `EndpointName`.
func resolveSageMakerMonitoringSchedule(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerMonitoringSchedule)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeSageMakerEndpoint)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			EndpointName *string `json:"EndpointName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitEndpointByName(st, r.ID, sv(r.Region), acct.ID, sv(a.EndpointName), sets); err != nil {
			return fmt.Errorf("monitoring-schedule→endpoint: %w", err)
		}
	}
	return nil
}

type jobDefNetworkConfig struct {
	VpcConfig *struct {
		Subnets          []string `json:"Subnets"`
		SecurityGroupIDs []string `json:"SecurityGroupIds"`
	} `json:"VpcConfig"`
}

type jobDefOutputConfig struct {
	KmsKeyID          *string `json:"KmsKeyId"`
	MonitoringOutputs []struct {
		S3Output *struct {
			S3Uri *string `json:"S3Uri"`
		} `json:"S3Output"`
	} `json:"MonitoringOutputs"`
}

type jobDefJobInput struct {
	EndpointInput *struct {
		EndpointName *string `json:"EndpointName"`
	} `json:"EndpointInput"`
}

// resolveSageMakerDataQualityJobDefinition walks the data-quality variant.
func resolveSageMakerDataQualityJobDefinition(acct *account, st *store.Store) error {
	return resolveSagemakerJobDef(acct, st, TypeSageMakerDataQualityJobDefinition,
		"DataQualityJobInput", "DataQualityJobOutputConfig")
}

// resolveSageMakerModelBiasJobDefinition walks the model-bias variant.
func resolveSageMakerModelBiasJobDefinition(acct *account, st *store.Store) error {
	return resolveSagemakerJobDef(acct, st, TypeSageMakerModelBiasJobDefinition,
		"ModelBiasJobInput", "ModelBiasJobOutputConfig")
}

// resolveSageMakerModelExplainabilityJobDefinition walks the explainability variant.
func resolveSageMakerModelExplainabilityJobDefinition(acct *account, st *store.Store) error {
	return resolveSagemakerJobDef(acct, st, TypeSageMakerModelExplainabilityJobDefinition,
		"ModelExplainabilityJobInput", "ModelExplainabilityJobOutputConfig")
}

// resolveSageMakerModelQualityJobDefinition walks the model-quality variant.
func resolveSageMakerModelQualityJobDefinition(acct *account, st *store.Store) error {
	return resolveSagemakerJobDef(acct, st, TypeSageMakerModelQualityJobDefinition,
		"ModelQualityJobInput", "ModelQualityJobOutputConfig")
}

// resolveSagemakerJobDef is the shared body of the four job-definition
// resolvers. The four variants differ only in the JSON keys carrying their
// JobInput and OutputConfig — hence the two key parameters.
func resolveSagemakerJobDef(acct *account, st *store.Store, srcType, inputKey, outputKey string) error {
	rs, err := listSources(acct, st, srcType)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st,
		TypeIAMRole, TypeKMSKey, TypeS3Bucket, TypeSageMakerEndpoint, TypeEC2Subnet, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rs {
		// Decode into a generic map so we can pick out the variant-named
		// keys without a separate struct per variant.
		raw := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &raw); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitJobDefRole(st, r.ID, acct.ID, raw, sets); err != nil {
			return err
		}
		if err := emitJobDefNetwork(st, r.ID, region, acct.ID, raw, sets); err != nil {
			return err
		}
		if err := emitJobDefInput(st, r.ID, region, acct.ID, raw[inputKey], sets); err != nil {
			return err
		}
		if err := emitJobDefOutput(st, r.ID, region, acct.ID, raw[outputKey], sets); err != nil {
			return err
		}
	}
	return nil
}

// emitJobDefRole emits the IAM role edge from a job-definition raw map.
func emitJobDefRole(st *store.Store, srcID, acctID string, raw map[string]json.RawMessage, sets *sagemakerEdgeSets) error {
	var role string
	if err := json.Unmarshal(raw["RoleArn"], &role); err != nil || role == "" {
		return nil
	}
	return emitIAMRole(st, srcID, role, acctID, sets)
}

// emitJobDefNetwork emits subnet + sg edges from `NetworkConfig.VpcConfig`.
func emitJobDefNetwork(st *store.Store, srcID, region, acctID string, raw map[string]json.RawMessage, sets *sagemakerEdgeSets) error {
	var nc jobDefNetworkConfig
	if err := json.Unmarshal(raw["NetworkConfig"], &nc); err != nil || nc.VpcConfig == nil {
		return nil
	}
	if err := emitSubnetEdges(st, srcID, region, acctID, nc.VpcConfig.Subnets, sets); err != nil {
		return err
	}
	return emitSGEdges(st, srcID, region, acctID, nc.VpcConfig.SecurityGroupIDs, sets)
}

// emitJobDefInput emits the endpoint edge from a `*JobInput.EndpointInput`.
func emitJobDefInput(st *store.Store, srcID, region, acctID string, raw json.RawMessage, sets *sagemakerEdgeSets) error {
	if len(raw) == 0 {
		return nil
	}
	var ji jobDefJobInput
	if err := json.Unmarshal(raw, &ji); err != nil || ji.EndpointInput == nil {
		return nil
	}
	return emitEndpointByName(st, srcID, region, acctID, sv(ji.EndpointInput.EndpointName), sets)
}

// emitJobDefOutput emits S3 + KMS edges from a `*JobOutputConfig` block.
func emitJobDefOutput(st *store.Store, srcID, region, acctID string, raw json.RawMessage, sets *sagemakerEdgeSets) error {
	if len(raw) == 0 {
		return nil
	}
	var oc jobDefOutputConfig
	if err := json.Unmarshal(raw, &oc); err != nil {
		return nil
	}
	if err := emitKMS(st, srcID, sv(oc.KmsKeyID), region, acctID, sets); err != nil {
		return err
	}
	for _, mo := range oc.MonitoringOutputs {
		if mo.S3Output == nil {
			continue
		}
		if err := emitS3Bucket(st, srcID, sv(mo.S3Output.S3Uri), acctID, sets); err != nil {
			return err
		}
	}
	return nil
}

// resolveSageMakerProcessingJob walks IAM role, VPC subnet/SG, KMS, S3 edges
// from each processing job. Output S3 lives under `ProcessingOutputConfig`.
func resolveSageMakerProcessingJob(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerProcessingJob)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st,
		TypeIAMRole, TypeKMSKey, TypeS3Bucket, TypeEC2Subnet, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			RoleArn       *string `json:"RoleArn"`
			NetworkConfig *struct {
				VpcConfig *struct {
					Subnets          []string `json:"Subnets"`
					SecurityGroupIDs []string `json:"SecurityGroupIds"`
				} `json:"VpcConfig"`
			} `json:"NetworkConfig"`
			ProcessingOutputConfig *struct {
				KmsKeyID *string `json:"KmsKeyId"`
				Outputs  []struct {
					S3Output *struct {
						S3Uri *string `json:"S3Uri"`
					} `json:"S3Output"`
				} `json:"Outputs"`
			} `json:"ProcessingOutputConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitIAMRole(st, r.ID, sv(a.RoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("processing-job→role: %w", err)
		}
		if a.NetworkConfig != nil && a.NetworkConfig.VpcConfig != nil {
			vc := a.NetworkConfig.VpcConfig
			if err := emitSubnetEdges(st, r.ID, region, acct.ID, vc.Subnets, sets); err != nil {
				return err
			}
			if err := emitSGEdges(st, r.ID, region, acct.ID, vc.SecurityGroupIDs, sets); err != nil {
				return err
			}
		}
		if a.ProcessingOutputConfig != nil {
			if err := emitKMS(st, r.ID, sv(a.ProcessingOutputConfig.KmsKeyID), region, acct.ID, sets); err != nil {
				return err
			}
			for _, o := range a.ProcessingOutputConfig.Outputs {
				if o.S3Output == nil {
					continue
				}
				if err := emitS3Bucket(st, r.ID, sv(o.S3Output.S3Uri), acct.ID, sets); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// resolveSageMakerPipeline links a pipeline to its execution role.
func resolveSageMakerPipeline(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerPipeline)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitIAMRole(st, r.ID, sv(a.RoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("pipeline→role: %w", err)
		}
	}
	return nil
}

// resolveSageMakerProject is a no-op — its edge-bearing fields point at
// service-catalog products which have no dedicated disco scanner.
func resolveSageMakerProject(acct *account, st *store.Store) error {
	_ = acct
	_ = st
	return nil
}

// resolveSageMakerMlflowTrackingServer walks the IAM role + S3 artifact-store
// edges from each MLflow tracking-server row.
func resolveSageMakerMlflowTrackingServer(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerMlflowTrackingServer)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeIAMRole, TypeS3Bucket)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			RoleArn          *string `json:"RoleArn"`
			ArtifactStoreURI *string `json:"ArtifactStoreUri"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emitIAMRole(st, r.ID, sv(a.RoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("mlflow→role: %w", err)
		}
		if err := emitS3Bucket(st, r.ID, sv(a.ArtifactStoreURI), acct.ID, sets); err != nil {
			return fmt.Errorf("mlflow→s3: %w", err)
		}
	}
	return nil
}

// resolveSageMakerPartnerApp walks partner-app → IAM execution role + KMS edges.
func resolveSageMakerPartnerApp(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerPartnerApp)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeIAMRole, TypeKMSKey)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			ExecutionRoleArn *string `json:"ExecutionRoleArn"`
			KmsKeyID         *string `json:"KmsKeyId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitIAMRole(st, r.ID, sv(a.ExecutionRoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("partner-app→role: %w", err)
		}
		if err := emitKMS(st, r.ID, sv(a.KmsKeyID), region, acct.ID, sets); err != nil {
			return fmt.Errorf("partner-app→kms: %w", err)
		}
	}
	return nil
}

// resolveSageMakerCluster walks HyperPod cluster → execution role, VPC subnets,
// security groups. Cluster's role lives under `ClusterRole`, networking under
// the top-level `VpcConfig`.
func resolveSageMakerCluster(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerCluster)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeIAMRole, TypeEC2Subnet, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			ClusterRole *string `json:"ClusterRole"`
			VpcConfig   *struct {
				Subnets          []string `json:"Subnets"`
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
			} `json:"VpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitIAMRole(st, r.ID, sv(a.ClusterRole), acct.ID, sets); err != nil {
			return fmt.Errorf("cluster→role: %w", err)
		}
		if a.VpcConfig != nil {
			if err := emitSubnetEdges(st, r.ID, region, acct.ID, a.VpcConfig.Subnets, sets); err != nil {
				return err
			}
			if err := emitSGEdges(st, r.ID, region, acct.ID, a.VpcConfig.SecurityGroupIDs, sets); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveSageMakerDeviceFleet walks device-fleet → IAM role, S3 output bucket,
// KMS key (output config encryption).
func resolveSageMakerDeviceFleet(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerDeviceFleet)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeIAMRole, TypeS3Bucket, TypeKMSKey)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			RoleArn      *string `json:"RoleArn"`
			OutputConfig *struct {
				S3OutputLocation *string `json:"S3OutputLocation"`
				KmsKeyID         *string `json:"KmsKeyId"`
			} `json:"OutputConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if err := emitIAMRole(st, r.ID, sv(a.RoleArn), acct.ID, sets); err != nil {
			return fmt.Errorf("device-fleet→role: %w", err)
		}
		if a.OutputConfig != nil {
			if err := emitS3Bucket(st, r.ID, sv(a.OutputConfig.S3OutputLocation), acct.ID, sets); err != nil {
				return err
			}
			if err := emitKMS(st, r.ID, sv(a.OutputConfig.KmsKeyID), region, acct.ID, sets); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveSageMakerDevice links each device to its parent fleet via DeviceFleetName.
func resolveSageMakerDevice(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerDevice)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeSageMakerDeviceFleet)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			DeviceFleetName *string `json:"DeviceFleetName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		name := sv(a.DeviceFleetName)
		if name == "" {
			continue
		}
		region := sv(r.Region)
		fID := store.ResourceID("aws", acct.ID, TypeSageMakerDeviceFleet,
			sagemakerARN(region, acct.ID, "device-fleet", name))
		if !sets.fleets[fID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, fID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("device→fleet: %w", err)
		}
	}
	return nil
}

// resolveSageMakerInferenceComponent links a component to the endpoint that
// hosts it; the EndpointArn is supplied directly by the SDK.
func resolveSageMakerInferenceComponent(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerInferenceComponent)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeSageMakerEndpoint)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			EndpointArn *string `json:"EndpointArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		arn := sv(a.EndpointArn)
		if arn == "" {
			continue
		}
		epID := store.ResourceID("aws", acct.ID, TypeSageMakerEndpoint, arn)
		if !sets.endpts[epID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, epID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("inference-component→endpoint: %w", err)
		}
	}
	return nil
}

// resolveSageMakerInferenceExperiment links each experiment to the hosting
// endpoint, the model variants under test, and the storage KMS key.
func resolveSageMakerInferenceExperiment(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerInferenceExperiment)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeSageMakerEndpoint, TypeSageMakerModel, TypeKMSKey)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			EndpointMetadata *struct {
				EndpointName *string `json:"EndpointName"`
			} `json:"EndpointMetadata"`
			ModelVariants []struct {
				ModelName *string `json:"ModelName"`
			} `json:"ModelVariants"`
			KmsKey *string `json:"KmsKey"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		region := sv(r.Region)
		if a.EndpointMetadata != nil {
			if err := emitEndpointByName(st, r.ID, region, acct.ID, sv(a.EndpointMetadata.EndpointName), sets); err != nil {
				return err
			}
		}
		for _, v := range a.ModelVariants {
			name := sv(v.ModelName)
			if name == "" {
				continue
			}
			mID := store.ResourceID("aws", acct.ID, TypeSageMakerModel,
				sagemakerModelARN(region, acct.ID, name))
			if !sets.models[mID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, mID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("inference-experiment→model: %w", err)
			}
		}
		if err := emitKMS(st, r.ID, sv(a.KmsKey), region, acct.ID, sets); err != nil {
			return err
		}
	}
	return nil
}

// resolveSageMakerModelCard walks the security-config KMS edge.
func resolveSageMakerModelCard(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerModelCard)
	if err != nil {
		return err
	}
	sets, err := loadSagemakerEdgeSets(acct, st, TypeKMSKey)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			SecurityConfig *struct {
				KmsKeyID *string `json:"KmsKeyId"`
			} `json:"SecurityConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.SecurityConfig == nil {
			continue
		}
		if err := emitKMS(st, r.ID, sv(a.SecurityConfig.KmsKeyID), sv(r.Region), acct.ID, sets); err != nil {
			return fmt.Errorf("model-card→kms: %w", err)
		}
	}
	return nil
}
