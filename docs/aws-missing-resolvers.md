# AWS missing resolvers

Resolver-layer coverage gaps. Pair with `aws-missing-services.md` for the
scanner-layer skip list.

Two gap signals live here:

- **Audit candidates** — `(source_type → target_type)` pairs where a source's
  `AttributesJSON` carries an ARN/ID reference to a scanned target type but no
  edge exists between them in `relationships`.
- **Orphan types** — disco types whose scanned rows produce zero outbound
  resolver edges across a representative scan. Worklist for new resolver work.

## Regenerate

```bash
# 1. Populate disco.db with a representative scan (broad region coverage).
./disco scan aws --regions us-east-1,us-west-2,eu-west-1

# 2. Audit pairs (source → target with ref but no edge):
go run ./cmd/aws-resolver-audit --db ~/.local/share/disco/disco.db --top 100

# 3. Orphan types (types with no outbound edges):
disco coverage --missing-resolvers --provider aws
```

Audit tool source: `cmd/aws-resolver-audit/main.go`. Output is TSV; pipe to
`column -t -s$'\t'` for human reading. The orphan list at the bottom of this
file is the captured snapshot of `disco coverage --missing-resolvers`; refresh
it after each resolver-shipping commit so future PRs diff against it.

## Validation workflow

Each candidate pair below needs three checks before it counts as a real gap:

1. **Resolver search.** `grep -nE "Type<Source>.*Type<Target>|Type<Target>.*Type<Source>" internal/providers/aws/*resolvers*.go` — if a resolver names both types, the gap is likely a sample artifact (sampled rows happened to have nil refs, or target unscanned in this account).
2. **Skip-doc check.** `grep -i "<source>.*<target>" docs/aws-missing-services.md` — pair may be intentionally deferred at the scanner layer.
3. **Synthetic-stub check.** Target type with `Synthetic: true` may already be covered via the cross-account / foreign-resource resolver pattern documented in `internal/providers/CLAUDE.md`.

Pairs surviving all three are implementable resolver work.

## Implementation precedents

When implementing a confirmed gap, start from the closest existing resolver:

| Target service | Helper / pattern                                            | Precedent file                              |
|----------------|--------------------------------------------------------------|---------------------------------------------|
| KMS            | `loadKMSResolveIndex` + `idx.resolveKMSKeyID`               | `internal/providers/aws/kms_helpers.go`     |
| IAM (any)      | `scannedIDSet` + `scannedIDsByRegion`                       | `internal/providers/aws/securityhub_resolvers.go` |
| EC2 IDs        | `loadSecurityGroupIndex`-style id-set lookup                | `internal/providers/aws/ec2_*_resolvers.go` |
| ECR via image  | `apprunnerImageToRepoARN`                                   | `internal/providers/aws/apprunner_resolvers.go` |
| Cross-acct     | Synthetic stub upsert pre-edge                              | `internal/providers/aws/iam_resolvers.go::resolveIAMRoleCrossAccountTrust` |

## Audit candidates

Last refresh: 2026-05-04 against the same DB after the gap-grind session
below. Audit binary returns zero candidates after the three opened-and-
closed pairs were shipped; re-run after the next broad scan to surface
new gaps as scanner coverage grows.

### Closed (this session)

| Source | Target | Resolution |
|--------|--------|------------|
| `aws:iam:service-linked-role` → `aws:iam:role` | audit hardening: skip refs equal to source NativeID (Phase A self-ARN suppression) |
| `aws:iam:role` → `aws:iam:policy` | audit hardening: direction-blind diff — `policy → role` already emitted by `resolveManagedPolicyAttachments` |
| `aws:kms:grant` → `aws:kms:key` | audit hardening: direction-blind diff — `key → grant` hierarchy `contains` exists |
| `aws:iam:instance-profile` → `aws:iam:role` | covered by `resolveInstanceProfileRoles`; surfaced as sample artifact, cleared on re-scan |
| `aws:ec2:route-table` → IGW/NAT/TGW/peering/VPCE/instance | `resolveRouteTableRoutes` (`ec2_networking_resolvers.go`) walks `Routes[]` and dispatches per target field |
| `aws:ec2:security-group` → `aws:ec2:vpc` | `resolveSecurityGroupVPC` (`ec2_networking_resolvers.go`) reads `VpcId` |
| `aws:route53resolver:resolver-config` → `aws:ec2:vpc` | `resolveR53RResolverConfigVPC` (`route53resolver_resolvers.go`) — new file |
| `aws:route53resolver:resolver-rule-association` → `aws:ec2:vpc` + `:resolver-rule` | `resolveR53RResolverRuleAssoc` (`route53resolver_resolvers.go`) |
| `aws:ec2:network-acl` → `aws:ec2:subnet` | `resolveNetworkACLRelationships` extended to walk `Associations[].SubnetId` |
| `aws:ec2:route-table` → `aws:ec2:subnet` | `resolveRouteTableRelationships` extended to walk `Associations[].SubnetId` |
| `aws:kms:grant` → `aws:ec2:volume` | `resolveKMSGrantEncryptionContext` reads `Constraints.EncryptionContextSubset["aws:ebs:id"]` (bare volume ID, not ARN) |
| `aws:scheduler:schedule` → lambda/sqs/sns/sfn/kinesis/firehose/api-destination | new `scheduler_resolvers.go` reuses `eventBridgeTargetType` substring dispatch on `Target.Arn` |
| `aws:route53resolver:resolver-rule` → `:resolver-endpoint` | `resolveR53RResolverRuleEndpoint` reads `ResolverEndpointId` |
| `aws:route53resolver:resolver-query-logging-config` → S3/log-group/firehose | `resolveR53RQueryLogConfigDestination` substring-dispatches `DestinationArn` |
| `aws:sso:application` → `aws:sso:instance` | `resolveSSOApplicationInstance` reads `InstanceArn` |
| `aws:sso:application-assignment` → `:application` + identity-store user/group | `resolveSSOApplicationAssignmentRefs` parent-extracts `/assignment/` and resolves principal via parent app's `IdentityStoreId` |
| `aws:sso:instance-access-control-attribute-configuration` → `:instance` | `resolveSSOAttrConfigInstance` parent-extracts NativeID suffix |
| `aws:glue:database` → `:catalog` + `:connection` | `resolveGlueDatabaseTargets` reads `TargetDatabase.CatalogId` and `FederatedDatabase.ConnectionName` |
| `aws:glue:integration` → KMS / RDS / Redshift / Kinesis / DynamoDB / S3 | `resolveGlueIntegrationRefs` substring-dispatches `SourceArn` + `TargetArn`, KMS via `loadKMSResolveIndex` |
| `aws:glue:catalog` → `aws:redshift:cluster` | `resolveGlueCatalogTargets` reads `TargetRedshiftCatalog.CatalogArn` |
| `aws:ivs:stage` → `aws:ivs:storage-configuration` | `resolveIVSStageStorageConfig` reads `AutoParticipantRecordingConfiguration.StorageConfigurationArn` |

### Bonus shipped alongside r53r work

| Source | Target | Resolver |
|--------|--------|----------|
| `aws:route53resolver:firewall-rule-group-association` → `aws:ec2:vpc` | `resolveR53RFirewallRuleGroupAssoc` |

### Phase 2 leaf-flag harvest (this session)

The orphan inventory shrank from 453 to 227 across the same session by
flagging 200+ types into `internal/providers/aws/coverage_leaves.go`.
Each cluster represents either a no-out-edges singleton (IAM auth
artefacts, cost-domain rows, Pinpoint v2 templates), a deprecated /
preview-stage SDK (route53globalresolver, security-agent, dev-ops-agent,
nova-act), or a List-only summary type pending Tier C1 Describe
enrichment (SES Mail Manager, SageMaker leftovers, Workspaces Web
portal children, MediaConnect, Location, Timestream, SSM document /
maintenance-window / patch-baseline, Logs leftovers, Route53 recovery
suite + profiles, Lake Formation, Kinesis Analytics, Imagebuilder
catalog, IoT-* family).

## Orphan types (no outbound resolver edges)

Captured snapshot from `disco coverage --missing-resolvers --provider aws`.
Tab-separated so the regenerate command can overwrite the block in place
(replace everything between the fence markers below).

```tsv
disco_type	service
aws:appflow:connector-profile	appflow
aws:backup:plan	backup
aws:batch:consumable-resource	batch
aws:batch:scheduling-policy	batch
aws:batch:service-environment	batch
aws:chime:app-instance	chime
aws:cleanrooms-ml:configured-model-algorithm	cleanrooms-ml
aws:cleanrooms-ml:configured-model-algorithm-association	cleanrooms-ml
aws:cleanrooms-ml:training-dataset	cleanrooms-ml
aws:cleanrooms:collaboration	cleanrooms
aws:cleanrooms:configured-table	cleanrooms
aws:cloud9:environment-ec2	cloud9
aws:cloudfront:continuous-deployment-policy	cloudfront
aws:cloudfront:function	cloudfront
aws:cloudfront:vpc-origin	cloudfront
aws:cloudtrail:channel	cloudtrail
aws:cloudtrail:dashboard	cloudtrail
aws:code-build:fleet	code-build
aws:code-build:project	code-build
aws:code-build:report-group	code-build
aws:code-build:source-credential	code-build
aws:code-guru-profiler:profiling-group	code-guru-profiler
aws:code-guru-reviewer:repository-association	code-guru-reviewer
aws:codecommit:repository	codecommit
aws:codedeploy:application	codedeploy
aws:codedeploy:deployment-config	codedeploy
aws:codepipeline:custom-action-type	codepipeline
aws:codepipeline:pipeline	codepipeline
aws:codestar-connections:connection	codestar-connections
aws:cognito:user-pool	cognito
aws:config:organization-conformance-pack	config
aws:config:stored-query	config
aws:connect-campaigns-v2:campaign	connect-campaigns-v2
aws:connect-campaigns:campaign	connect-campaigns
aws:connect:instance	connect
aws:controltower:enabled-control	controltower
aws:customer-profiles:domain	customer-profiles
aws:databrew:dataset	databrew
aws:databrew:ruleset	databrew
aws:datasync:agent	datasync
aws:datasync:task	datasync
aws:datazone:domain	datazone
aws:devops-guru:log-anomaly-detection-integration	devops-guru
aws:devops-guru:notification-channel	devops-guru
aws:devops-guru:resource-collection	devops-guru
aws:directconnect:direct-connect-gateway	directconnect
aws:directconnect:lag	directconnect
aws:doc-db-elastic:cluster	doc-db-elastic
aws:dsql:cluster	dsql
aws:ec2:ipam	ec2
aws:ec2:ipam-prefix-list-resolver	ec2
aws:ec2:ipam-resource-discovery	ec2
aws:ec2:launch-template	ec2
aws:ec2:local-gateway-route-table	ec2
aws:ec2:local-gateway-virtual-interface-group	ec2
aws:ec2:transit-gateway	ec2
aws:ec2:vpc-endpoint-service	ec2
aws:ecs:cluster	ecs
aws:efs:access-point	efs
aws:elasticloadbalancingv2:trust-store	elasticloadbalancingv2
aws:elemental-inference:feed	elemental-inference
aws:emr-containers:security-configuration	emr-containers
aws:emr:cluster	emr
aws:emr:security-configuration	emr
aws:emr:studio	emr
aws:entityresolution:id-mapping-workflow	entityresolution
aws:entityresolution:id-namespace	entityresolution
aws:entityresolution:matching-workflow	entityresolution
aws:entityresolution:schema-mapping	entityresolution
aws:events:connection	events
aws:events:event-bus	events
aws:evs:environment	evs
aws:fin-space:environment	fin-space
aws:forecast:dataset	forecast
aws:forecast:dataset-group	forecast
aws:fsx:file-system	fsx
aws:fsx:s3-access-point-attachment	fsx
aws:global-accelerator:accelerator	global-accelerator
aws:global-accelerator:cross-account-attachment	global-accelerator
aws:global-accelerator:endpoint-group	global-accelerator
aws:global-accelerator:listener	global-accelerator
aws:grafana:workspace	grafana
aws:guardduty:detector	guardduty
aws:guardduty:malware-protection-plan	guardduty
aws:health-imaging:datastore	health-imaging
aws:health-lake:fhir-datastore	health-lake
aws:iam:foreign-account	iam
aws:iam:instance-profile	iam
aws:identitystore:group	identitystore
aws:identitystore:group-membership	identitystore
aws:identitystore:user	identitystore
aws:inspector:resource-group	inspector
aws:interconnect:connection	interconnect
aws:internet-monitor:monitor	internet-monitor
aws:ivs-chat:logging-configuration	ivs-chat
aws:ivs-chat:room	ivs-chat
aws:kendra-ranking:execution-plan	kendra-ranking
aws:kendra:index	kendra
aws:launch-wizard:deployment	launch-wizard
aws:lex:bot	lex
aws:license-manager:license	license-manager
aws:lightsail:bucket	lightsail
aws:lightsail:container-service	lightsail
aws:lightsail:database	lightsail
aws:lightsail:domain	lightsail
aws:lookout-equipment:inference-scheduler	lookout-equipment
aws:m2:application	m2
aws:m2:environment	m2
aws:managed-blockchain:accessor	managed-blockchain
aws:managed-blockchain:member	managed-blockchain
aws:managed-blockchain:node	managed-blockchain
aws:media-convert:job-template	media-convert
aws:media-convert:preset	media-convert
aws:media-convert:queue	media-convert
aws:media-store:container	media-store
aws:medialive:cloudwatch-alarm-template-group	medialive
aws:medialive:eventbridge-rule-template-group	medialive
aws:medialive:multiplex	medialive
aws:medialive:signal-map	medialive
aws:mediapackage:channel	mediapackage
aws:mediapackage:packaging-group	mediapackage
aws:mediapackagev2:channel-group	mediapackagev2
aws:mediatailor:channel	mediatailor
aws:mediatailor:playback-configuration	mediatailor
aws:mediatailor:source-location	mediatailor
aws:mpa:approval-team	mpa
aws:mpa:identity-source	mpa
aws:mq:configuration	mq
aws:mwaa-serverless:workflow	mwaa-serverless
aws:mwaa:environment	mwaa
aws:neptune-graph:graph	neptune-graph
aws:network-firewall:rule-group	network-firewall
aws:network-firewall:tls-inspection-configuration	network-firewall
aws:networkmanager:global-network	networkmanager
aws:oam:link	oam
aws:oam:sink	oam
aws:odb:cloud-autonomous-vm-cluster	odb
aws:odb:cloud-exadata-infrastructure	odb
aws:odb:cloud-vm-cluster	odb
aws:odb:odb-network	odb
aws:odb:odb-peering-connection	odb
aws:omics:configuration	omics
aws:omics:run-group	omics
aws:omics:workflow	omics
aws:opensearchservice:application	opensearchservice
aws:organizations:account	organizations
aws:organizations:ou	organizations
aws:organizations:resource-policy	organizations
aws:osis:pipeline	osis
aws:payment-cryptography:key	payment-cryptography
aws:pca-connector-ad:service-principal-name	pca-connector-ad
aws:pca-connector-ad:template	pca-connector-ad
aws:pca-connector-ad:template-group-access-control-entry	pca-connector-ad
aws:pca-connector-scep:challenge	pca-connector-scep
aws:pca-connector-scep:connector	pca-connector-scep
aws:pcs:cluster	pcs
aws:pcs:compute-node-group	pcs
aws:pcs:queue	pcs
aws:pinpoint:app	pinpoint
aws:pipes:pipe	pipes
aws:qbusiness:application	qbusiness
aws:quicksight:action-connector	quicksight
aws:quicksight:analysis	quicksight
aws:quicksight:dashboard	quicksight
aws:quicksight:data-set	quicksight
aws:quicksight:data-source	quicksight
aws:quicksight:folder	quicksight
aws:rds:integration	rds
aws:refactor-spaces:environment	refactor-spaces
aws:resource-groups:group	resource-groups
aws:roles-anywhere:trust-anchor	roles-anywhere
aws:rtbfabric:link	rtbfabric
aws:rtbfabric:requester-gateway	rtbfabric
aws:rtbfabric:responder-gateway	rtbfabric
aws:rum:app-monitor	rum
aws:s3-object-lambda:access-point	s3-object-lambda
aws:s3-object-lambda:access-point-policy	s3-object-lambda
aws:s3:access-grants-instance	s3
aws:s3:multi-region-access-point	s3
aws:s3:storage-lens-group	s3
aws:s3express:access-point	s3express
aws:s3files:access-point	s3files
aws:s3files:file-system	s3files
aws:s3files:file-system-policy	s3files
aws:s3files:mount-target	s3files
aws:s3outposts:bucket-policy	s3outposts
aws:s3vectors:index	s3vectors
aws:s3vectors:vector-bucket	s3vectors
aws:s3vectors:vector-bucket-policy	s3vectors
aws:scheduler:schedule-group	scheduler
aws:security-lake:aws-log-source	security-lake
aws:security-lake:data-lake	security-lake
aws:security-lake:subscriber	security-lake
aws:service-catalog-app-registry:application	service-catalog-app-registry
aws:service-catalog-app-registry:attribute-group	service-catalog-app-registry
aws:service-catalog-app-registry:attribute-group-association	service-catalog-app-registry
aws:service-catalog-app-registry:resource-association	service-catalog-app-registry
aws:servicediscovery:http-namespace	servicediscovery
aws:servicediscovery:instance	servicediscovery
aws:servicediscovery:private-dns-namespace	servicediscovery
aws:servicediscovery:public-dns-namespace	servicediscovery
aws:servicediscovery:service	servicediscovery
aws:sfn:activity	sfn
aws:shield:drt-access	shield
aws:shield:proactive-engagement	shield
aws:signer:signing-profile	signer
aws:sim-space-weaver:simulation	sim-space-weaver
aws:sso:permission-set	sso
aws:synthetics:canary	synthetics
aws:synthetics:group	synthetics
aws:systems-manager-sap:application	systems-manager-sap
aws:verifiedpermissions:policy-store	verifiedpermissions
aws:voice-id:domain	voice-id
aws:vpclattice:domain-verification	vpclattice
aws:vpclattice:service	vpclattice
aws:vpclattice:service-network	vpclattice
aws:wafv2:ip-set	wafv2
aws:wafv2:regex-pattern-set	wafv2
aws:wafv2:rule-group	wafv2
aws:work-spaces:connection-alias	work-spaces
aws:work-spaces:workspace	work-spaces
aws:work-spaces:workspaces-pool	work-spaces
aws:workspaces-instances:workspace-instance	workspaces-instances
aws:workspaces-thin-client:environment	workspaces-thin-client
aws:xray:group	xray
aws:xray:resource-policy	xray
aws:xray:sampling-rule	xray
```

## Limitations

- **Sample frequency depends on scan breadth.** A pair surfacing once may be common in production accounts; broaden scan coverage (more regions, more services enabled) before dismissing low-freq rows.
- **ARN classification is heuristic.** `cmd/aws-resolver-audit/main.go::arnKindSuffix` ships a generic mapping; service-specific kind segments (e.g. SFN `stateMachine`, CloudWatch `alarm`, etc.) need entries when their pairs surface as `""` target types. Extend rather than rewrite.
- **Bare-ID detection is ARN-prefix-based.** Resources keyed by name only (DynamoDB tables, IAM roles when referenced by name) will not surface as gaps via Signal B — those pairs need ARN form in the source attrs to be detected.
- **Self-edges suppressed.** `(typeX → typeX)` pairs are dropped — peering, parent-child within same type, etc. Existing resolvers handle these.
