## AWS

**Coverage:** 49.8% (1660/3336 listable) · depth0 64.3% · depth1 27.9% · depth2 21.8% · depth3 3.1% · depth4 0.0% · attribute 1625 · excluded 439 · disco-only 0 (0 unexplained)

Pins: aws-sdk-go-v2@release-2026-09-15, service-reference@1396d710a7d5

The denominator is every candidate the provider's own SDK can list that the extractor classified `resource` — not every API operation, and not a curated list. Attributes (detail reads), catalogs (provider-published, read-only) and non-resources are outside it and are listed below. Each row names the rule that classified it; the table below gives the coverage of each rule, so a rule that admits rows no scanner can close is visible as a low percentage rather than as a smaller number.

| Admitting rule | Covered | Uncovered | % |
|---|---|---|---|
| sr-resource | 987 | 234 | 80.8 |
| child-uncatalogued | 121 | 896 | 11.9 |
| smithy-resource | 386 | 176 | 68.7 |
| element-written | 140 | 276 | 33.7 |
| element-arn | 23 | 83 | 21.7 |
| element-created | 3 | 11 | 21.4 |

| Service | Covered | Uncovered | % |
|---|---|---|---|
| ec2 | 107 | 89 | 54.6 |
| resiliencehub | 4 | 36 | 10.0 |
| sagemaker | 51 | 36 | 58.6 |
| connect | 35 | 33 | 51.5 |
| ssm | 9 | 29 | 23.7 |
| iotsitewise | 9 | 28 | 24.3 |
| config | 10 | 27 | 27.0 |
| datazone | 13 | 23 | 36.1 |
| bedrock | 22 | 22 | 50.0 |
| quicksight | 22 | 22 | 50.0 |
| bedrock-agentcore | 25 | 21 | 54.3 |
| glue | 22 | 20 | 52.4 |
| lex | 4 | 20 | 16.7 |
| wellarchitected | 4 | 20 | 16.7 |
| chime | 11 | 18 | 37.9 |
| securityagent | 7 | 18 | 28.0 |
| iot | 32 | 17 | 65.3 |
| mgn | 8 | 17 | 32.0 |
| es | 4 | 16 | 20.0 |
| workmail | 1 | 16 | 5.9 |
| gamelift | 12 | 15 | 44.4 |
| rds | 24 | 15 | 61.5 |
| ses | 19 | 15 | 55.9 |
| cleanrooms | 9 | 14 | 39.1 |
| deadline | 13 | 14 | 48.1 |
| dms | 11 | 14 | 44.0 |
| inspector2 | 4 | 14 | 22.2 |
| cloudformation | 7 | 13 | 35.0 |
| comprehend | 4 | 13 | 23.5 |
| omics | 11 | 13 | 45.8 |
| profile | 12 | 13 | 48.0 |
| devicefarm | 7 | 12 | 36.8 |
| iotmanagedintegrations | 5 | 12 | 29.4 |
| partnercentral-selling | 0 | 12 | 0.0 |
| aws-marketplace | 0 | 11 | 0.0 |
| lambda | 10 | 11 | 47.6 |
| redshift | 16 | 11 | 59.3 |
| aidevops | 4 | 10 | 28.6 |
| clouddirectory | 2 | 10 | 16.7 |
| codecatalyst | 0 | 10 | 0.0 |
| codecommit | 1 | 10 | 9.1 |
| compute-optimizer | 0 | 10 | 0.0 |
| ds | 1 | 10 | 9.1 |
| imagebuilder | 9 | 10 | 47.4 |
| logs | 15 | 10 | 60.0 |
| qbusiness | 9 | 10 | 47.4 |
| s3 | 10 | 10 | 50.0 |
| storagegateway | 8 | 10 | 44.4 |
| auditmanager | 3 | 9 | 25.0 |
| backup | 10 | 9 | 52.6 |
| drs | 5 | 9 | 35.7 |
| iam | 14 | 9 | 60.9 |
| license-manager | 6 | 9 | 40.0 |
| sso | 8 | 9 | 47.1 |
| workspaces | 7 | 9 | 43.8 |
| athena | 5 | 8 | 38.5 |
| cleanrooms-ml | 7 | 8 | 46.7 |
| codebuild | 4 | 8 | 33.3 |
| devops-guru | 1 | 8 | 11.1 |
| guardduty | 9 | 8 | 52.9 |
| ivs | 11 | 8 | 57.9 |
| mobiletargeting | 3 | 8 | 27.3 |
| proton | 11 | 8 | 57.9 |
| securityhub | 9 | 8 | 52.9 |
| sms-voice | 9 | 8 | 52.9 |
| workdocs | 0 | 8 | 0.0 |
| appstream | 14 | 7 | 66.7 |
| bcm-pricing-calculator | 3 | 7 | 30.0 |
| cognito-idp | 6 | 7 | 46.2 |
| globalaccelerator | 4 | 7 | 36.4 |
| greengrass | 20 | 7 | 74.1 |
| groundstation | 3 | 7 | 30.0 |
| networkmanager | 13 | 7 | 65.0 |
| outposts | 2 | 7 | 22.2 |
| personalize | 10 | 7 | 58.8 |
| ram | 2 | 7 | 22.2 |
| redshift-serverless | 6 | 7 | 46.2 |
| servicecatalog | 13 | 7 | 65.0 |
| aco-automation | 0 | 6 | 0.0 |
| autoscaling | 6 | 6 | 50.0 |
| cloudtrail | 4 | 6 | 40.0 |
| eks | 9 | 6 | 60.0 |
| forecast | 8 | 6 | 57.1 |
| frauddetector | 10 | 6 | 62.5 |
| lookoutequipment | 5 | 6 | 45.5 |
| medialive | 17 | 6 | 73.9 |
| rekognition | 4 | 6 | 40.0 |
| ssm-contacts | 3 | 6 | 33.3 |
| wickr | 0 | 6 | 0.0 |
| access-analyzer | 2 | 5 | 28.6 |
| acm | 1 | 5 | 16.7 |
| applicationinsights | 1 | 5 | 16.7 |
| cases | 5 | 5 | 50.0 |
| ce | 4 | 5 | 44.4 |
| codepipeline | 3 | 5 | 37.5 |
| controlcatalog | 0 | 5 | 0.0 |
| directconnect | 5 | 5 | 50.0 |
| ecs | 9 | 5 | 64.3 |
| elasticmapreduce | 7 | 5 | 58.3 |
| evs | 1 | 5 | 16.7 |
| finspace-api | 0 | 5 | 0.0 |
| geo | 7 | 5 | 58.3 |
| health | 0 | 5 | 0.0 |
| iotfleetwise | 7 | 5 | 58.3 |
| iottwinmaker | 5 | 5 | 50.0 |
| iotwireless | 11 | 5 | 68.8 |
| kafka | 5 | 5 | 50.0 |
| kendra | 8 | 5 | 61.5 |
| lightsail | 16 | 5 | 76.2 |
| mgh | 1 | 5 | 16.7 |
| migrationhub-orchestrator | 2 | 5 | 28.6 |
| odb | 8 | 5 | 61.5 |
| snowball | 0 | 5 | 0.0 |
| ssm-incidents | 2 | 5 | 28.6 |
| trustedadvisor | 1 | 5 | 16.7 |
| voiceid | 1 | 5 | 16.7 |
| wisdom | 11 | 5 | 68.8 |
| apigateway | 25 | 4 | 86.2 |
| arc-region-switch | 1 | 4 | 20.0 |
| billingconductor | 4 | 4 | 50.0 |
| codeconnections | 0 | 4 | 0.0 |
| controltower | 3 | 4 | 42.9 |
| dataexchange | 3 | 4 | 42.9 |
| dynamodb | 4 | 4 | 50.0 |
| elasticbeanstalk | 4 | 4 | 50.0 |
| fis | 2 | 4 | 33.3 |
| healthlake | 1 | 4 | 20.0 |
| inspector | 0 | 4 | 0.0 |
| launchwizard | 1 | 4 | 20.0 |
| m2 | 3 | 4 | 42.9 |
| machinelearning | 0 | 4 | 0.0 |
| mpa | 2 | 4 | 33.3 |
| network-firewall | 7 | 4 | 63.6 |
| notifications | 7 | 4 | 63.6 |
| nova-act | 1 | 4 | 20.0 |
| partnercentral | 0 | 4 | 0.0 |
| partnercentral-account | 0 | 4 | 0.0 |
| redshift-data | 0 | 4 | 0.0 |
| social-messaging | 1 | 4 | 20.0 |
| ssm-sap | 3 | 4 | 42.9 |
| transcribe | 5 | 4 | 55.6 |
| xray | 3 | 4 | 42.9 |
| agent-registry | 0 | 3 | 0.0 |
| airflow-serverless | 1 | 3 | 25.0 |
| app-integrations | 3 | 3 | 50.0 |
| backup-search | 0 | 3 | 0.0 |
| braket | 1 | 3 | 25.0 |
| chatbot | 3 | 3 | 50.0 |
| cloudfront | 20 | 3 | 87.0 |
| codeartifact | 3 | 3 | 50.0 |
| codedeploy | 3 | 3 | 50.0 |
| codeguru-reviewer | 1 | 3 | 25.0 |
| cognito-sync | 0 | 3 | 0.0 |
| discovery | 0 | 3 | 0.0 |
| ds-data | 0 | 3 | 0.0 |
| emr-serverless | 1 | 3 | 25.0 |
| events | 8 | 3 | 72.7 |
| fms | 4 | 3 | 57.1 |
| gameliftstreams | 2 | 3 | 40.0 |
| grafana | 1 | 3 | 25.0 |
| internetmonitor | 1 | 3 | 25.0 |
| kinesisanalytics | 1 | 3 | 25.0 |
| macie2 | 5 | 3 | 62.5 |
| mediaconnect | 7 | 3 | 70.0 |
| medical-imaging | 1 | 3 | 25.0 |
| monitoring | 6 | 3 | 66.7 |
| mturk-requester | 0 | 3 | 0.0 |
| organizations | 8 | 3 | 72.7 |
| partnercentral-benefits | 0 | 3 | 0.0 |
| partnercentral-channel | 0 | 3 | 0.0 |
| route53 | 8 | 3 | 72.7 |
| route53-recovery-readiness | 4 | 3 | 57.1 |
| route53globalresolver | 7 | 3 | 70.0 |
| route53resolver | 12 | 3 | 80.0 |
| rum | 1 | 3 | 25.0 |
| serverlessrepo | 1 | 3 | 25.0 |
| sns | 2 | 3 | 40.0 |
| states | 4 | 3 | 57.1 |
| support | 0 | 3 | 0.0 |
| wafv2 | 6 | 3 | 66.7 |
| account-access | 0 | 2 | 0.0 |
| amplify | 5 | 2 | 71.4 |
| appconfig | 8 | 2 | 80.0 |
| application-signals | 2 | 2 | 50.0 |
| apprunner | 6 | 2 | 75.0 |
| arc-zonal-shift | 1 | 2 | 33.3 |
| artifact | 2 | 2 | 50.0 |
| backup-gateway | 3 | 2 | 60.0 |
| batch | 7 | 2 | 77.8 |
| bcm-data-exports | 1 | 2 | 33.3 |
| cloudcontrolapi | 0 | 2 | 0.0 |
| codeguru-security | 0 | 2 | 0.0 |
| datapipeline | 1 | 2 | 33.3 |
| detective | 3 | 2 | 60.0 |
| ecr | 3 | 2 | 60.0 |
| ecr-public | 1 | 2 | 33.3 |
| elasticache | 12 | 2 | 85.7 |
| elemental-inference | 1 | 2 | 33.3 |
| entityresolution | 4 | 2 | 66.7 |
| finspace | 8 | 2 | 80.0 |
| fsx | 8 | 2 | 80.0 |
| glacier | 1 | 2 | 33.3 |
| health-agent | 0 | 2 | 0.0 |
| invoicing | 1 | 2 | 33.3 |
| kinesis | 2 | 2 | 50.0 |
| kinesisvideo | 2 | 2 | 50.0 |
| kms | 4 | 2 | 66.7 |
| lakeformation | 3 | 2 | 60.0 |
| managedblockchain | 5 | 2 | 71.4 |
| mediastore | 0 | 2 | 0.0 |
| mediatailor | 7 | 2 | 77.8 |
| migrationhub-strategy | 0 | 2 | 0.0 |
| neptune-graph | 3 | 2 | 60.0 |
| networkflowmonitor | 2 | 2 | 50.0 |
| pi | 0 | 2 | 0.0 |
| qapps | 1 | 2 | 33.3 |
| resource-explorer-2 | 4 | 2 | 66.7 |
| resource-groups | 2 | 2 | 50.0 |
| sagemaker-geospatial | 1 | 2 | 33.3 |
| scn | 4 | 2 | 66.7 |
| secretsmanager | 1 | 2 | 33.3 |
| security-ir | 2 | 2 | 50.0 |
| servicequotas | 1 | 2 | 33.3 |
| shield | 3 | 2 | 60.0 |
| snow-device-management | 2 | 2 | 50.0 |
| sqs | 1 | 2 | 33.3 |
| supportauthz | 0 | 2 | 0.0 |
| synthetics | 2 | 2 | 50.0 |
| tax | 0 | 2 | 0.0 |
| transfer | 8 | 2 | 80.0 |
| vpc-lattice | 13 | 2 | 86.7 |
| account | 0 | 1 | 0.0 |
| amplifybackend | 0 | 1 | 0.0 |
| amplifyuibuilder | 3 | 1 | 75.0 |
| appfabric | 4 | 1 | 80.0 |
| appflow | 3 | 1 | 75.0 |
| application-autoscaling | 2 | 1 | 66.7 |
| application-cost-profiler | 0 | 1 | 0.0 |
| awsssoportal | 0 | 1 | 0.0 |
| billing | 1 | 1 | 50.0 |
| cassandra | 3 | 1 | 75.0 |
| cloud9 | 1 | 1 | 50.0 |
| cloudhsm | 2 | 1 | 66.7 |
| codeguru-profiler | 1 | 1 | 50.0 |
| cognito-identity | 1 | 1 | 50.0 |
| comprehendmedical | 0 | 1 | 0.0 |
| cost-optimization-hub | 0 | 1 | 0.0 |
| databrew | 6 | 1 | 85.7 |
| datasync | 4 | 1 | 80.0 |
| docdb-elastic | 2 | 1 | 66.7 |
| elasticfilesystem | 3 | 1 | 75.0 |
| elasticloadbalancing | 7 | 1 | 87.5 |
| emr-containers | 4 | 1 | 80.0 |
| execute-api | 0 | 1 | 0.0 |
| geo-places | 0 | 1 | 0.0 |
| interconnect | 2 | 1 | 66.7 |
| iot-jobs-data | 0 | 1 | 0.0 |
| iotdeviceadvisor | 1 | 1 | 50.0 |
| iotsecuredtunneling | 0 | 1 | 0.0 |
| kafkaconnect | 3 | 1 | 75.0 |
| mediaconvert | 3 | 1 | 75.0 |
| mediapackage | 2 | 1 | 66.7 |
| mediapackagev2 | 3 | 1 | 75.0 |
| memorydb | 9 | 1 | 90.0 |
| mq | 2 | 1 | 66.7 |
| oam | 2 | 1 | 66.7 |
| observabilityadmin | 4 | 1 | 80.0 |
| osis | 3 | 1 | 75.0 |
| polly | 1 | 1 | 50.0 |
| pricing | 0 | 1 | 0.0 |
| pricingplanmanager | 0 | 1 | 0.0 |
| refactor-spaces | 4 | 1 | 80.0 |
| repostspace | 1 | 1 | 50.0 |
| route53-recovery-cluster | 0 | 1 | 0.0 |
| route53-recovery-control-config | 4 | 1 | 80.0 |
| rtbfabric | 4 | 1 | 80.0 |
| s3vectors | 2 | 1 | 66.7 |
| schemas | 3 | 1 | 75.0 |
| sdb | 0 | 1 | 0.0 |
| securitylake | 2 | 1 | 66.7 |
| signer | 2 | 1 | 66.7 |
| ssm-quicksetup | 1 | 1 | 50.0 |
| swf | 1 | 1 | 50.0 |
| tagging | 0 | 1 | 0.0 |
| timestream | 3 | 1 | 75.0 |
| timestream-influxdb | 3 | 1 | 75.0 |
| translate | 2 | 1 | 66.7 |
| waf | 12 | 1 | 92.3 |
| waf-regional | 13 | 1 | 92.9 |
| workspaces-web | 10 | 1 | 90.9 |
| acm-pca | 2 | 0 | 100.0 |
| aiops | 1 | 0 | 100.0 |
| airflow | 1 | 0 | 100.0 |
| aoss | 7 | 0 | 100.0 |
| appmesh | 7 | 0 | 100.0 |
| appsync | 10 | 0 | 100.0 |
| aps | 4 | 0 | 100.0 |
| autoscaling-plans | 1 | 0 | 100.0 |
| b2bi | 4 | 0 | 100.0 |
| bcm-dashboards | 2 | 0 | 100.0 |
| budgets | 2 | 0 | 100.0 |
| cloudsearch | 1 | 0 | 100.0 |
| codestar-connections | 4 | 0 | 100.0 |
| codestar-notifications | 1 | 0 | 100.0 |
| connect-campaigns | 1 | 0 | 100.0 |
| cur | 1 | 0 | 100.0 |
| dax | 3 | 0 | 100.0 |
| dlm | 1 | 0 | 100.0 |
| dsql | 2 | 0 | 100.0 |
| firehose | 1 | 0 | 100.0 |
| identitystore | 3 | 0 | 100.0 |
| ivschat | 2 | 0 | 100.0 |
| kendra-ranking | 1 | 0 | 100.0 |
| license-manager-linux-subscriptions | 1 | 0 | 100.0 |
| license-manager-user-subscriptions | 5 | 0 | 100.0 |
| mediapackage-vod | 3 | 0 | 100.0 |
| networkmonitor | 1 | 0 | 100.0 |
| notifications-contacts | 1 | 0 | 100.0 |
| payment-cryptography | 2 | 0 | 100.0 |
| pca-connector-ad | 5 | 0 | 100.0 |
| pca-connector-scep | 2 | 0 | 100.0 |
| pcs | 3 | 0 | 100.0 |
| pipes | 1 | 0 | 100.0 |
| rbin | 1 | 0 | 100.0 |
| rolesanywhere | 4 | 0 | 100.0 |
| route53profiles | 3 | 0 | 100.0 |
| s3-outposts | 2 | 0 | 100.0 |
| s3files | 3 | 0 | 100.0 |
| s3tables | 3 | 0 | 100.0 |
| savingsplans | 1 | 0 | 100.0 |
| scheduler | 2 | 0 | 100.0 |
| servicediscovery | 3 | 0 | 100.0 |
| supportapp | 1 | 0 | 100.0 |
| textract | 2 | 0 | 100.0 |
| thinclient | 3 | 0 | 100.0 |
| tnb | 5 | 0 | 100.0 |
| verifiedpermissions | 5 | 0 | 100.0 |
| workspaces-instances | 1 | 0 | 100.0 |

### Uncovered (listable, no scanner) (1676)

| Service | Key | Depth | Scope | Rule | Ops |
| --- | --- | --- | --- | --- | --- |
| access-analyzer | access-analyzer/accesspreview | 0 |  | child-uncatalogued | access-analyzer:GetAccessPreview, access-analyzer:ListAccessPreviews |
| access-analyzer | access-analyzer/accesspreviewfinding | 1 |  | child-uncatalogued | access-analyzer:ListAccessPreviewFindings |
| access-analyzer | access-analyzer/analyzedresource | 1 |  | child-uncatalogued | access-analyzer:GetAnalyzedResource, access-analyzer:ListAnalyzedResources |
| access-analyzer | access-analyzer/finding | 1 |  | child-uncatalogued | access-analyzer:GetFinding, access-analyzer:GetFindingV2, access-analyzer:ListFindings, access-analyzer:ListFindingsV2 |
| access-analyzer | access-analyzer/policygeneration | 0 |  | element-arn | access-analyzer:ListPolicyGenerations |
| account | account/region | 1 |  | child-uncatalogued | account:ListRegions |
| account-access | account-access/application | 0 |  | smithy-resource | account-access:GetApplication, account-access:ListApplications |
| account-access | account-access/entitlement | 1 |  | child-uncatalogued | account-access:GetEntitlement, account-access:ListEntitlements |
| acm | acm/acmeaccount | 1 |  | child-uncatalogued | acm:DescribeAcmeAccount, acm:ListAcmeAccounts |
| acm | acm/acmedomainvalidation | 1 |  | sr-resource | acm:DescribeAcmeDomainValidation, acm:ListAcmeDomainValidations |
| acm | acm/acmeendpoint | 0 |  | sr-resource | acm:DescribeAcmeEndpoint, acm:ListAcmeEndpoints |
| acm | acm/acmeexternalaccountbinding | 1 |  | sr-resource | acm:DescribeAcmeExternalAccountBinding, acm:ListAcmeExternalAccountBindings |
| acm | acm/certificatedomainvalidation | 1 |  | child-uncatalogued | acm:ListCertificateDomainValidations |
| aco-automation | aco-automation/account | 0 |  | element-written | aco-automation:ListAccounts |
| aco-automation | aco-automation/automationevent | 0 |  | element-written | aco-automation:GetAutomationEvent, aco-automation:ListAutomationEvents |
| aco-automation | aco-automation/automationeventstep | 0 |  | child-uncatalogued | aco-automation:ListAutomationEventSteps |
| aco-automation | aco-automation/automationrule | 0 |  | sr-resource | aco-automation:GetAutomationRule, aco-automation:ListAutomationRules |
| aco-automation | aco-automation/automationrulepreview | 0 |  | element-written | aco-automation:ListAutomationRulePreview |
| aco-automation | aco-automation/recommendedaction | 0 |  | element-written | aco-automation:ListRecommendedActions |
| agent-registry | agent-registry/discoverableregistryrecord | 1 |  | child-uncatalogued | agent-registry:ListDiscoverableRegistryRecords |
| agent-registry | agent-registry/registry | 0 |  | smithy-resource | agent-registry:GetRegistry, agent-registry:ListRegistries |
| agent-registry | agent-registry/registryrecord | 1 |  | smithy-resource | agent-registry:GetRegistryRecord, agent-registry:ListRegistryRecords |
| aidevops | aidevops/asset | 1 |  | sr-resource | aidevops:GetAsset, aidevops:ListAssets |
| aidevops | aidevops/backlogtask | 1 |  | child-uncatalogued | aidevops:GetBacklogTask, aidevops:ListBacklogTasks |
| aidevops | aidevops/chat | 1 |  | child-uncatalogued | aidevops:ListChats |
| aidevops | aidevops/execution | 1 |  | child-uncatalogued | aidevops:ListExecutions |
| aidevops | aidevops/goal | 1 |  | child-uncatalogued | aidevops:ListGoals |
| aidevops | aidevops/journalrecord | 1 |  | child-uncatalogued | aidevops:ListJournalRecords |
| aidevops | aidevops/pendingmessage | 1 |  | child-uncatalogued | aidevops:ListPendingMessages |
| aidevops | aidevops/recommendation | 1 |  | child-uncatalogued | aidevops:GetRecommendation, aidevops:ListRecommendations |
| aidevops | aidevops/trigger | 1 |  | sr-resource | aidevops:GetTrigger, aidevops:ListTriggers |
| aidevops | aidevops/webhook | 2 |  | child-uncatalogued | aidevops:ListWebhooks |
| airflow-serverless | airflow-serverless/taskinstance | 0 |  | smithy-resource | airflow-serverless:GetTaskInstance, airflow-serverless:ListTaskInstances |
| airflow-serverless | airflow-serverless/workflowrun | 0 |  | smithy-resource | airflow-serverless:GetWorkflowRun, airflow-serverless:ListWorkflowRuns |
| airflow-serverless | airflow-serverless/workflowversion | 0 |  | smithy-resource | airflow-serverless:ListWorkflowVersions |
| amplify | amplify/artifact | 3 |  | child-uncatalogued | amplify:ListArtifacts |
| amplify | amplify/job | 2 |  | sr-resource | amplify:GetJob, amplify:ListJobs |
| amplifybackend | amplifybackend/backendjob | 2 |  | child-uncatalogued | amplifybackend:GetBackendJob, amplifybackend:ListBackendJobs |
| amplifyuibuilder | amplifyuibuilder/codegenjob | 0 |  | smithy-resource | amplifyuibuilder:GetCodegenJob, amplifyuibuilder:ListCodegenJobs |
| apigateway | apigateway/portal | 0 |  | sr-resource | apigateway:GetPortal, apigateway:ListPortals |
| apigateway | apigateway/portalproduct | 0 |  | sr-resource | apigateway:GetPortalProduct, apigateway:ListPortalProducts |
| apigateway | apigateway/productpage | 1 |  | sr-resource | apigateway:GetProductPage, apigateway:ListProductPages |
| apigateway | apigateway/productrestendpointpage | 1 |  | sr-resource | apigateway:GetProductRestEndpointPage, apigateway:ListProductRestEndpointPages |
| app-integrations | app-integrations/applicationassociation | 1 |  | sr-resource | app-integrations:ListApplicationAssociations |
| app-integrations | app-integrations/dataintegrationassociation | 1 |  | sr-resource | app-integrations:ListDataIntegrationAssociations |
| app-integrations | app-integrations/eventintegrationassociation | 1 |  | sr-resource | app-integrations:ListEventIntegrationAssociations |
| appconfig | appconfig/experimentdefinition | 0 |  | sr-resource | appconfig:GetExperimentDefinition, appconfig:ListExperimentDefinitions |
| appconfig | appconfig/experimentrun | 1 |  | sr-resource | appconfig:GetExperimentRun, appconfig:ListExperimentRuns |
| appfabric | appfabric/useraccesstask | 1 |  | child-uncatalogued | appfabric:BatchGetUserAccessTasks |
| appflow | appflow/flowexecutionrecord | 1 |  | child-uncatalogued | appflow:DescribeFlowExecutionRecords |
| application-autoscaling | application-autoscaling/scheduledaction | 0 |  | element-written | application-autoscaling:DescribeScheduledActions |
| application-cost-profiler | application-cost-profiler/reportdefinition | 0 |  | element-written | application-cost-profiler:GetReportDefinition, application-cost-profiler:ListReportDefinitions |
| application-signals | application-signals/getservicelevelobjectivebudgetreport | 1 |  | child-uncatalogued | application-signals:BatchGetServiceLevelObjectiveBudgetReport |
| application-signals | application-signals/instrumentationconfiguration | 0 |  | element-written | application-signals:GetInstrumentationConfiguration, application-signals:ListInstrumentationConfigurations |
| applicationinsights | applicationinsights/component | 0 |  | child-uncatalogued | applicationinsights:DescribeComponent, applicationinsights:ListComponents |
| applicationinsights | applicationinsights/configurationhistory | 0 |  | element-arn | applicationinsights:ListConfigurationHistory |
| applicationinsights | applicationinsights/logpattern | 0 |  | child-uncatalogued | applicationinsights:DescribeLogPattern, applicationinsights:ListLogPatterns |
| applicationinsights | applicationinsights/logpatternset | 0 |  | child-uncatalogued | applicationinsights:ListLogPatternSets |
| applicationinsights | applicationinsights/workload | 0 |  | child-uncatalogued | applicationinsights:DescribeWorkload, applicationinsights:ListWorkloads |
| apprunner | apprunner/customdomain | 1 |  | child-uncatalogued | apprunner:DescribeCustomDomains |
| apprunner | apprunner/operation | 1 |  | child-uncatalogued | apprunner:ListOperations |
| appstream | appstream/appblockbuilderappblockassociation | 0 |  | element-written | appstream:DescribeAppBlockBuilderAppBlockAssociations |
| appstream | appstream/applicenseusage | 0 |  | element-arn | appstream:DescribeAppLicenseUsage |
| appstream | appstream/associatedstack | 1 |  | child-uncatalogued | appstream:ListAssociatedStacks |
| appstream | appstream/exportimagetask | 0 |  | element-written | appstream:GetExportImageTask, appstream:ListExportImageTasks |
| appstream | appstream/imagepermission | 1 |  | child-uncatalogued | appstream:DescribeImagePermissions |
| appstream | appstream/session | 1 |  | child-uncatalogued | appstream:DescribeSessions |
| appstream | appstream/softwareassociation | 1 |  | child-uncatalogued | appstream:DescribeSoftwareAssociations |
| arc-region-switch | arc-region-switch/planevaluationstatus | 1 |  | child-uncatalogued | arc-region-switch:GetPlanEvaluationStatus |
| arc-region-switch | arc-region-switch/planexecution | 1 |  | child-uncatalogued | arc-region-switch:GetPlanExecution, arc-region-switch:ListPlanExecutions |
| arc-region-switch | arc-region-switch/planexecutionevent | 1 |  | child-uncatalogued | arc-region-switch:ListPlanExecutionEvents |
| arc-region-switch | arc-region-switch/route53healthcheck | 1 |  | child-uncatalogued | arc-region-switch:ListRoute53HealthChecks, arc-region-switch:ListRoute53HealthChecksInRegion |
| arc-zonal-shift | arc-zonal-shift/autoshift | 0 |  | smithy-resource | arc-zonal-shift:ListAutoshifts |
| arc-zonal-shift | arc-zonal-shift/zonalshift | 0 |  | smithy-resource | arc-zonal-shift:ListZonalShifts |
| artifact | artifact/complianceinquiry | 0 |  | smithy-resource | artifact:ExportComplianceInquiry, artifact:ListComplianceInquiries |
| artifact | artifact/complianceinquiryquery | 1 |  | child-uncatalogued | artifact:ListComplianceInquiryQueries |
| athena | athena/calculationexecution | 1 |  | child-uncatalogued | athena:GetCalculationExecution, athena:ListCalculationExecutions |
| athena | athena/database | 1 |  | child-uncatalogued | athena:GetDatabase, athena:ListDatabases |
| athena | athena/executor | 2 |  | child-uncatalogued | athena:ListExecutors |
| athena | athena/notebookmetadata | 1 |  | child-uncatalogued | athena:GetNotebookMetadata, athena:ListNotebookMetadata |
| athena | athena/notebooksession | 1 |  | child-uncatalogued | athena:ListNotebookSessions |
| athena | athena/queryexecution | 0 |  | element-written | athena:BatchGetQueryExecution, athena:GetQueryExecution, athena:ListQueryExecutions |
| athena | athena/session | 1 |  | sr-resource | athena:GetSession, athena:ListSessions |
| athena | athena/tablemetadata | 1 |  | child-uncatalogued | athena:GetTableMetadata, athena:ListTableMetadata |
| auditmanager | auditmanager/assessmentcontrolinsight | 0 |  | child-uncatalogued | auditmanager:ListAssessmentControlInsightsByControlDomain |
| auditmanager | auditmanager/assessmentframeworksharerequest | 0 |  | element-written | auditmanager:ListAssessmentFrameworkShareRequests |
| auditmanager | auditmanager/assessmentreport | 0 |  | element-written | auditmanager:ListAssessmentReports |
| auditmanager | auditmanager/changelog | 1 |  | child-uncatalogued | auditmanager:GetChangeLogs |
| auditmanager | auditmanager/controldomaininsight | 0 |  | child-uncatalogued | auditmanager:ListControlDomainInsights, auditmanager:ListControlDomainInsightsByAssessment |
| auditmanager | auditmanager/controlinsight | 0 |  | child-uncatalogued | auditmanager:ListControlInsightsByControlDomain |
| auditmanager | auditmanager/delegation | 0 |  | element-arn | auditmanager:GetDelegations |
| auditmanager | auditmanager/evidence | 2 |  | child-uncatalogued | auditmanager:GetEvidence, auditmanager:GetEvidenceByEvidenceFolder |
| auditmanager | auditmanager/evidencefolder | 1 |  | child-uncatalogued | auditmanager:GetEvidenceFolder, auditmanager:GetEvidenceFoldersByAssessment, auditmanager:GetEvidenceFoldersByAssessmentControl |
| autoscaling | autoscaling/instancerefresh | 1 |  | child-uncatalogued | autoscaling:DescribeInstanceRefreshes |
| autoscaling | autoscaling/loadbalancer | 1 |  | child-uncatalogued | autoscaling:DescribeLoadBalancers |
| autoscaling | autoscaling/loadbalancertargetgroup | 1 |  | child-uncatalogued | autoscaling:DescribeLoadBalancerTargetGroups |
| autoscaling | autoscaling/notificationconfiguration | 0 |  | element-arn | autoscaling:DescribeNotificationConfigurations |
| autoscaling | autoscaling/scalingactivity | 0 |  | element-written | autoscaling:DescribeScalingActivities |
| autoscaling | autoscaling/trafficsource | 1 |  | child-uncatalogued | autoscaling:DescribeTrafficSources |
| aws-marketplace | aws-marketplace/agreementcancellationrequest | 0 |  | element-written | aws-marketplace:GetAgreementCancellationRequest, aws-marketplace:ListAgreementCancellationRequests |
| aws-marketplace | aws-marketplace/agreemententitlement | 1 |  | child-uncatalogued | aws-marketplace:GetAgreementEntitlements |
| aws-marketplace | aws-marketplace/agreementinvoicelineitem | 1 |  | child-uncatalogued | aws-marketplace:ListAgreementInvoiceLineItems |
| aws-marketplace | aws-marketplace/agreementpaymentrequest | 0 |  | element-written | aws-marketplace:GetAgreementPaymentRequest, aws-marketplace:ListAgreementPaymentRequests |
| aws-marketplace | aws-marketplace/assessment | 0 |  | sr-resource | aws-marketplace:DescribeAssessment, aws-marketplace:ListAssessments |
| aws-marketplace | aws-marketplace/billingadjustmentrequest | 0 |  | element-written | aws-marketplace:GetBillingAdjustmentRequest, aws-marketplace:ListBillingAdjustmentRequests |
| aws-marketplace | aws-marketplace/changeset | 0 |  | sr-resource | aws-marketplace:ListChangeSets |
| aws-marketplace | aws-marketplace/entitlement | 0 |  | element-arn | aws-marketplace:GetEntitlements |
| aws-marketplace | aws-marketplace/entity | 2 |  | sr-resource | aws-marketplace:DescribeEntity, aws-marketplace:ListEntities |
| aws-marketplace | aws-marketplace/listing | 0 |  | sr-resource | aws-marketplace:GetListing, aws-marketplace:SearchListings |
| aws-marketplace | aws-marketplace/purchaseoption | 0 |  | sr-resource | aws-marketplace:ListPurchaseOptions |
| awsssoportal | awsssoportal/account | 0 |  | sr-resource | awsssoportal:ListAccounts |
| backup | backup/backupaccesspoint | 0 |  | sr-resource | backup:DescribeBackupAccessPoint, backup:ListBackupAccessPoints, backup:ListBackupAccessPointsByRecoveryPoint, backup:ListBackupAccessPointsByResource |
| backup | backup/backupjob | 0 |  | element-written | backup:DescribeBackupJob, backup:ListBackupJobs |
| backup | backup/copyjob | 0 |  | element-written | backup:DescribeCopyJob, backup:ListCopyJobs |
| backup | backup/indexedrecoverypoint | 0 |  | element-arn | backup:ListIndexedRecoveryPoints |
| backup | backup/protectedresource | 0 |  | element-arn | backup:DescribeProtectedResource, backup:ListProtectedResources, backup:ListProtectedResourcesByBackupVault |
| backup | backup/reportjob | 0 |  | element-written | backup:DescribeReportJob, backup:ListReportJobs |
| backup | backup/restoreaccessbackupvault | 1 |  | child-uncatalogued | backup:ListRestoreAccessBackupVaults |
| backup | backup/restorejob | 0 |  | element-written | backup:DescribeRestoreJob, backup:ListRestoreJobs, backup:ListRestoreJobsByProtectedResource |
| backup | backup/scanjob | 0 |  | element-written | backup:DescribeScanJob, backup:ListScanJobs |
| backup-gateway | backup-gateway/bandwidthratelimitschedule | 1 |  | smithy-resource | backup-gateway:GetBandwidthRateLimitSchedule |
| backup-gateway | backup-gateway/hypervisorpropertymapping | 1 |  | smithy-resource | backup-gateway:GetHypervisorPropertyMappings |
| backup-search | backup-search/searchjob | 0 |  | smithy-resource | backup-search:GetSearchJob, backup-search:ListSearchJobs |
| backup-search | backup-search/searchjobbackup | 1 |  | child-uncatalogued | backup-search:ListSearchJobBackups |
| backup-search | backup-search/searchresultexportjob | 0 |  | smithy-resource | backup-search:GetSearchResultExportJob, backup-search:ListSearchResultExportJobs |
| batch | batch/job | 0 |  | sr-resource | batch:DescribeJobs, batch:ListJobs, batch:ListJobsByConsumableResource |
| batch | batch/servicejob | 0 |  | sr-resource | batch:DescribeServiceJob, batch:ListServiceJobs |
| bcm-data-exports | bcm-data-exports/execution | 1 |  | child-uncatalogued | bcm-data-exports:GetExecution, bcm-data-exports:ListExecutions |
| bcm-data-exports | bcm-data-exports/table | 0 |  | sr-resource | bcm-data-exports:ListTables |
| bcm-pricing-calculator | bcm-pricing-calculator/billestimatecommitment | 1 |  | smithy-resource | bcm-pricing-calculator:ListBillEstimateCommitments |
| bcm-pricing-calculator | bcm-pricing-calculator/billestimateinputcommitmentmodification | 1 |  | smithy-resource | bcm-pricing-calculator:ListBillEstimateInputCommitmentModifications |
| bcm-pricing-calculator | bcm-pricing-calculator/billestimateinputusagemodification | 1 |  | smithy-resource | bcm-pricing-calculator:ListBillEstimateInputUsageModifications |
| bcm-pricing-calculator | bcm-pricing-calculator/billestimatelineitem | 1 |  | smithy-resource | bcm-pricing-calculator:ListBillEstimateLineItems |
| bcm-pricing-calculator | bcm-pricing-calculator/billscenariocommitmentmodification | 1 |  | smithy-resource | bcm-pricing-calculator:ListBillScenarioCommitmentModifications |
| bcm-pricing-calculator | bcm-pricing-calculator/billscenariousagemodification | 1 |  | smithy-resource | bcm-pricing-calculator:ListBillScenarioUsageModifications |
| bcm-pricing-calculator | bcm-pricing-calculator/workloadestimateusage | 1 |  | smithy-resource | bcm-pricing-calculator:ListWorkloadEstimateUsage |
| bedrock | bedrock/advancedpromptoptimizationjob | 0 |  | smithy-resource | bedrock:GetAdvancedPromptOptimizationJob, bedrock:ListAdvancedPromptOptimizationJobs |
| bedrock | bedrock/agentactiongroup | 1 |  | child-uncatalogued | bedrock:GetAgentActionGroup, bedrock:ListAgentActionGroups |
| bedrock | bedrock/agentcollaborator | 0 |  | smithy-resource | bedrock:GetAgentCollaborator, bedrock:ListAgentCollaborators |
| bedrock | bedrock/agentknowledgebase | 1 |  | child-uncatalogued | bedrock:GetAgentKnowledgeBase, bedrock:ListAgentKnowledgeBases |
| bedrock | bedrock/agentversion | 1 |  | child-uncatalogued | bedrock:GetAgentVersion, bedrock:ListAgentVersions |
| bedrock | bedrock/asyncinvoke | 0 |  | smithy-resource | bedrock:GetAsyncInvoke, bedrock:ListAsyncInvokes |
| bedrock | bedrock/automatedreasoningpolicybuildworkflow | 1 |  | child-uncatalogued | bedrock:GetAutomatedReasoningPolicyBuildWorkflow, bedrock:ListAutomatedReasoningPolicyBuildWorkflows |
| bedrock | bedrock/automatedreasoningpolicytestcase | 1 |  | child-uncatalogued | bedrock:GetAutomatedReasoningPolicyTestCase, bedrock:ListAutomatedReasoningPolicyTestCases |
| bedrock | bedrock/automatedreasoningpolicytestresult | 1 |  | child-uncatalogued | bedrock:GetAutomatedReasoningPolicyTestResult, bedrock:ListAutomatedReasoningPolicyTestResults |
| bedrock | bedrock/dataautomationlibraryingestionjob | 0 |  | smithy-resource | bedrock:GetDataAutomationLibraryIngestionJob, bedrock:ListDataAutomationLibraryIngestionJobs |
| bedrock | bedrock/evaluationjob | 0 |  | smithy-resource | bedrock:GetEvaluationJob, bedrock:ListEvaluationJobs |
| bedrock | bedrock/flowexecution | 0 |  | smithy-resource | bedrock:ListFlowExecutions |
| bedrock | bedrock/foundationmodelagreementoffer | 1 |  | child-uncatalogued | bedrock:ListFoundationModelAgreementOffers |
| bedrock | bedrock/ingestionjob | 0 |  | smithy-resource | bedrock:GetIngestionJob, bedrock:ListIngestionJobs |
| bedrock | bedrock/invocation | 1 |  | smithy-resource | bedrock:ListInvocations |
| bedrock | bedrock/invocationstep | 1 |  | smithy-resource | bedrock:GetInvocationStep, bedrock:ListInvocationSteps |
| bedrock | bedrock/knowledgebasedocument | 0 |  | smithy-resource | bedrock:GetKnowledgeBaseDocuments, bedrock:ListKnowledgeBaseDocuments |
| bedrock | bedrock/modelcopyjob | 1 |  | sr-resource | bedrock:GetModelCopyJob, bedrock:ListModelCopyJobs, bedrock:ListTagsForResource |
| bedrock | bedrock/modelcustomizationjob | 1 |  | sr-resource | bedrock:GetModelCustomizationJob, bedrock:ListModelCustomizationJobs |
| bedrock | bedrock/modelimportjob | 1 |  | sr-resource | bedrock:GetModelImportJob, bedrock:ListModelImportJobs |
| bedrock | bedrock/modelinvocationjob | 0 |  | smithy-resource | bedrock:GetModelInvocationJob, bedrock:ListModelInvocationJobs |
| bedrock | bedrock/session | 0 |  | smithy-resource | bedrock:GetSession, bedrock:ListSessions |
| bedrock-agentcore | bedrock-agentcore/abtest | 1 |  | sr-resource | bedrock-agentcore:GetABTest, bedrock-agentcore:ListABTests |
| bedrock-agentcore | bedrock-agentcore/actor | 1 |  | child-uncatalogued | bedrock-agentcore:ListActors |
| bedrock-agentcore | bedrock-agentcore/agentruntimeversion | 1 |  | child-uncatalogued | bedrock-agentcore:ListAgentRuntimeVersionsByCapacityProvider |
| bedrock-agentcore | bedrock-agentcore/batchevaluation | 1 |  | child-uncatalogued | bedrock-agentcore:GetBatchEvaluation, bedrock-agentcore:ListBatchEvaluations |
| bedrock-agentcore | bedrock-agentcore/browsersession | 0 |  | smithy-resource | bedrock-agentcore:GetBrowserSession, bedrock-agentcore:ListBrowserSessions |
| bedrock-agentcore | bedrock-agentcore/capacityprovider | 0 |  | smithy-resource | bedrock-agentcore:GetCapacityProvider, bedrock-agentcore:ListCapacityProviders |
| bedrock-agentcore | bedrock-agentcore/codeinterpretersession | 0 |  | smithy-resource | bedrock-agentcore:GetCodeInterpreterSession, bedrock-agentcore:ListCodeInterpreterSessions |
| bedrock-agentcore | bedrock-agentcore/configurationbundleversion | 1 |  | child-uncatalogued | bedrock-agentcore:ListConfigurationBundleVersions |
| bedrock-agentcore | bedrock-agentcore/consentportal | 0 |  | smithy-resource | bedrock-agentcore:GetConsentPortal, bedrock-agentcore:ListConsentPortals |
| bedrock-agentcore | bedrock-agentcore/datasetexample | 1 |  | child-uncatalogued | bedrock-agentcore:ListDatasetExamples |
| bedrock-agentcore | bedrock-agentcore/event | 1 |  | child-uncatalogued | bedrock-agentcore:GetEvent, bedrock-agentcore:ListEvents |
| bedrock-agentcore | bedrock-agentcore/gatewayratelimit | 0 |  | smithy-resource | bedrock-agentcore:GetGatewayRateLimit, bedrock-agentcore:ListGatewayRateLimits |
| bedrock-agentcore | bedrock-agentcore/gatewayrule | 0 |  | smithy-resource | bedrock-agentcore:GetGatewayRule, bedrock-agentcore:ListGatewayRules |
| bedrock-agentcore | bedrock-agentcore/harnessversion | 1 |  | child-uncatalogued | bedrock-agentcore:ListHarnessVersions |
| bedrock-agentcore | bedrock-agentcore/memoryextractionjob | 1 |  | child-uncatalogued | bedrock-agentcore:ListMemoryExtractionJobs |
| bedrock-agentcore | bedrock-agentcore/memoryrecord | 1 |  | child-uncatalogued | bedrock-agentcore:GetMemoryRecord, bedrock-agentcore:ListMemoryRecords, bedrock-agentcore:RetrieveMemoryRecords |
| bedrock-agentcore | bedrock-agentcore/paymentinstrument | 0 |  | smithy-resource | bedrock-agentcore:GetPaymentInstrument, bedrock-agentcore:ListPaymentInstruments |
| bedrock-agentcore | bedrock-agentcore/paymentsession | 0 |  | smithy-resource | bedrock-agentcore:GetPaymentSession, bedrock-agentcore:ListPaymentSessions |
| bedrock-agentcore | bedrock-agentcore/policygenerationasset | 1 |  | child-uncatalogued | bedrock-agentcore:ListPolicyGenerationAssets |
| bedrock-agentcore | bedrock-agentcore/recommendation | 1 |  | sr-resource | bedrock-agentcore:GetRecommendation, bedrock-agentcore:ListRecommendations |
| bedrock-agentcore | bedrock-agentcore/session | 1 |  | child-uncatalogued | bedrock-agentcore:ListSessions |
| billing | billing/sourceviewsforbillingview | 1 |  | child-uncatalogued | billing:ListSourceViewsForBillingView |
| billingconductor | billingconductor/accountassociation | 0 |  | element-arn | billingconductor:ListAccountAssociations |
| billingconductor | billingconductor/billinggroupcostreport | 0 |  | element-arn | billingconductor:GetBillingGroupCostReport, billingconductor:ListBillingGroupCostReports |
| billingconductor | billingconductor/customlineitemversion | 1 |  | child-uncatalogued | billingconductor:ListCustomLineItemVersions |
| billingconductor | billingconductor/resourcesassociatedtocustomlineitem | 1 |  | child-uncatalogued | billingconductor:ListResourcesAssociatedToCustomLineItem |
| braket | braket/device | 0 |  | smithy-resource | braket:SearchDevices |
| braket | braket/job | 0 |  | smithy-resource | braket:GetJob, braket:SearchJobs |
| braket | braket/quantumtask | 0 |  | smithy-resource | braket:GetQuantumTask, braket:SearchQuantumTasks |
| cases | cases/allrelateditem | 1 |  | child-uncatalogued | cases:SearchAllRelatedItems |
| cases | cases/case | 1 |  | smithy-resource | cases:GetCase, cases:ListCasesForContact, cases:SearchCases |
| cases | cases/caseauditevent | 2 |  | child-uncatalogued | cases:GetCaseAuditEvents |
| cases | cases/fieldoption | 2 |  | child-uncatalogued | cases:ListFieldOptions |
| cases | cases/relateditem | 2 |  | smithy-resource | cases:SearchRelatedItems |
| cassandra | cassandra/stream | 0 |  | sr-resource | cassandra:GetStream, cassandra:ListStreams |
| ce | ce/anomaly | 1 |  | child-uncatalogued | ce:GetAnomalies |
| ce | ce/commitmentpurchaseanalysis | 0 |  | element-written | ce:GetCommitmentPurchaseAnalysis, ce:ListCommitmentPurchaseAnalyses |
| ce | ce/costallocationtagbackfillhistory | 0 |  | element-written | ce:ListCostAllocationTagBackfillHistory |
| ce | ce/costcategoryresourceassociation | 0 |  | element-arn | ce:ListCostCategoryResourceAssociations |
| ce | ce/savingsplansutilizationdetail | 0 |  | element-arn | ce:GetSavingsPlansUtilizationDetails |
| chatbot | chatbot/chimewebhookconfiguration | 0 |  | element-written | chatbot:DescribeChimeWebhookConfigurations |
| chatbot | chatbot/microsoftteamsuseridentity | 0 |  | element-arn | chatbot:ListMicrosoftTeamsUserIdentities |
| chatbot | chatbot/slackuseridentity | 0 |  | element-arn | chatbot:DescribeSlackUserIdentities |
| chime | chime/account | 0 |  | element-written | chime:GetAccount, chime:ListAccounts |
| chime | chime/appinstanceuserendpoint | 1 |  | child-uncatalogued | chime:DescribeAppInstanceUserEndpoint, chime:ListAppInstanceUserEndpoints |
| chime | chime/attendee | 1 |  | child-uncatalogued | chime:GetAttendee, chime:ListAttendees |
| chime | chime/bot | 0 |  | element-written | chime:GetBot, chime:ListBots |
| chime | chime/channel | 0 |  | sr-resource | chime:DescribeChannel, chime:ListChannels, chime:SearchChannels |
| chime | chime/channelmessage | 1 |  | child-uncatalogued | chime:GetChannelMessage, chime:ListChannelMessages |
| chime | chime/channelsassociatedwithchannelflow | 1 |  | child-uncatalogued | chime:ListChannelsAssociatedWithChannelFlow |
| chime | chime/mediacapturepipeline | 0 |  | element-arn | chime:GetMediaCapturePipeline, chime:ListMediaCapturePipelines |
| chime | chime/messagingstreamingconfiguration | 1 |  | child-uncatalogued | chime:GetMessagingStreamingConfigurations |
| chime | chime/phonenumber | 0 |  | element-written | chime:GetPhoneNumber, chime:ListPhoneNumbers |
| chime | chime/phonenumberorder | 0 |  | element-written | chime:GetPhoneNumberOrder, chime:ListPhoneNumberOrders |
| chime | chime/proxysession | 1 |  | child-uncatalogued | chime:GetProxySession, chime:ListProxySessions |
| chime | chime/room | 0 |  | element-written | chime:GetRoom, chime:ListRooms |
| chime | chime/roommembership | 1 |  | child-uncatalogued | chime:ListRoomMemberships |
| chime | chime/siprule | 0 |  | element-written | chime:GetSipRule, chime:ListSipRules |
| chime | chime/subchannel | 1 |  | child-uncatalogued | chime:ListSubChannels |
| chime | chime/user | 0 |  | element-written | chime:GetUser, chime:ListUsers |
| chime | chime/voiceconnectorgroup | 0 |  | element-written | chime:GetVoiceConnectorGroup, chime:ListVoiceConnectorGroups |
| cleanrooms | cleanrooms/analysislogexport | 1 |  | child-uncatalogued | cleanrooms:GetAnalysisLogExport, cleanrooms:ListAnalysisLogExports |
| cleanrooms | cleanrooms/collaborationanalysistemplate | 1 |  | child-uncatalogued | cleanrooms:BatchGetCollaborationAnalysisTemplate, cleanrooms:GetCollaborationAnalysisTemplate, cleanrooms:ListCollaborationAnalysisTemplates |
| cleanrooms | cleanrooms/collaborationchangerequest | 1 |  | child-uncatalogued | cleanrooms:GetCollaborationChangeRequest, cleanrooms:ListCollaborationChangeRequests |
| cleanrooms | cleanrooms/collaborationconfiguredaudiencemodelassociation | 1 |  | child-uncatalogued | cleanrooms:GetCollaborationConfiguredAudienceModelAssociation, cleanrooms:ListCollaborationConfiguredAudienceModelAssociations |
| cleanrooms | cleanrooms/collaborationidnamespaceassociation | 1 |  | child-uncatalogued | cleanrooms:ListCollaborationIdNamespaceAssociations |
| cleanrooms | cleanrooms/collaborationprivacybudget | 1 |  | child-uncatalogued | cleanrooms:ListCollaborationPrivacyBudgets |
| cleanrooms | cleanrooms/collaborationprivacybudgettemplate | 1 |  | child-uncatalogued | cleanrooms:GetCollaborationPrivacyBudgetTemplate, cleanrooms:ListCollaborationPrivacyBudgetTemplates |
| cleanrooms | cleanrooms/intermediatetable | 1 |  | smithy-resource | cleanrooms:GetIntermediateTable, cleanrooms:ListIntermediateTables |
| cleanrooms | cleanrooms/intermediatetableversion | 2 |  | child-uncatalogued | cleanrooms:ListIntermediateTableVersions |
| cleanrooms | cleanrooms/member | 1 |  | child-uncatalogued | cleanrooms:ListMembers |
| cleanrooms | cleanrooms/privacybudget | 1 |  | child-uncatalogued | cleanrooms:ListPrivacyBudgets |
| cleanrooms | cleanrooms/protectedjob | 1 |  | child-uncatalogued | cleanrooms:GetProtectedJob, cleanrooms:ListProtectedJobs |
| cleanrooms | cleanrooms/protectedquery | 1 |  | child-uncatalogued | cleanrooms:GetProtectedQuery, cleanrooms:ListProtectedQueries |
| cleanrooms | cleanrooms/schema | 1 |  | child-uncatalogued | cleanrooms:BatchGetSchema, cleanrooms:GetSchema, cleanrooms:ListSchemas |
| cleanrooms-ml | cleanrooms-ml/audienceexportjob | 0 |  | smithy-resource | cleanrooms-ml:ListAudienceExportJobs |
| cleanrooms-ml | cleanrooms-ml/audiencegenerationjob | 0 |  | smithy-resource | cleanrooms-ml:GetAudienceGenerationJob, cleanrooms-ml:ListAudienceGenerationJobs |
| cleanrooms-ml | cleanrooms-ml/collaborationconfiguredmodelalgorithmassociation | 1 |  | child-uncatalogued | cleanrooms-ml:ListCollaborationConfiguredModelAlgorithmAssociations |
| cleanrooms-ml | cleanrooms-ml/collaborationmlinputchannel | 1 |  | child-uncatalogued | cleanrooms-ml:GetCollaborationMLInputChannel, cleanrooms-ml:ListCollaborationMLInputChannels |
| cleanrooms-ml | cleanrooms-ml/collaborationtrainedmodel | 1 |  | child-uncatalogued | cleanrooms-ml:GetCollaborationTrainedModel, cleanrooms-ml:ListCollaborationTrainedModels |
| cleanrooms-ml | cleanrooms-ml/collaborationtrainedmodelexportjob | 1 |  | child-uncatalogued | cleanrooms-ml:ListCollaborationTrainedModelExportJobs |
| cleanrooms-ml | cleanrooms-ml/collaborationtrainedmodelinferencejob | 1 |  | child-uncatalogued | cleanrooms-ml:ListCollaborationTrainedModelInferenceJobs |
| cleanrooms-ml | cleanrooms-ml/trainedmodelinferencejob | 0 |  | smithy-resource | cleanrooms-ml:GetTrainedModelInferenceJob, cleanrooms-ml:ListTrainedModelInferenceJobs |
| cloud9 | cloud9/environmentmembership | 0 |  | element-written | cloud9:DescribeEnvironmentMemberships |
| cloudcontrolapi | cloudcontrolapi/resource | 1 |  | child-uncatalogued | cloudcontrolapi:GetResource, cloudcontrolapi:ListResources |
| cloudcontrolapi | cloudcontrolapi/resourcerequest | 0 |  | element-written | cloudcontrolapi:GetResourceRequestStatus, cloudcontrolapi:ListResourceRequests |
| clouddirectory | clouddirectory/facetattribute | 3 |  | child-uncatalogued | clouddirectory:ListFacetAttributes |
| clouddirectory | clouddirectory/facetname | 3 |  | child-uncatalogued | clouddirectory:ListFacetNames |
| clouddirectory | clouddirectory/index | 1 |  | child-uncatalogued | clouddirectory:ListAttachedIndices, clouddirectory:ListIndex |
| clouddirectory | clouddirectory/objectinformation | 1 |  | child-uncatalogued | clouddirectory:GetObjectInformation |
| clouddirectory | clouddirectory/objectparent | 1 |  | child-uncatalogued | clouddirectory:ListObjectParents |
| clouddirectory | clouddirectory/objectparentpath | 1 |  | child-uncatalogued | clouddirectory:ListObjectParentPaths |
| clouddirectory | clouddirectory/objectpolicy | 1 |  | child-uncatalogued | clouddirectory:ListObjectPolicies |
| clouddirectory | clouddirectory/policyattachment | 1 |  | child-uncatalogued | clouddirectory:ListPolicyAttachments |
| clouddirectory | clouddirectory/typedlinkfacetattribute | 3 |  | child-uncatalogued | clouddirectory:ListTypedLinkFacetAttributes |
| clouddirectory | clouddirectory/typedlinkfacetname | 3 |  | child-uncatalogued | clouddirectory:ListTypedLinkFacetNames |
| cloudformation | cloudformation/changeset | 1 |  | sr-resource | cloudformation:DescribeChangeSet, cloudformation:ListChangeSets |
| cloudformation | cloudformation/hookresult | 0 |  | element-arn | cloudformation:GetHookResult, cloudformation:ListHookResults |
| cloudformation | cloudformation/import | 1 |  | child-uncatalogued | cloudformation:ListImports |
| cloudformation | cloudformation/resourcescanresource | 1 |  | child-uncatalogued | cloudformation:ListResourceScanRelatedResources, cloudformation:ListResourceScanResources |
| cloudformation | cloudformation/stackevent | 1 |  | child-uncatalogued | cloudformation:DescribeStackEvents |
| cloudformation | cloudformation/stackinstanceresourcedrift | 1 |  | child-uncatalogued | cloudformation:ListStackInstanceResourceDrifts |
| cloudformation | cloudformation/stackrefactor | 0 |  | element-written | cloudformation:DescribeStackRefactor, cloudformation:ListStackRefactors |
| cloudformation | cloudformation/stackrefactoraction | 1 |  | child-uncatalogued | cloudformation:ListStackRefactorActions |
| cloudformation | cloudformation/stackresourcedrift | 1 |  | child-uncatalogued | cloudformation:DescribeStackResourceDrifts, cloudformation:DetectStackResourceDrift |
| cloudformation | cloudformation/stacksetautodeploymenttarget | 1 |  | child-uncatalogued | cloudformation:ListStackSetAutoDeploymentTargets |
| cloudformation | cloudformation/stacksetoperation | 1 |  | child-uncatalogued | cloudformation:ListStackSetOperations |
| cloudformation | cloudformation/stacksetoperationresult | 1 |  | child-uncatalogued | cloudformation:ListStackSetOperationResults |
| cloudformation | cloudformation/typeversion | 0 |  | element-arn | cloudformation:ListTypeVersions |
| cloudfront | cloudfront/conflictingalias | 1 |  | child-uncatalogued | cloudfront:ListConflictingAliases |
| cloudfront | cloudfront/domainconflict | 1 |  | child-uncatalogued | cloudfront:ListDomainConflicts |
| cloudfront | cloudfront/invalidation | 1 |  | child-uncatalogued | cloudfront:GetInvalidation, cloudfront:GetInvalidationForDistributionTenant, cloudfront:ListInvalidations, cloudfront:ListInvalidationsForDistributionTenant |
| cloudhsm | cloudhsm/hsm | 0 |  | element-written | cloudhsm:ListHsms |
| cloudtrail | cloudtrail/event | 0 |  | child-uncatalogued | cloudtrail:ListInsightsData, cloudtrail:LookupEvents |
| cloudtrail | cloudtrail/eventconfiguration | 0 |  | element-written | cloudtrail:GetEventConfiguration |
| cloudtrail | cloudtrail/import | 0 |  | element-written | cloudtrail:GetImport, cloudtrail:ListImports |
| cloudtrail | cloudtrail/insightselector | 0 |  | element-written | cloudtrail:GetInsightSelectors |
| cloudtrail | cloudtrail/query | 0 |  | child-uncatalogued | cloudtrail:DescribeQuery, cloudtrail:ListQueries |
| cloudtrail | cloudtrail/queryresult | 1 |  | child-uncatalogued | cloudtrail:GetQueryResults |
| codeartifact | codeartifact/allowedrepository | 2 |  | child-uncatalogued | codeartifact:ListAllowedRepositoriesForGroup |
| codeartifact | codeartifact/package | 1 |  | sr-resource | codeartifact:DescribePackage, codeartifact:ListPackages |
| codeartifact | codeartifact/packageversionasset | 2 |  | child-uncatalogued | codeartifact:GetPackageVersionAsset, codeartifact:ListPackageVersionAssets |
| codebuild | codebuild/build | 0 |  | sr-resource | codebuild:BatchGetBuilds, codebuild:ListBuilds, codebuild:ListBuildsForProject |
| codebuild | codebuild/buildbatch | 0 |  | sr-resource | codebuild:BatchGetBuildBatches, codebuild:ListBuildBatches, codebuild:ListBuildBatchesForProject |
| codebuild | codebuild/codecoverage | 1 |  | child-uncatalogued | codebuild:DescribeCodeCoverages |
| codebuild | codebuild/commandexecution | 1 |  | child-uncatalogued | codebuild:BatchGetCommandExecutions, codebuild:ListCommandExecutionsForSandbox |
| codebuild | codebuild/report | 0 |  | sr-resource | codebuild:BatchGetReports, codebuild:ListReports |
| codebuild | codebuild/reportsforreportgroup | 1 |  | child-uncatalogued | codebuild:ListReportsForReportGroup |
| codebuild | codebuild/sandbox | 0 |  | sr-resource | codebuild:BatchGetSandboxes, codebuild:ListSandboxes, codebuild:ListSandboxesForProject |
| codebuild | codebuild/testcase | 1 |  | child-uncatalogued | codebuild:DescribeTestCases |
| codecatalyst | codecatalyst/accesstoken | 0 |  | smithy-resource | codecatalyst:ListAccessTokens |
| codecatalyst | codecatalyst/devenvironment | 2 |  | child-uncatalogued | codecatalyst:GetDevEnvironment, codecatalyst:ListDevEnvironments |
| codecatalyst | codecatalyst/devenvironmentsession | 3 |  | child-uncatalogued | codecatalyst:ListDevEnvironmentSessions |
| codecatalyst | codecatalyst/eventlog | 1 |  | smithy-resource | codecatalyst:ListEventLogs |
| codecatalyst | codecatalyst/project | 1 |  | smithy-resource | codecatalyst:GetProject, codecatalyst:ListProjects |
| codecatalyst | codecatalyst/sourcerepository | 2 |  | smithy-resource | codecatalyst:GetSourceRepository, codecatalyst:ListSourceRepositories |
| codecatalyst | codecatalyst/sourcerepositorybranch | 3 |  | smithy-resource | codecatalyst:ListSourceRepositoryBranches |
| codecatalyst | codecatalyst/space | 0 |  | smithy-resource | codecatalyst:GetSpace, codecatalyst:ListSpaces |
| codecatalyst | codecatalyst/workflow | 2 |  | smithy-resource | codecatalyst:GetWorkflow, codecatalyst:ListWorkflows |
| codecatalyst | codecatalyst/workflowrun | 2 |  | smithy-resource | codecatalyst:GetWorkflowRun, codecatalyst:ListWorkflowRuns |
| codecommit | codecommit/approvalruletemplate | 0 |  | element-written | codecommit:GetApprovalRuleTemplate, codecommit:ListApprovalRuleTemplates |
| codecommit | codecommit/associatedapprovalruletemplate | 1 |  | child-uncatalogued | codecommit:ListAssociatedApprovalRuleTemplatesForRepository |
| codecommit | codecommit/branch | 0 |  | child-uncatalogued | codecommit:GetBranch, codecommit:ListBranches |
| codecommit | codecommit/comment | 1 |  | child-uncatalogued | codecommit:GetComment, codecommit:GetCommentsForPullRequest |
| codecommit | codecommit/commentsforcomparedcommit | 1 |  | child-uncatalogued | codecommit:GetCommentsForComparedCommit |
| codecommit | codecommit/commit | 1 |  | child-uncatalogued | codecommit:BatchGetCommits, codecommit:GetCommit, codecommit:GetMergeCommit |
| codecommit | codecommit/filecommithistory | 1 |  | child-uncatalogued | codecommit:ListFileCommitHistory |
| codecommit | codecommit/pullrequest | 1 |  | child-uncatalogued | codecommit:GetPullRequest, codecommit:ListPullRequests |
| codecommit | codecommit/pullrequestevent | 1 |  | child-uncatalogued | codecommit:DescribePullRequestEvents |
| codecommit | codecommit/repositorytrigger | 1 |  | child-uncatalogued | codecommit:GetRepositoryTriggers |
| codeconnections | codeconnections/connection | 0 |  | sr-resource | codeconnections:GetConnection, codeconnections:ListConnections |
| codeconnections | codeconnections/host | 0 |  | sr-resource | codeconnections:GetHost, codeconnections:ListHosts |
| codeconnections | codeconnections/repositorylink | 0 |  | sr-resource | codeconnections:GetRepositoryLink, codeconnections:ListRepositoryLinks |
| codeconnections | codeconnections/syncconfiguration | 1 |  | child-uncatalogued | codeconnections:GetSyncConfiguration, codeconnections:ListSyncConfigurations |
| codedeploy | codedeploy/deployment | 0 |  | element-written | codedeploy:BatchGetDeployments, codedeploy:GetDeployment, codedeploy:ListDeployments |
| codedeploy | codedeploy/deploymentinstance | 2 |  | child-uncatalogued | codedeploy:GetDeploymentInstance, codedeploy:ListDeploymentInstances |
| codedeploy | codedeploy/deploymenttarget | 1 |  | child-uncatalogued | codedeploy:GetDeploymentTarget, codedeploy:ListDeploymentTargets |
| codeguru-profiler | codeguru-profiler/findingsreport | 1 |  | child-uncatalogued | codeguru-profiler:GetFindingsReportAccountSummary, codeguru-profiler:ListFindingsReports |
| codeguru-reviewer | codeguru-reviewer/codereview | 0 |  | sr-resource | codeguru-reviewer:DescribeCodeReview, codeguru-reviewer:ListCodeReviews |
| codeguru-reviewer | codeguru-reviewer/recommendation | 1 |  | child-uncatalogued | codeguru-reviewer:ListRecommendations |
| codeguru-reviewer | codeguru-reviewer/recommendationfeedback | 1 |  | child-uncatalogued | codeguru-reviewer:DescribeRecommendationFeedback, codeguru-reviewer:ListRecommendationFeedback |
| codeguru-security | codeguru-security/finding | 0 |  | child-uncatalogued | codeguru-security:GetFindings |
| codeguru-security | codeguru-security/scan | 0 |  | element-written | codeguru-security:GetScan, codeguru-security:ListScans |
| codepipeline | codepipeline/actionexecution | 1 |  | child-uncatalogued | codepipeline:ListActionExecutions |
| codepipeline | codepipeline/deployactionexecutiontarget | 1 |  | child-uncatalogued | codepipeline:ListDeployActionExecutionTargets |
| codepipeline | codepipeline/pipelineexecution | 1 |  | child-uncatalogued | codepipeline:GetPipelineExecution, codepipeline:ListPipelineExecutions |
| codepipeline | codepipeline/ruleexecution | 1 |  | child-uncatalogued | codepipeline:ListRuleExecutions |
| codepipeline | codepipeline/ruletype | 0 |  | element-written | codepipeline:ListRuleTypes |
| cognito-identity | cognito-identity/identity | 0 |  | child-uncatalogued | cognito-identity:DescribeIdentity, cognito-identity:ListIdentities |
| cognito-idp | cognito-idp/device | 0 |  | element-created | cognito-idp:AdminGetDevice, cognito-idp:AdminListDevices, cognito-idp:GetDevice, cognito-idp:ListDevices |
| cognito-idp | cognito-idp/listuserauthevent | 1 |  | child-uncatalogued | cognito-idp:AdminListUserAuthEvents |
| cognito-idp | cognito-idp/user | 0 |  | element-written | cognito-idp:AdminGetUser, cognito-idp:GetUser, cognito-idp:ListUsers, cognito-idp:ListUsersInGroup |
| cognito-idp | cognito-idp/userimportjob | 1 |  | child-uncatalogued | cognito-idp:DescribeUserImportJob, cognito-idp:ListUserImportJobs |
| cognito-idp | cognito-idp/userpoolclientsecret | 1 |  | child-uncatalogued | cognito-idp:ListUserPoolClientSecrets |
| cognito-idp | cognito-idp/userpoolreplica | 1 |  | child-uncatalogued | cognito-idp:ListUserPoolReplicas |
| cognito-idp | cognito-idp/webauthncredential | 0 |  | element-created | cognito-idp:ListWebAuthnCredentials |
| cognito-sync | cognito-sync/dataset | 2 |  | sr-resource | cognito-sync:DescribeDataset, cognito-sync:ListDatasets |
| cognito-sync | cognito-sync/identitypoolusage | 0 |  | element-written | cognito-sync:DescribeIdentityPoolUsage, cognito-sync:ListIdentityPoolUsage |
| cognito-sync | cognito-sync/record | 3 |  | child-uncatalogued | cognito-sync:ListRecords |
| comprehend | comprehend/dataset | 0 |  | element-written | comprehend:DescribeDataset, comprehend:ListDatasets |
| comprehend | comprehend/documentclassificationjob | 0 |  | sr-resource | comprehend:DescribeDocumentClassificationJob, comprehend:ListDocumentClassificationJobs |
| comprehend | comprehend/documentclassifiersummary | 0 |  | element-written | comprehend:ListDocumentClassifierSummaries |
| comprehend | comprehend/dominantlanguagedetectionjob | 0 |  | sr-resource | comprehend:DescribeDominantLanguageDetectionJob, comprehend:ListDominantLanguageDetectionJobs |
| comprehend | comprehend/entitiesdetectionjob | 0 |  | sr-resource | comprehend:DescribeEntitiesDetectionJob, comprehend:ListEntitiesDetectionJobs |
| comprehend | comprehend/entityrecognizersummary | 0 |  | element-created | comprehend:ListEntityRecognizerSummaries |
| comprehend | comprehend/eventsdetectionjob | 0 |  | sr-resource | comprehend:DescribeEventsDetectionJob, comprehend:ListEventsDetectionJobs |
| comprehend | comprehend/flywheeliteration | 1 |  | child-uncatalogued | comprehend:DescribeFlywheelIteration, comprehend:ListFlywheelIterationHistory |
| comprehend | comprehend/keyphrasesdetectionjob | 0 |  | sr-resource | comprehend:DescribeKeyPhrasesDetectionJob, comprehend:ListKeyPhrasesDetectionJobs |
| comprehend | comprehend/piientitiesdetectionjob | 0 |  | sr-resource | comprehend:DescribePiiEntitiesDetectionJob, comprehend:ListPiiEntitiesDetectionJobs |
| comprehend | comprehend/sentimentdetectionjob | 0 |  | sr-resource | comprehend:DescribeSentimentDetectionJob, comprehend:ListSentimentDetectionJobs |
| comprehend | comprehend/targetedsentimentdetectionjob | 0 |  | sr-resource | comprehend:DescribeTargetedSentimentDetectionJob, comprehend:ListTargetedSentimentDetectionJobs |
| comprehend | comprehend/topicsdetectionjob | 0 |  | sr-resource | comprehend:DescribeTopicsDetectionJob, comprehend:ListTopicsDetectionJobs |
| comprehendmedical | comprehendmedical/phidetectionjob | 0 |  | element-written | comprehendmedical:DescribeEntitiesDetectionV2Job, comprehendmedical:DescribeICD10CMInferenceJob, comprehendmedical:DescribePHIDetectionJob, comprehendmedical:DescribeRxNormInferenceJob, comprehendmedical:DescribeSNOMEDCTInferenceJob, comprehendmedical:ListEntitiesDetectionV2Jobs, comprehendmedical:ListICD10CMInferenceJobs, comprehendmedical:ListPHIDetectionJobs, comprehendmedical:ListRxNormInferenceJobs, comprehendmedical:ListSNOMEDCTInferenceJobs |
| compute-optimizer | compute-optimizer/autoscalinggrouprecommendation | 0 |  | element-written | compute-optimizer:GetAutoScalingGroupRecommendations |
| compute-optimizer | compute-optimizer/ebsvolumerecommendation | 0 |  | element-arn | compute-optimizer:GetEBSVolumeRecommendations |
| compute-optimizer | compute-optimizer/ec2instancerecommendation | 0 |  | element-written | compute-optimizer:GetEC2InstanceRecommendations |
| compute-optimizer | compute-optimizer/ecsservicerecommendation | 0 |  | element-arn | compute-optimizer:GetECSServiceRecommendations |
| compute-optimizer | compute-optimizer/idlerecommendation | 0 |  | element-arn | compute-optimizer:GetIdleRecommendations |
| compute-optimizer | compute-optimizer/lambdafunctionrecommendation | 0 |  | element-arn | compute-optimizer:GetLambdaFunctionRecommendations |
| compute-optimizer | compute-optimizer/licenserecommendation | 0 |  | element-arn | compute-optimizer:GetLicenseRecommendations |
| compute-optimizer | compute-optimizer/rdsdatabaserecommendation | 0 |  | element-written | compute-optimizer:GetRDSDatabaseRecommendations |
| compute-optimizer | compute-optimizer/recommendationexportjob | 0 |  | element-created | compute-optimizer:DescribeRecommendationExportJobs |
| compute-optimizer | compute-optimizer/recommendationpreference | 0 |  | element-written | compute-optimizer:GetRecommendationPreferences |
| config | config/aggregatecompliancebyconfigrule | 1 |  | child-uncatalogued | config:DescribeAggregateComplianceByConfigRules |
| config | config/aggregatecompliancebyconformancepack | 1 |  | child-uncatalogued | config:DescribeAggregateComplianceByConformancePacks |
| config | config/aggregatecompliancedetail | 1 |  | child-uncatalogued | config:GetAggregateComplianceDetailsByConfigRule |
| config | config/aggregateconfigrulecompliancesummary | 1 |  | child-uncatalogued | config:GetAggregateConfigRuleComplianceSummary |
| config | config/aggregateconformancepackcompliancesummary | 1 |  | child-uncatalogued | config:GetAggregateConformancePackComplianceSummary |
| config | config/aggregatediscoveredresource | 1 |  | child-uncatalogued | config:ListAggregateDiscoveredResources |
| config | config/aggregatediscoveredresourcecount | 1 |  | child-uncatalogued | config:GetAggregateDiscoveredResourceCounts |
| config | config/aggregateresourceconfig | 1 |  | child-uncatalogued | config:SelectAggregateResourceConfig |
| config | config/compliancedetail | 0 |  | element-written | config:GetComplianceDetailsByConfigRule, config:GetComplianceDetailsByResource |
| config | config/configruleevaluationstatus | 0 |  | element-written | config:DescribeConfigRuleEvaluationStatus |
| config | config/configurationaggregatorsourcesstatus | 1 |  | child-uncatalogued | config:DescribeConfigurationAggregatorSourcesStatus |
| config | config/configurationrecorderstatus | 0 |  | element-arn | config:DescribeConfigurationRecorderStatus |
| config | config/conformancepackcompliance | 1 |  | child-uncatalogued | config:DescribeConformancePackCompliance |
| config | config/conformancepackcompliancedetail | 1 |  | child-uncatalogued | config:GetConformancePackComplianceDetails |
| config | config/conformancepackcompliancescore | 0 |  | element-written | config:ListConformancePackComplianceScores |
| config | config/conformancepackcompliancesummary | 1 |  | child-uncatalogued | config:GetConformancePackComplianceSummary |
| config | config/conformancepackstatus | 0 |  | element-written | config:DescribeConformancePackStatus |
| config | config/connector | 0 |  | sr-resource | config:GetConnector, config:ListConnectors |
| config | config/organizationconfigruledetailedstatus | 1 |  | child-uncatalogued | config:GetOrganizationConfigRuleDetailedStatus |
| config | config/organizationconfigrulestatus | 0 |  | element-written | config:DescribeOrganizationConfigRuleStatuses |
| config | config/organizationconformancepackdetailedstatus | 1 |  | child-uncatalogued | config:GetOrganizationConformancePackDetailedStatus |
| config | config/organizationconformancepackstatus | 0 |  | element-written | config:DescribeOrganizationConformancePackStatuses |
| config | config/remediationexception | 1 |  | child-uncatalogued | config:DescribeRemediationExceptions |
| config | config/resourceconfig | 0 |  | element-written | config:BatchGetResourceConfig, config:SelectResourceConfig |
| config | config/resourceconfighistory | 0 |  | child-uncatalogued | config:GetAggregateResourceConfig, config:GetResourceConfigHistory |
| config | config/resourceevaluation | 0 |  | element-written | config:ListResourceEvaluations |
| config | config/retentionconfiguration | 0 |  | element-written | config:DescribeRetentionConfigurations |
| connect | connect/analyticsdataassociation | 1 |  | child-uncatalogued | connect:ListAnalyticsDataAssociations |
| connect | connect/analyticsdatalakedataset | 1 |  | child-uncatalogued | connect:ListAnalyticsDataLakeDataSets |
| connect | connect/associatedcontact | 1 |  | child-uncatalogued | connect:ListAssociatedContacts |
| connect | connect/attachedfilesconfiguration | 0 |  | child-uncatalogued | connect:DescribeAttachedFilesConfiguration, connect:ListAttachedFilesConfigurations |
| connect | connect/childhour | 2 |  | child-uncatalogued | connect:ListChildHoursOfOperations |
| connect | connect/contact | 1 |  | sr-resource | connect:DescribeContact, connect:SearchContacts |
| connect | connect/contactevaluation | 1 |  | sr-resource | connect:DescribeContactEvaluation, connect:ListContactEvaluations, connect:SearchContactEvaluations |
| connect | connect/datatablevalue | 2 |  | child-uncatalogued | connect:BatchDescribeDataTableValue, connect:EvaluateDataTableValues, connect:ListDataTableValues |
| connect | connect/defaultvocabulary | 1 |  | child-uncatalogued | connect:ListDefaultVocabularies |
| connect | connect/entitysecurityprofile | 1 |  | child-uncatalogued | connect:ListEntitySecurityProfiles |
| connect | connect/evaluationformaiversion | 1 |  | child-uncatalogued | connect:ListEvaluationFormAIVersions |
| connect | connect/evaluationformversion | 2 |  | child-uncatalogued | connect:ListEvaluationFormVersions |
| connect | connect/extractiondefinition | 0 |  | sr-resource | connect:DescribeExtractionDefinition, connect:ListExtractionDefinitions |
| connect | connect/flowassociation | 1 |  | child-uncatalogued | connect:BatchGetFlowAssociation, connect:GetFlowAssociation, connect:ListFlowAssociations |
| connect | connect/getattachedfilemetadata | 1 |  | child-uncatalogued | connect:BatchGetAttachedFileMetadata |
| connect | connect/hoursofoperationoverride | 2 |  | child-uncatalogued | connect:DescribeHoursOfOperationOverride, connect:ListHoursOfOperationOverrides, connect:SearchHoursOfOperationOverrides |
| connect | connect/lambdafunction | 1 |  | child-uncatalogued | connect:ListLambdaFunctions |
| connect | connect/lexbot | 1 |  | child-uncatalogued | connect:ListLexBots |
| connect | connect/metric | 1 |  | sr-resource | connect:DescribeMetric, connect:ListMetrics, connect:SearchMetrics |
| connect | connect/queueemailaddress | 2 |  | child-uncatalogued | connect:ListQueueEmailAddresses |
| connect | connect/routingprofilemanualassignmentqueue | 2 |  | child-uncatalogued | connect:ListRoutingProfileManualAssignmentQueues |
| connect | connect/routingprofilequeue | 2 |  | child-uncatalogued | connect:ListRoutingProfileQueues |
| connect | connect/securityprofileflowmodule | 2 |  | child-uncatalogued | connect:ListSecurityProfileFlowModules |
| connect | connect/securityprofilepermission | 2 |  | child-uncatalogued | connect:ListSecurityProfilePermissions |
| connect | connect/testcase | 1 |  | child-uncatalogued | connect:DescribeTestCase, connect:ListTestCases |
| connect | connect/testcaseexecution | 0 |  | child-uncatalogued | connect:ListTestCaseExecutions |
| connect | connect/testcaseexecutionrecord | 1 |  | child-uncatalogued | connect:ListTestCaseExecutionRecords |
| connect | connect/trafficdistributiongroupuser | 1 |  | child-uncatalogued | connect:ListTrafficDistributionGroupUsers |
| connect | connect/usecase | 2 |  | sr-resource | connect:ListUseCases |
| connect | connect/usernotification | 2 |  | child-uncatalogued | connect:ListUserNotifications |
| connect | connect/userproficiency | 2 |  | child-uncatalogued | connect:ListUserProficiencies |
| connect | connect/workspaceassociation | 2 |  | child-uncatalogued | connect:SearchWorkspaceAssociations |
| connect | connect/workspacepage | 2 |  | child-uncatalogued | connect:ListWorkspacePages |
| controlcatalog | controlcatalog/commoncontrol | 0 |  | smithy-resource | controlcatalog:ListCommonControls |
| controlcatalog | controlcatalog/control | 0 |  | smithy-resource | controlcatalog:GetControl, controlcatalog:ListControls |
| controlcatalog | controlcatalog/controlmapping | 0 |  | element-arn | controlcatalog:ListControlMappings |
| controlcatalog | controlcatalog/domain | 0 |  | smithy-resource | controlcatalog:ListDomains |
| controlcatalog | controlcatalog/objective | 0 |  | smithy-resource | controlcatalog:ListObjectives |
| controltower | controltower/baseline | 0 |  | smithy-resource | controltower:GetBaseline, controltower:ListBaselines |
| controltower | controltower/controloperation | 0 |  | smithy-resource | controltower:GetControlOperation, controltower:ListControlOperations |
| controltower | controltower/landingzoneoperation | 0 |  | smithy-resource | controltower:GetLandingZoneOperation, controltower:ListLandingZoneOperations |
| controltower | controltower/tagging | 0 |  | smithy-resource | controltower:ListTagsForResource |
| cost-optimization-hub | cost-optimization-hub/recommendation | 0 |  | element-arn | cost-optimization-hub:ListRecommendations |
| databrew | databrew/jobrun | 1 |  | child-uncatalogued | databrew:DescribeJobRun, databrew:ListJobRuns |
| dataexchange | dataexchange/datasetrevision | 1 |  | child-uncatalogued | dataexchange:ListDataSetRevisions |
| dataexchange | dataexchange/job | 0 |  | sr-resource | dataexchange:GetJob, dataexchange:ListJobs |
| dataexchange | dataexchange/receiveddatagrant | 0 |  | element-written | dataexchange:GetReceivedDataGrant, dataexchange:ListReceivedDataGrants |
| dataexchange | dataexchange/revisionasset | 2 |  | child-uncatalogued | dataexchange:ListRevisionAssets |
| datapipeline | datapipeline/object | 1 |  | child-uncatalogued | datapipeline:DescribeObjects, datapipeline:QueryObjects |
| datapipeline | datapipeline/pipelinedefinition | 1 |  | child-uncatalogued | datapipeline:GetPipelineDefinition, datapipeline:ValidatePipelineDefinition |
| datasync | datasync/taskexecution | 0 |  | sr-resource | datasync:DescribeTaskExecution, datasync:ListTaskExecutions |
| datazone | datazone/accountpool | 1 |  | child-uncatalogued | datazone:GetAccountPool, datazone:ListAccountPools |
| datazone | datazone/accountsinaccountpool | 2 |  | child-uncatalogued | datazone:ListAccountsInAccountPool |
| datazone | datazone/assetfilter | 1 |  | child-uncatalogued | datazone:GetAssetFilter, datazone:ListAssetFilters |
| datazone | datazone/assetrevision | 1 |  | child-uncatalogued | datazone:ListAssetRevisions |
| datazone | datazone/dataproduct | 0 |  | smithy-resource | datazone:GetDataProduct, datazone:GetLineageNode, datazone:GetSubscriptionRequestDetails |
| datazone | datazone/dataproductrevision | 1 |  | child-uncatalogued | datazone:ListDataProductRevisions |
| datazone | datazone/datasourcerun | 0 |  | smithy-resource | datazone:GetDataSourceRun, datazone:ListDataSourceRuns |
| datazone | datazone/datasourcerunactivity | 1 |  | child-uncatalogued | datazone:ListDataSourceRunActivities |
| datazone | datazone/environmentblueprint | 1 |  | child-uncatalogued | datazone:GetEnvironmentBlueprint, datazone:ListEnvironmentBlueprints |
| datazone | datazone/getattributesmetadata | 3 |  | child-uncatalogued | datazone:BatchGetAttributesMetadata |
| datazone | datazone/jobrun | 2 |  | child-uncatalogued | datazone:GetJobRun, datazone:ListJobRuns |
| datazone | datazone/lineageevent | 1 |  | child-uncatalogued | datazone:GetLineageEvent, datazone:ListLineageEvents |
| datazone | datazone/lineagenodehistory | 2 |  | child-uncatalogued | datazone:ListLineageNodeHistory |
| datazone | datazone/metadatagenerationrun | 0 |  | smithy-resource | datazone:GetMetadataGenerationRun, datazone:ListMetadataGenerationRuns |
| datazone | datazone/notebook | 0 |  | smithy-resource | datazone:GetNotebook, datazone:ListNotebooks |
| datazone | datazone/notebookrun | 0 |  | smithy-resource | datazone:GetNotebookRun, datazone:ListNotebookRuns |
| datazone | datazone/notification | 1 |  | child-uncatalogued | datazone:ListNotifications |
| datazone | datazone/policygrant | 3 |  | child-uncatalogued | datazone:ListPolicyGrants |
| datazone | datazone/rule | 0 |  | smithy-resource | datazone:GetRule, datazone:ListRules |
| datazone | datazone/subscription | 1 |  | child-uncatalogued | datazone:GetSubscription, datazone:ListSubscriptions |
| datazone | datazone/subscriptiongrant | 1 |  | child-uncatalogued | datazone:GetSubscriptionGrant, datazone:ListSubscriptionGrants |
| datazone | datazone/subscriptionrequest | 1 |  | child-uncatalogued | datazone:ListSubscriptionRequests |
| datazone | datazone/timeseriesdatapoint | 3 |  | child-uncatalogued | datazone:GetTimeSeriesDataPoint, datazone:ListTimeSeriesDataPoints |
| deadline | deadline/farmmember | 1 |  | child-uncatalogued | deadline:ListFarmMembers |
| deadline | deadline/fleetmember | 2 |  | child-uncatalogued | deadline:ListFleetMembers |
| deadline | deadline/job | 2 |  | smithy-resource | deadline:BatchGetJob, deadline:GetJob, deadline:ListJobs, deadline:SearchJobs |
| deadline | deadline/jobmember | 3 |  | child-uncatalogued | deadline:ListJobMembers |
| deadline | deadline/jobparameterdefinition | 3 |  | child-uncatalogued | deadline:ListJobParameterDefinitions |
| deadline | deadline/queuemember | 2 |  | child-uncatalogued | deadline:ListQueueMembers |
| deadline | deadline/session | 3 |  | child-uncatalogued | deadline:BatchGetSession, deadline:GetSession, deadline:ListSessions, deadline:ListSessionsForWorker |
| deadline | deadline/sessionaction | 3 |  | child-uncatalogued | deadline:ListSessionActions |
| deadline | deadline/sessionsstatisticsaggregation | 1 |  | child-uncatalogued | deadline:GetSessionsStatisticsAggregation, deadline:StartSessionsStatisticsAggregation |
| deadline | deadline/step | 3 |  | child-uncatalogued | deadline:BatchGetStep, deadline:GetStep, deadline:ListSteps |
| deadline | deadline/stepconsumer | 3 |  | child-uncatalogued | deadline:ListStepConsumers |
| deadline | deadline/stepdependency | 3 |  | child-uncatalogued | deadline:ListStepDependencies |
| deadline | deadline/task | 3 |  | child-uncatalogued | deadline:ListTasks |
| deadline | deadline/worker | 2 |  | smithy-resource | deadline:BatchGetWorker, deadline:GetWorker, deadline:ListWorkers, deadline:SearchWorkers |
| detective | detective/getgraphmemberdatasource | 1 |  | child-uncatalogued | detective:BatchGetGraphMemberDatasources |
| detective | detective/investigation | 1 |  | child-uncatalogued | detective:GetInvestigation, detective:ListInvestigations |
| devicefarm | devicefarm/artifact | 1 |  | sr-resource | devicefarm:ListArtifacts |
| devicefarm | devicefarm/device | 0 |  | sr-resource | devicefarm:GetDevice, devicefarm:ListDevices |
| devicefarm | devicefarm/job | 0 |  | sr-resource | devicefarm:GetJob, devicefarm:ListJobs |
| devicefarm | devicefarm/offeringtransaction | 0 |  | element-written | devicefarm:ListOfferingTransactions |
| devicefarm | devicefarm/remoteaccesssession | 1 |  | child-uncatalogued | devicefarm:GetRemoteAccessSession, devicefarm:ListRemoteAccessSessions |
| devicefarm | devicefarm/run | 0 |  | sr-resource | devicefarm:GetRun, devicefarm:ListRuns |
| devicefarm | devicefarm/sample | 1 |  | sr-resource | devicefarm:ListSamples |
| devicefarm | devicefarm/suite | 0 |  | sr-resource | devicefarm:GetSuite, devicefarm:ListSuites |
| devicefarm | devicefarm/test | 0 |  | sr-resource | devicefarm:GetTest, devicefarm:ListTests |
| devicefarm | devicefarm/testgridsession | 0 |  | sr-resource | devicefarm:GetTestGridSession, devicefarm:ListTestGridSessions |
| devicefarm | devicefarm/uniqueproblem | 1 |  | child-uncatalogued | devicefarm:ListUniqueProblems |
| devicefarm | devicefarm/upload | 0 |  | sr-resource | devicefarm:GetUpload, devicefarm:ListUploads |
| devops-guru | devops-guru/anomalousloggroup | 1 |  | child-uncatalogued | devops-guru:ListAnomalousLogGroups |
| devops-guru | devops-guru/anomaly | 1 |  | child-uncatalogued | devops-guru:DescribeAnomaly, devops-guru:ListAnomaliesForInsight |
| devops-guru | devops-guru/event | 0 |  | element-written | devops-guru:ListEvents |
| devops-guru | devops-guru/insight | 0 |  | element-written | devops-guru:DescribeInsight, devops-guru:ListInsights, devops-guru:SearchInsights |
| devops-guru | devops-guru/monitoredresource | 0 |  | element-written | devops-guru:ListMonitoredResources |
| devops-guru | devops-guru/organizationinsight | 0 |  | element-written | devops-guru:ListOrganizationInsights, devops-guru:SearchOrganizationInsights |
| devops-guru | devops-guru/recommendation | 1 |  | child-uncatalogued | devops-guru:ListRecommendations |
| devops-guru | devops-guru/resourcecollectionhealth | 1 |  | child-uncatalogued | devops-guru:DescribeResourceCollectionHealth |
| directconnect | directconnect/directconnectgatewayassociationproposal | 0 |  | element-written | directconnect:DescribeDirectConnectGatewayAssociationProposals |
| directconnect | directconnect/interconnect | 0 |  | element-written | directconnect:DescribeInterconnects |
| directconnect | directconnect/resiliencygroup | 0 |  | element-written | directconnect:GetResiliencyGroup, directconnect:ListResiliencyGroups |
| directconnect | directconnect/resiliencygroupassociation | 1 |  | child-uncatalogued | directconnect:ListResiliencyGroupAssociations |
| directconnect | directconnect/virtualinterfacetesthistory | 0 |  | element-written | directconnect:ListVirtualInterfaceTestHistory |
| discovery | discovery/continuousexport | 0 |  | element-written | discovery:DescribeContinuousExports |
| discovery | discovery/importtask | 0 |  | element-written | discovery:DescribeImportTasks |
| discovery | discovery/serverneighbor | 1 |  | child-uncatalogued | discovery:ListServerNeighbors |
| dms | dms/connection | 0 |  | element-written | dms:DescribeConnections, dms:TestConnection |
| dms | dms/endpointsetting | 0 |  | child-uncatalogued | dms:DescribeEndpointSettings |
| dms | dms/fleetadvisorcollector | 0 |  | element-arn | dms:DescribeFleetAdvisorCollectors |
| dms | dms/metadatamodelchildren | 1 |  | child-uncatalogued | dms:DescribeMetadataModelChildren |
| dms | dms/metadatamodelimport | 1 |  | child-uncatalogued | dms:DescribeExtensionPackAssociations, dms:DescribeMetadataModelAssessments, dms:DescribeMetadataModelConversions, dms:DescribeMetadataModelCreations, dms:DescribeMetadataModelExportsAsScript, dms:DescribeMetadataModelExportsToTarget, dms:DescribeMetadataModelImports |
| dms | dms/pendingmaintenanceaction | 0 |  | element-written | dms:DescribePendingMaintenanceActions |
| dms | dms/recommendation | 0 |  | element-written | dms:DescribeRecommendations |
| dms | dms/replication | 0 |  | element-written | dms:DescribeReplications |
| dms | dms/replicationinstancetasklog | 1 |  | child-uncatalogued | dms:DescribeReplicationInstanceTaskLogs |
| dms | dms/replicationtaskassessmentresult | 0 |  | element-arn | dms:DescribeReplicationTaskAssessmentResults |
| dms | dms/replicationtaskassessmentrun | 0 |  | sr-resource | dms:DescribeReplicationTaskAssessmentRuns |
| dms | dms/replicationtaskindividualassessment | 0 |  | sr-resource | dms:DescribeReplicationTaskIndividualAssessments |
| dms | dms/schema | 1 |  | child-uncatalogued | dms:DescribeSchemas |
| dms | dms/tablestatistic | 1 |  | child-uncatalogued | dms:DescribeReplicationTableStatistics, dms:DescribeTableStatistics |
| docdb-elastic | docdb-elastic/pendingmaintenanceaction | 0 |  | element-written | docdb-elastic:GetPendingMaintenanceAction, docdb-elastic:ListPendingMaintenanceActions |
| drs | drs/extensiblesourceserver | 1 |  | child-uncatalogued | drs:ListExtensibleSourceServers |
| drs | drs/job | 0 |  | smithy-resource | drs:DescribeJobs |
| drs | drs/launchaction | 1 |  | child-uncatalogued | drs:ListLaunchActions |
| drs | drs/recoveryplan | 0 |  | element-written | drs:GetRecoveryPlan, drs:ListRecoveryPlans |
| drs | drs/recoveryplanexecution | 0 |  | element-written | drs:GetRecoveryPlanExecution, drs:ListRecoveryPlanExecutions |
| drs | drs/recoveryplanexecutionstep | 1 |  | child-uncatalogued | drs:GetRecoveryPlanExecutionStep, drs:ListRecoveryPlanExecutionSteps |
| drs | drs/recoveryplanstep | 1 |  | child-uncatalogued | drs:GetRecoveryPlanStep, drs:ListRecoveryPlanSteps |
| drs | drs/recoverysnapshot | 1 |  | child-uncatalogued | drs:DescribeRecoverySnapshots |
| drs | drs/replicationconfiguration | 1 |  | child-uncatalogued | drs:GetReplicationConfiguration |
| ds | ds/certificate | 1 |  | child-uncatalogued | ds:DescribeCertificate, ds:ListCertificates |
| ds | ds/domaincontroller | 1 |  | child-uncatalogued | ds:DescribeDomainControllers |
| ds | ds/eventtopic | 0 |  | element-arn | ds:DescribeEventTopics |
| ds | ds/iproute | 1 |  | child-uncatalogued | ds:ListIpRoutes |
| ds | ds/logsubscription | 0 |  | element-created | ds:ListLogSubscriptions |
| ds | ds/region | 1 |  | child-uncatalogued | ds:DescribeRegions |
| ds | ds/schemaextension | 1 |  | child-uncatalogued | ds:ListSchemaExtensions |
| ds | ds/shareddirectory | 1 |  | child-uncatalogued | ds:DescribeSharedDirectories |
| ds | ds/snapshot | 0 |  | element-written | ds:DescribeSnapshots |
| ds | ds/trust | 0 |  | element-written | ds:DescribeTrusts, ds:VerifyTrust |
| ds-data | ds-data/group | 1 |  | child-uncatalogued | ds-data:ListGroups, ds-data:ListGroupsForMember, ds-data:SearchGroups |
| ds-data | ds-data/groupmember | 1 |  | child-uncatalogued | ds-data:ListGroupMembers |
| ds-data | ds-data/user | 1 |  | child-uncatalogued | ds-data:ListUsers, ds-data:SearchUsers |
| dynamodb | dynamodb/export | 0 |  | sr-resource | dynamodb:DescribeExport, dynamodb:ListExports |
| dynamodb | dynamodb/globaltablesetting | 1 |  | child-uncatalogued | dynamodb:DescribeGlobalTableSettings |
| dynamodb | dynamodb/import | 0 |  | sr-resource | dynamodb:DescribeImport, dynamodb:ListImports |
| dynamodb | dynamodb/item | 1 |  | child-uncatalogued | dynamodb:BatchGetItem, dynamodb:GetItem, dynamodb:TransactGetItems |
| ec2 | ec2/addressesattribute | 0 |  | element-written | ec2:DescribeAddressesAttribute |
| ec2 | ec2/addresstransfer | 0 |  | element-written | ec2:DescribeAddressTransfers |
| ec2 | ec2/allowedimagessetting | 0 |  | element-written | ec2:GetAllowedImagesSettings |
| ec2 | ec2/applicationstatuscheck | 0 |  | sr-resource | ec2:DescribeApplicationStatusChecks |
| ec2 | ec2/awsnetworkperformancedata | 0 |  | element-written | ec2:GetAwsNetworkPerformanceData |
| ec2 | ec2/bundletask | 0 |  | element-written | ec2:DescribeBundleTasks |
| ec2 | ec2/byoipcidr | 0 |  | element-written | ec2:DescribeByoipCidrs |
| ec2 | ec2/capacityblockextensionhistory | 0 |  | element-written | ec2:DescribeCapacityBlockExtensionHistory |
| ec2 | ec2/capacityblockextensionoffering | 1 |  | child-uncatalogued | ec2:DescribeCapacityBlockExtensionOfferings |
| ec2 | ec2/capacityblockoffering | 0 |  | element-written | ec2:DescribeCapacityBlockOfferings |
| ec2 | ec2/capacitymanagermetricdimension | 0 |  | child-uncatalogued | ec2:GetCapacityManagerMetricDimensions |
| ec2 | ec2/capacitymanagermonitoredtagkey | 0 |  | element-written | ec2:GetCapacityManagerMonitoredTagKeys |
| ec2 | ec2/capacityreservationbillingrequest | 0 |  | element-written | ec2:DescribeCapacityReservationBillingRequests |
| ec2 | ec2/capacityreservationcancellationquote | 0 |  | sr-resource | ec2:DescribeCapacityReservationCancellationQuotes |
| ec2 | ec2/capacityreservationtopology | 0 |  | element-written | ec2:DescribeCapacityReservationTopology |
| ec2 | ec2/clientvpnconnection | 1 |  | child-uncatalogued | ec2:DescribeClientVpnConnections |
| ec2 | ec2/conversiontask | 0 |  | element-written | ec2:DescribeConversionTasks |
| ec2 | ec2/declarativepoliciesreport | 0 |  | sr-resource | ec2:DescribeDeclarativePoliciesReports |
| ec2 | ec2/elasticgpu | 0 |  | sr-resource | ec2:DescribeElasticGpus |
| ec2 | ec2/exportimagetask | 0 |  | sr-resource | ec2:DescribeExportImageTasks |
| ec2 | ec2/exporttask | 0 |  | element-written | ec2:DescribeExportTasks |
| ec2 | ec2/fastlaunchimage | 0 |  | element-written | ec2:DescribeFastLaunchImages |
| ec2 | ec2/fastsnapshotrestore | 0 |  | element-written | ec2:DescribeFastSnapshotRestores |
| ec2 | ec2/fleetinstance | 1 |  | child-uncatalogued | ec2:DescribeFleetInstances, ec2:DescribeSpotFleetInstances |
| ec2 | ec2/fpgaimageattribute | 1 |  | child-uncatalogued | ec2:DescribeFpgaImageAttribute |
| ec2 | ec2/group | 1 |  | child-uncatalogued | ec2:GetGroupsForCapacityReservation |
| ec2 | ec2/hostreservationpurchasepreview | 0 |  | child-uncatalogued | ec2:GetHostReservationPurchasePreview |
| ec2 | ec2/iaminstanceprofileassociation | 0 |  | element-written | ec2:DescribeIamInstanceProfileAssociations |
| ec2 | ec2/idformat | 0 |  | child-uncatalogued | ec2:DescribeAggregateIdFormat, ec2:DescribeIdFormat, ec2:DescribeIdentityIdFormat |
| ec2 | ec2/imageattribute | 1 |  | child-uncatalogued | ec2:DescribeImageAttribute |
| ec2 | ec2/imagereference | 1 |  | child-uncatalogued | ec2:DescribeImageReferences |
| ec2 | ec2/imagesinrecyclebin | 0 |  | element-written | ec2:ListImagesInRecycleBin |
| ec2 | ec2/imageusagereport | 0 |  | sr-resource | ec2:DescribeImageUsageReports |
| ec2 | ec2/imageusagereportentry | 0 |  | element-written | ec2:DescribeImageUsageReportEntries |
| ec2 | ec2/importimagetask | 0 |  | sr-resource | ec2:DescribeImportImageTasks |
| ec2 | ec2/importsnapshottask | 0 |  | sr-resource | ec2:DescribeImportSnapshotTasks |
| ec2 | ec2/instanceattribute | 1 |  | child-uncatalogued | ec2:DescribeInstanceAttribute, ec2:DescribeNetworkInterfaceAttribute |
| ec2 | ec2/instanceimagemetadata | 0 |  | element-written | ec2:DescribeInstanceImageMetadata |
| ec2 | ec2/instancesqlhastate | 0 |  | element-written | ec2:DescribeInstanceSqlHaHistoryStates, ec2:DescribeInstanceSqlHaStates |
| ec2 | ec2/instancestatus | 0 |  | element-written | ec2:DescribeInstanceStatus |
| ec2 | ec2/ipamaddresshistory | 1 |  | child-uncatalogued | ec2:GetIpamAddressHistory |
| ec2 | ec2/ipambyoasn | 0 |  | element-written | ec2:DescribeIpamByoasn |
| ec2 | ec2/ipamdiscoveredaccount | 1 |  | child-uncatalogued | ec2:GetIpamDiscoveredAccounts |
| ec2 | ec2/ipamdiscoveredresourcecidr | 1 |  | child-uncatalogued | ec2:GetIpamDiscoveredResourceCidrs |
| ec2 | ec2/ipamdiscoveredroute | 1 |  | child-uncatalogued | ec2:GetIpamDiscoveredRoutes |
| ec2 | ec2/ipaminternetregistryassociation | 0 |  | sr-resource | ec2:DescribeIpamInternetRegistryAssociations |
| ec2 | ec2/ipampolicyallocationrule | 1 |  | child-uncatalogued | ec2:GetIpamPolicyAllocationRules |
| ec2 | ec2/ipampolicyorganizationtarget | 1 |  | child-uncatalogued | ec2:GetIpamPolicyOrganizationTargets |
| ec2 | ec2/ipamprefixlistresolverrule | 1 |  | child-uncatalogued | ec2:GetIpamPrefixListResolverRules |
| ec2 | ec2/ipamresourcecidr | 1 |  | child-uncatalogued | ec2:GetIpamResourceCidrs |
| ec2 | ec2/ipamrouteprotectionfinding | 1 |  | child-uncatalogued | ec2:GetIpamRouteProtectionFindings |
| ec2 | ec2/ipamroutingpolicyregistration | 1 |  | child-uncatalogued | ec2:GetIpamRoutingPolicyRegistrations |
| ec2 | ec2/ipamroutingpolicyregistrationdelta | 1 |  | child-uncatalogued | ec2:GetIpamRoutingPolicyRegistrationDeltas |
| ec2 | ec2/lockedsnapshot | 0 |  | element-written | ec2:DescribeLockedSnapshots |
| ec2 | ec2/macmodificationtask | 0 |  | sr-resource | ec2:DescribeMacModificationTasks |
| ec2 | ec2/managedprefixlistassociation | 1 |  | child-uncatalogued | ec2:GetManagedPrefixListAssociations |
| ec2 | ec2/networkinsightsaccessscopeanalysisfinding | 1 |  | child-uncatalogued | ec2:GetNetworkInsightsAccessScopeAnalysisFindings |
| ec2 | ec2/principalidformat | 0 |  | element-arn | ec2:DescribePrincipalIdFormat |
| ec2 | ec2/replacerootvolumetask | 0 |  | sr-resource | ec2:DescribeReplaceRootVolumeTasks |
| ec2 | ec2/reservedinstanceslisting | 0 |  | element-written | ec2:DescribeReservedInstancesListings |
| ec2 | ec2/reservedinstancesmodification | 0 |  | element-created | ec2:DescribeReservedInstancesModifications |
| ec2 | ec2/routeserverassociation | 1 |  | child-uncatalogued | ec2:GetRouteServerAssociations |
| ec2 | ec2/routeserverpropagation | 1 |  | child-uncatalogued | ec2:GetRouteServerPropagations |
| ec2 | ec2/scheduledinstance | 0 |  | element-written | ec2:DescribeScheduledInstances |
| ec2 | ec2/scheduledinstanceavailability | 0 |  | element-written | ec2:DescribeScheduledInstanceAvailability |
| ec2 | ec2/securitygroupreference | 1 |  | child-uncatalogued | ec2:DescribeSecurityGroupReferences |
| ec2 | ec2/securitygrouprule | 0 |  | sr-resource | ec2:DescribeSecurityGroupRules |
| ec2 | ec2/servicelinkvirtualinterface | 0 |  | element-written | ec2:DescribeServiceLinkVirtualInterfaces |
| ec2 | ec2/snapshotattribute | 1 |  | child-uncatalogued | ec2:DescribeSnapshotAttribute |
| ec2 | ec2/snapshotsinrecyclebin | 0 |  | element-written | ec2:ListSnapshotsInRecycleBin |
| ec2 | ec2/snapshottierstatus | 0 |  | element-written | ec2:DescribeSnapshotTierStatus |
| ec2 | ec2/stalesecuritygroup | 1 |  | child-uncatalogued | ec2:DescribeStaleSecurityGroups |
| ec2 | ec2/subnetcidrreservation | 1 |  | sr-resource | ec2:GetSubnetCidrReservations |
| ec2 | ec2/transitgatewayattachmentpropagation | 1 |  | child-uncatalogued | ec2:GetTransitGatewayAttachmentPropagations |
| ec2 | ec2/transitgatewaypolicytableassociation | 1 |  | child-uncatalogued | ec2:GetTransitGatewayPolicyTableAssociations |
| ec2 | ec2/transitgatewaypolicytableentry | 1 |  | child-uncatalogued | ec2:GetTransitGatewayPolicyTableEntries |
| ec2 | ec2/transitgatewayprefixlistreference | 1 |  | child-uncatalogued | ec2:GetTransitGatewayPrefixListReferences |
| ec2 | ec2/trunkinterfaceassociation | 0 |  | element-written | ec2:DescribeTrunkInterfaceAssociations |
| ec2 | ec2/verifiedaccessendpointtarget | 1 |  | sr-resource | ec2:GetVerifiedAccessEndpointTargets |
| ec2 | ec2/verifiedaccessinstanceloggingconfiguration | 0 |  | element-written | ec2:DescribeVerifiedAccessInstanceLoggingConfigurations |
| ec2 | ec2/volumeattribute | 1 |  | child-uncatalogued | ec2:DescribeVolumeAttribute |
| ec2 | ec2/volumesinrecyclebin | 0 |  | element-written | ec2:ListVolumesInRecycleBin |
| ec2 | ec2/volumesmodification | 0 |  | element-written | ec2:DescribeVolumesModifications |
| ec2 | ec2/volumestatus | 0 |  | element-written | ec2:DescribeVolumeStatus |
| ec2 | ec2/vpcendpointassociation | 0 |  | element-written | ec2:DescribeVpcEndpointAssociations |
| ec2 | ec2/vpcendpointconnection | 0 |  | sr-resource | ec2:DescribeVpcEndpointConnections |
| ec2 | ec2/vpcendpointserviceconfiguration | 0 |  | element-written | ec2:DescribeVpcEndpointServiceConfigurations |
| ec2 | ec2/vpcresourcesblockingencryptionenforcement | 1 |  | child-uncatalogued | ec2:GetVpcResourcesBlockingEncryptionEnforcement |
| ec2 | ec2/vpnconnectiondevicetype | 0 |  | sr-resource | ec2:GetVpnConnectionDeviceTypes |
| ecr | ecr/getrepositoryscanningconfiguration | 1 |  | child-uncatalogued | ecr:BatchGetRepositoryScanningConfiguration |
| ecr | ecr/image | 1 |  | child-uncatalogued | ecr:DescribeImageReplicationStatus, ecr:DescribeImageSigningStatus, ecr:DescribeImages, ecr:ListImages |
| ecr-public | ecr-public/image | 1 |  | child-uncatalogued | ecr-public:DescribeImages |
| ecr-public | ecr-public/registry | 0 |  | sr-resource | ecr-public:DescribeRegistries |
| ecs | ecs/accountsetting | 0 |  | element-written | ecs:ListAccountSettings |
| ecs | ecs/daemondeployment | 2 |  | smithy-resource | ecs:DescribeDaemonDeployments |
| ecs | ecs/daemonrevision | 2 |  | smithy-resource | ecs:DescribeDaemonRevisions |
| ecs | ecs/servicedeployment | 2 |  | smithy-resource | ecs:DescribeServiceDeployments |
| ecs | ecs/servicerevision | 2 |  | smithy-resource | ecs:DescribeServiceRevisions |
| eks | eks/accesspolicy | 0 |  | sr-resource | eks:ListAccessPolicies |
| eks | eks/addonversion | 0 |  | element-written | eks:DescribeAddonVersions |
| eks | eks/associatedaccesspolicy | 2 |  | child-uncatalogued | eks:ListAssociatedAccessPolicies |
| eks | eks/certificateauthority | 1 |  | child-uncatalogued | eks:DescribeCertificateAuthority, eks:ListCertificateAuthorities |
| eks | eks/clusterversion | 0 |  | element-written | eks:DescribeClusterVersions |
| eks | eks/insight | 1 |  | child-uncatalogued | eks:DescribeInsight, eks:ListInsights |
| elasticache | elasticache/cacheparameter | 1 |  | child-uncatalogued | elasticache:DescribeCacheParameters, elasticache:DescribeEngineDefaultParameters |
| elasticache | elasticache/reservedcachenodesoffering | 0 |  | element-written | elasticache:DescribeReservedCacheNodesOfferings |
| elasticbeanstalk | elasticbeanstalk/configurationsetting | 2 |  | child-uncatalogued | elasticbeanstalk:DescribeConfigurationSettings |
| elasticbeanstalk | elasticbeanstalk/environmentmanagedactionhistory | 0 |  | element-written | elasticbeanstalk:DescribeEnvironmentManagedActionHistory |
| elasticbeanstalk | elasticbeanstalk/event | 0 |  | element-arn | elasticbeanstalk:DescribeEvents |
| elasticbeanstalk | elasticbeanstalk/instanceshealth | 0 |  | element-written | elasticbeanstalk:DescribeInstancesHealth |
| elasticfilesystem | elasticfilesystem/replicationconfiguration | 0 |  | element-written | elasticfilesystem:DescribeReplicationConfigurations |
| elasticloadbalancing | elasticloadbalancing/truststoreassociation | 1 |  | child-uncatalogued | elasticloadbalancing:DescribeTrustStoreAssociations |
| elasticmapreduce | elasticmapreduce/bootstrapaction | 1 |  | child-uncatalogued | elasticmapreduce:ListBootstrapActions |
| elasticmapreduce | elasticmapreduce/instance | 1 |  | child-uncatalogued | elasticmapreduce:ListInstances |
| elasticmapreduce | elasticmapreduce/jobflow | 0 |  | element-written | elasticmapreduce:DescribeJobFlows |
| elasticmapreduce | elasticmapreduce/notebookexecution | 0 |  | sr-resource | elasticmapreduce:DescribeNotebookExecution, elasticmapreduce:ListNotebookExecutions |
| elasticmapreduce | elasticmapreduce/session | 1 |  | sr-resource | elasticmapreduce:GetSession, elasticmapreduce:ListSessions |
| elemental-inference | elemental-inference/dictionary | 0 |  | smithy-resource | elemental-inference:GetDictionary, elemental-inference:ListDictionaries |
| elemental-inference | elemental-inference/fixture | 0 |  | child-uncatalogued | elemental-inference:GetFixture, elemental-inference:SearchFixtures |
| emr-containers | emr-containers/jobrun | 1 |  | sr-resource | emr-containers:DescribeJobRun, emr-containers:ListJobRuns |
| emr-serverless | emr-serverless/jobrun | 1 |  | smithy-resource | emr-serverless:GetJobRun, emr-serverless:ListJobRuns |
| emr-serverless | emr-serverless/jobrunattempt | 2 |  | child-uncatalogued | emr-serverless:ListJobRunAttempts |
| emr-serverless | emr-serverless/session | 1 |  | smithy-resource | emr-serverless:GetSession, emr-serverless:ListSessions |
| entityresolution | entityresolution/matchingjob | 1 |  | child-uncatalogued | entityresolution:GetMatchingJob, entityresolution:ListIdMappingJobs, entityresolution:ListMatchingJobs |
| entityresolution | entityresolution/providerservice | 0 |  | sr-resource | entityresolution:GetProviderService, entityresolution:ListProviderServices |
| es | es/datasourceattachment | 1 |  | child-uncatalogued | es:DescribeDataSourceAttachment, es:ListDataSourceAttachments |
| es | es/directquerydatasource | 0 |  | element-written | es:GetDirectQueryDataSource, es:ListDirectQueryDataSources |
| es | es/domainmaintenance | 1 |  | child-uncatalogued | es:ListDomainMaintenances |
| es | es/elasticsearchdomain | 1 |  | child-uncatalogued | es:DescribeElasticsearchDomain, es:DescribeElasticsearchDomains |
| es | es/elasticsearchinstancetype | 1 |  | child-uncatalogued | es:ListElasticsearchInstanceTypes |
| es | es/inboundconnection | 0 |  | element-written | es:DescribeInboundConnections |
| es | es/inboundcrossclustersearchconnection | 0 |  | element-written | es:DescribeInboundCrossClusterSearchConnections |
| es | es/insight | 1 |  | child-uncatalogued | es:ListInsights |
| es | es/migration | 0 |  | child-uncatalogued | es:GetMigration, es:ListMigrations |
| es | es/outboundconnection | 0 |  | element-written | es:DescribeOutboundConnections |
| es | es/outboundcrossclustersearchconnection | 0 |  | element-written | es:DescribeOutboundCrossClusterSearchConnections |
| es | es/package | 0 |  | element-written | es:DescribePackages |
| es | es/reservedinstance | 0 |  | element-written | es:DescribeReservedInstances |
| es | es/scheduledaction | 1 |  | child-uncatalogued | es:ListScheduledActions |
| es | es/upgradehistory | 1 |  | child-uncatalogued | es:GetUpgradeHistory |
| es | es/vpcendpoint | 0 |  | element-written | es:DescribeVpcEndpoints, es:ListVpcEndpoints, es:ListVpcEndpointsForDomain |
| events | events/partnereventsource | 0 |  | element-arn | events:DescribePartnerEventSource, events:ListPartnerEventSources |
| events | events/replay | 0 |  | sr-resource | events:DescribeReplay, events:ListReplays |
| events | events/rulename | 3 |  | child-uncatalogued | events:ListRuleNamesByTarget |
| evs | evs/accountsetting | 0 |  | element-written | evs:GetAccountSettings |
| evs | evs/environmentconnector | 1 |  | child-uncatalogued | evs:ListEnvironmentConnectors |
| evs | evs/environmenthost | 1 |  | child-uncatalogued | evs:ListEnvironmentHosts |
| evs | evs/environmentvlan | 1 |  | child-uncatalogued | evs:ListEnvironmentVlans |
| evs | evs/vmentitlement | 1 |  | child-uncatalogued | evs:ListVmEntitlements |
| execute-api | execute-api/transcript | 0 |  | element-written | execute-api:GetTranscript |
| finspace | finspace/kxchangeset | 2 |  | child-uncatalogued | finspace:GetKxChangeset, finspace:ListKxChangesets |
| finspace | finspace/kxclusternode | 2 |  | child-uncatalogued | finspace:ListKxClusterNodes |
| finspace-api | finspace-api/changeset | 1 |  | child-uncatalogued | finspace-api:GetChangeset, finspace-api:ListChangesets |
| finspace-api | finspace-api/dataset | 0 |  | element-written | finspace-api:GetDataset, finspace-api:ListDatasets |
| finspace-api | finspace-api/dataview | 1 |  | child-uncatalogued | finspace-api:GetDataView, finspace-api:ListDataViews |
| finspace-api | finspace-api/permissiongroup | 0 |  | element-written | finspace-api:GetPermissionGroup, finspace-api:ListPermissionGroups, finspace-api:ListPermissionGroupsByUser |
| finspace-api | finspace-api/user | 0 |  | element-written | finspace-api:GetUser, finspace-api:ListUsers, finspace-api:ListUsersByPermissionGroup |
| fis | fis/action | 0 |  | sr-resource | fis:GetAction, fis:ListActions |
| fis | fis/experiment | 0 |  | sr-resource | fis:GetExperiment, fis:ListExperiments |
| fis | fis/experimentresolvedtarget | 1 |  | child-uncatalogued | fis:ListExperimentResolvedTargets |
| fis | fis/experimenttargetaccountconfiguration | 1 |  | child-uncatalogued | fis:GetExperimentTargetAccountConfiguration, fis:ListExperimentTargetAccountConfigurations |
| fms | fms/compliancestatus | 1 |  | child-uncatalogued | fms:ListComplianceStatus |
| fms | fms/discoveredresource | 1 |  | child-uncatalogued | fms:ListDiscoveredResources |
| fms | fms/resourcesetresource | 1 |  | child-uncatalogued | fms:ListResourceSetResources |
| forecast | forecast/datasetimportjob | 0 |  | sr-resource | forecast:DescribeDatasetImportJob, forecast:ListDatasetImportJobs |
| forecast | forecast/explainabilityexport | 0 |  | sr-resource | forecast:DescribeExplainabilityExport, forecast:DescribeForecastExportJob, forecast:ListExplainabilityExports |
| forecast | forecast/forecastexportjob | 0 |  | element-written | forecast:ListForecastExportJobs |
| forecast | forecast/monitorevaluation | 1 |  | child-uncatalogued | forecast:ListMonitorEvaluations |
| forecast | forecast/predictorbacktestexportjob | 0 |  | sr-resource | forecast:DescribePredictorBacktestExportJob, forecast:ListPredictorBacktestExportJobs |
| forecast | forecast/whatifforecastexport | 0 |  | sr-resource | forecast:DescribeWhatIfForecastExport, forecast:ListWhatIfForecastExports |
| frauddetector | frauddetector/batchimportjob | 0 |  | element-arn | frauddetector:GetBatchImportJobs |
| frauddetector | frauddetector/batchpredictionjob | 0 |  | element-written | frauddetector:GetBatchPredictionJobs |
| frauddetector | frauddetector/detectorversion | 0 |  | sr-resource | frauddetector:GetDetectorVersion |
| frauddetector | frauddetector/eventprediction | 0 |  | child-uncatalogued | frauddetector:GetEventPrediction, frauddetector:ListEventPredictions |
| frauddetector | frauddetector/eventpredictionmetadata | 1 |  | child-uncatalogued | frauddetector:GetEventPredictionMetadata |
| frauddetector | frauddetector/modelversion | 0 |  | sr-resource | frauddetector:DescribeModelVersions, frauddetector:GetModelVersion |
| fsx | fsx/datarepositorytask | 0 |  | element-written | fsx:DescribeDataRepositoryTasks |
| fsx | fsx/filesystemalias | 1 |  | child-uncatalogued | fsx:DescribeFileSystemAliases |
| gamelift | gamelift/compute | 1 |  | child-uncatalogued | gamelift:DescribeCompute, gamelift:ListCompute |
| gamelift | gamelift/fleetcapacity | 0 |  | element-written | gamelift:DescribeFleetCapacity, gamelift:DescribeFleetLocationCapacity |
| gamelift | gamelift/fleetdeployment | 0 |  | element-written | gamelift:DescribeFleetDeployment, gamelift:ListFleetDeployments |
| gamelift | gamelift/fleetevent | 1 |  | child-uncatalogued | gamelift:DescribeFleetEvents |
| gamelift | gamelift/fleetutilization | 0 |  | element-written | gamelift:DescribeFleetLocationUtilization, gamelift:DescribeFleetUtilization |
| gamelift | gamelift/gameserver | 1 |  | child-uncatalogued | gamelift:DescribeGameServer, gamelift:ListGameServers |
| gamelift | gamelift/gameserverinstance | 1 |  | child-uncatalogued | gamelift:DescribeGameServerInstances |
| gamelift | gamelift/gamesession | 0 |  | element-written | gamelift:DescribeGameSessions, gamelift:SearchGameSessions |
| gamelift | gamelift/gamesessiondetail | 0 |  | element-written | gamelift:DescribeGameSessionDetails |
| gamelift | gamelift/instance | 1 |  | child-uncatalogued | gamelift:DescribeInstances |
| gamelift | gamelift/matchmaking | 0 |  | child-uncatalogued | gamelift:DescribeMatchmaking |
| gamelift | gamelift/playersession | 0 |  | element-written | gamelift:DescribePlayerSessions |
| gamelift | gamelift/scalingpolicy | 1 |  | child-uncatalogued | gamelift:DescribeScalingPolicies |
| gamelift | gamelift/vpcpeeringauthorization | 0 |  | element-written | gamelift:DescribeVpcPeeringAuthorizations |
| gamelift | gamelift/vpcpeeringconnection | 0 |  | element-arn | gamelift:DescribeVpcPeeringConnections |
| gameliftstreams | gameliftstreams/applicationshadercache | 1 |  | child-uncatalogued | gameliftstreams:ListApplicationShaderCaches |
| gameliftstreams | gameliftstreams/streamsession | 1 |  | element-written | gameliftstreams:GetStreamSession, gameliftstreams:ListStreamSessions, gameliftstreams:ListStreamSessionsByAccount |
| gameliftstreams | gameliftstreams/streamurl | 0 |  | element-written | gameliftstreams:GetStreamUrl, gameliftstreams:ListStreamUrls |
| geo | geo/deviceposition | 1 |  | child-uncatalogued | geo:GetDevicePosition, geo:ListDevicePositions, geo:VerifyDevicePosition |
| geo | geo/devicepositionhistory | 1 |  | child-uncatalogued | geo:GetDevicePositionHistory |
| geo | geo/geofence | 1 |  | child-uncatalogued | geo:GetGeofence, geo:ListGeofences |
| geo | geo/geofenceevent | 1 |  | child-uncatalogued | geo:ForecastGeofenceEvents |
| geo | geo/job | 0 |  | smithy-resource | geo:GetJob, geo:ListJobs |
| geo-places | geo-places/text | 1 |  | child-uncatalogued | geo-places:SearchText |
| glacier | glacier/job | 1 |  | child-uncatalogued | glacier:DescribeJob, glacier:ListJobs |
| glacier | glacier/multipartupload | 1 |  | child-uncatalogued | glacier:ListMultipartUploads |
| globalaccelerator | globalaccelerator/byoipcidr | 0 |  | element-written | globalaccelerator:ListByoipCidrs |
| globalaccelerator | globalaccelerator/crossaccountresource | 0 |  | child-uncatalogued | globalaccelerator:ListCrossAccountResources |
| globalaccelerator | globalaccelerator/customroutingaccelerator | 0 |  | element-written | globalaccelerator:DescribeCustomRoutingAccelerator, globalaccelerator:ListCustomRoutingAccelerators |
| globalaccelerator | globalaccelerator/customroutingendpointgroup | 2 |  | child-uncatalogued | globalaccelerator:DescribeCustomRoutingEndpointGroup, globalaccelerator:ListCustomRoutingEndpointGroups |
| globalaccelerator | globalaccelerator/customroutinglistener | 1 |  | child-uncatalogued | globalaccelerator:DescribeCustomRoutingListener, globalaccelerator:ListCustomRoutingListeners |
| globalaccelerator | globalaccelerator/customroutingportmapping | 1 |  | child-uncatalogued | globalaccelerator:ListCustomRoutingPortMappings |
| globalaccelerator | globalaccelerator/customroutingportmappingsbydestination | 0 |  | child-uncatalogued | globalaccelerator:ListCustomRoutingPortMappingsByDestination |
| glue | glue/asset | 0 |  | child-uncatalogued | glue:GetAsset |
| glue | glue/assettype | 0 |  | child-uncatalogued | glue:GetAssetType, glue:ListAssetTypes |
| glue | glue/blueprintrun | 1 |  | child-uncatalogued | glue:GetBlueprintRun, glue:GetBlueprintRuns |
| glue | glue/columnstatisticstaskrun | 0 |  | child-uncatalogued | glue:GetColumnStatisticsTaskRun, glue:GetColumnStatisticsTaskRuns, glue:ListColumnStatisticsTaskRuns |
| glue | glue/crawl | 1 |  | child-uncatalogued | glue:ListCrawls |
| glue | glue/dataflowgraph | 0 |  | element-written | glue:GetDataflowGraph |
| glue | glue/dataqualityresult | 0 |  | element-written | glue:BatchGetDataQualityResult, glue:GetDataQualityResult, glue:ListDataQualityResults |
| glue | glue/dataqualityrulerecommendationrun | 0 |  | element-written | glue:GetDataQualityRuleRecommendationRun, glue:ListDataQualityRuleRecommendationRuns |
| glue | glue/dataqualityrulesetevaluationrun | 0 |  | element-written | glue:BatchGetDataQualityRulesetEvaluationRun, glue:GetDataQualityRulesetEvaluationRun, glue:ListDataQualityRulesetEvaluationRuns |
| glue | glue/glossaryterm | 1 |  | child-uncatalogued | glue:GetGlossaryTerm, glue:ListGlossaryTerms |
| glue | glue/inboundintegration | 0 |  | element-written | glue:DescribeInboundIntegrations |
| glue | glue/integrationtableproperty | 0 |  | element-written | glue:GetIntegrationTableProperties, glue:ListIntegrationTableProperties |
| glue | glue/iterableform | 0 |  | child-uncatalogued | glue:ListIterableForms |
| glue | glue/jobrun | 1 |  | child-uncatalogued | glue:GetJobRun, glue:GetJobRuns |
| glue | glue/mltaskrun | 1 |  | child-uncatalogued | glue:GetMLTaskRun, glue:GetMLTaskRuns |
| glue | glue/resourcepolicy | 0 |  | element-created | glue:GetResourcePolicies, glue:GetResourcePolicy |
| glue | glue/session | 0 |  | sr-resource | glue:GetSession, glue:ListSessions |
| glue | glue/statement | 1 |  | child-uncatalogued | glue:GetStatement, glue:ListStatements |
| glue | glue/tableversion | 1 |  | sr-resource | glue:GetTableVersion, glue:GetTableVersions |
| glue | glue/workflowrun | 1 |  | child-uncatalogued | glue:GetWorkflowRun, glue:GetWorkflowRuns |
| grafana | grafana/permission | 0 |  | smithy-resource | grafana:ListPermissions |
| grafana | grafana/workspaceserviceaccount | 1 |  | child-uncatalogued | grafana:ListWorkspaceServiceAccounts |
| grafana | grafana/workspaceserviceaccounttoken | 1 |  | child-uncatalogued | grafana:ListWorkspaceServiceAccountTokens |
| greengrass | greengrass/bulkdeployment | 0 |  | sr-resource | greengrass:ListBulkDeployments |
| greengrass | greengrass/clientdevicesassociatedwithcoredevice | 1 |  | child-uncatalogued | greengrass:ListClientDevicesAssociatedWithCoreDevice |
| greengrass | greengrass/componentcandidate | 0 |  | element-written | greengrass:ResolveComponentCandidates |
| greengrass | greengrass/connectivityinfo | 1 |  | sr-resource | greengrass:GetConnectivityInfo |
| greengrass | greengrass/effectivedeployment | 1 |  | child-uncatalogued | greengrass:ListEffectiveDeployments |
| greengrass | greengrass/groupcertificateauthority | 1 |  | child-uncatalogued | greengrass:GetGroupCertificateAuthority, greengrass:ListGroupCertificateAuthorities |
| greengrass | greengrass/installedcomponent | 1 |  | child-uncatalogued | greengrass:ListInstalledComponents |
| groundstation | groundstation/antenna | 1 |  | child-uncatalogued | groundstation:ListAntennas |
| groundstation | groundstation/contact | 0 |  | smithy-resource | groundstation:DescribeContact, groundstation:ListContacts |
| groundstation | groundstation/contactversion | 1 |  | child-uncatalogued | groundstation:DescribeContactVersion, groundstation:ListContactVersions |
| groundstation | groundstation/ephemeris | 0 |  | smithy-resource | groundstation:ListEphemerides |
| groundstation | groundstation/groundstation | 0 |  | smithy-resource | groundstation:ListGroundStations |
| groundstation | groundstation/groundstationreservation | 1 |  | child-uncatalogued | groundstation:ListGroundStationReservations |
| groundstation | groundstation/satellite | 0 |  | smithy-resource | groundstation:GetSatellite, groundstation:ListSatellites |
| guardduty | guardduty/coverage | 1 |  | child-uncatalogued | guardduty:ListCoverage |
| guardduty | guardduty/customdetectionrule | 0 |  | sr-resource | guardduty:GetCustomDetectionRule, guardduty:ListCustomDetectionRules |
| guardduty | guardduty/customdetectionruleassociation | 0 |  | sr-resource | guardduty:GetCustomDetectionRuleAssociation, guardduty:ListCustomDetectionRuleAssociations |
| guardduty | guardduty/customdetectionruleorgconfiguration | 0 |  | element-written | guardduty:GetCustomDetectionRuleOrgConfiguration, guardduty:ListCustomDetectionRuleOrgConfigurations |
| guardduty | guardduty/finding | 1 |  | child-uncatalogued | guardduty:ListFindings |
| guardduty | guardduty/investigation | 1 |  | child-uncatalogued | guardduty:GetInvestigation, guardduty:ListInvestigations |
| guardduty | guardduty/malwarescan | 0 |  | element-arn | guardduty:DescribeMalwareScans, guardduty:GetMalwareScan, guardduty:ListMalwareScans |
| guardduty | guardduty/organizationconfiguration | 1 |  | child-uncatalogued | guardduty:DescribeOrganizationConfiguration |
| health | health/affectedaccountsfororganization | 1 |  | child-uncatalogued | health:DescribeAffectedAccountsForOrganization |
| health | health/affectedentity | 1 |  | element-arn | health:DescribeAffectedEntities, health:DescribeAffectedEntitiesForOrganization |
| health | health/entityaggregate | 0 |  | element-arn | health:DescribeEntityAggregates |
| health | health/event | 0 |  | sr-resource | health:DescribeEvents |
| health | health/eventsfororganization | 0 |  | element-arn | health:DescribeEventsForOrganization |
| health-agent | health-agent/domain | 0 |  | sr-resource | health-agent:GetDomain, health-agent:ListDomains |
| health-agent | health-agent/subscription | 1 |  | sr-resource | health-agent:GetSubscription, health-agent:ListSubscriptions |
| healthlake | healthlake/datatransformationprofile | 0 |  | sr-resource | healthlake:GetDataTransformationProfile, healthlake:ListDataTransformationProfiles |
| healthlake | healthlake/datatransformationprofileversion | 1 |  | child-uncatalogued | healthlake:ListDataTransformationProfileVersions |
| healthlake | healthlake/fhirexportjob | 1 |  | child-uncatalogued | healthlake:DescribeFHIRExportJob, healthlake:ListFHIRExportJobs |
| healthlake | healthlake/fhirimportjob | 1 |  | child-uncatalogued | healthlake:DescribeFHIRImportJob, healthlake:ListFHIRImportJobs |
| iam | iam/attachedrolepolicy | 1 |  | child-uncatalogued | iam:ListAttachedGroupPolicies, iam:ListAttachedRolePolicies, iam:ListAttachedUserPolicies |
| iam | iam/contextkeysforprincipalpolicy | 1 |  | child-uncatalogued | iam:GetContextKeysForPrincipalPolicy |
| iam | iam/custompolicy | 0 |  | child-uncatalogued | iam:SimulateCustomPolicy, iam:SimulatePrincipalPolicy |
| iam | iam/delegationrequest | 0 |  | sr-resource | iam:GetDelegationRequest, iam:ListDelegationRequests |
| iam | iam/entity | 1 |  | child-uncatalogued | iam:ListEntitiesForPolicy |
| iam | iam/policyversion | 1 |  | child-uncatalogued | iam:GetPolicyVersion, iam:ListPolicyVersions |
| iam | iam/servicespecificcredential | 0 |  | element-written | iam:ListServiceSpecificCredentials |
| iam | iam/signingcertificate | 0 |  | element-written | iam:ListSigningCertificates |
| iam | iam/sshpublickey | 0 |  | element-written | iam:GetSSHPublicKey, iam:ListSSHPublicKeys |
| imagebuilder | imagebuilder/componentbuildversion | 0 |  | element-written | imagebuilder:ListComponentBuildVersions |
| imagebuilder | imagebuilder/imagebuildversion | 0 |  | element-written | imagebuilder:ListImageBuildVersions, imagebuilder:ListImagePipelineImages |
| imagebuilder | imagebuilder/imagepackage | 1 |  | child-uncatalogued | imagebuilder:ListImagePackages |
| imagebuilder | imagebuilder/imagescanfinding | 0 |  | element-arn | imagebuilder:ListImageScanFindings |
| imagebuilder | imagebuilder/lifecycleexecution | 0 |  | sr-resource | imagebuilder:GetLifecycleExecution, imagebuilder:ListLifecycleExecutions |
| imagebuilder | imagebuilder/lifecycleexecutionresource | 1 |  | child-uncatalogued | imagebuilder:ListLifecycleExecutionResources |
| imagebuilder | imagebuilder/waitingworkflowstep | 0 |  | element-arn | imagebuilder:ListWaitingWorkflowSteps |
| imagebuilder | imagebuilder/workflowbuildversion | 0 |  | element-written | imagebuilder:ListWorkflowBuildVersions |
| imagebuilder | imagebuilder/workflowexecution | 0 |  | sr-resource | imagebuilder:GetWorkflowExecution, imagebuilder:ListWorkflowExecutions |
| imagebuilder | imagebuilder/workflowstepexecution | 0 |  | sr-resource | imagebuilder:GetWorkflowStepExecution, imagebuilder:ListWorkflowStepExecutions |
| inspector | inspector/agent | 0 |  | child-uncatalogued | inspector:PreviewAgents |
| inspector | inspector/assessmentrunagent | 1 |  | child-uncatalogued | inspector:ListAssessmentRunAgents |
| inspector | inspector/eventsubscription | 0 |  | element-arn | inspector:ListEventSubscriptions |
| inspector | inspector/exclusion | 1 |  | child-uncatalogued | inspector:ListExclusions |
| inspector2 | inspector2/cisscan | 0 |  | element-written | inspector2:ListCisScans |
| inspector2 | inspector2/cisscanresultdetail | 0 |  | child-uncatalogued | inspector2:GetCisScanResultDetails |
| inspector2 | inspector2/cisscanresultsaggregatedbycheck | 0 |  | child-uncatalogued | inspector2:ListCisScanResultsAggregatedByChecks |
| inspector2 | inspector2/cisscanresultsaggregatedbytargetresource | 0 |  | child-uncatalogued | inspector2:ListCisScanResultsAggregatedByTargetResource |
| inspector2 | inspector2/clustersforimage | 0 |  | element-arn | inspector2:GetClustersForImage |
| inspector2 | inspector2/connector | 0 |  | sr-resource | inspector2:ListConnectors |
| inspector2 | inspector2/connectorscanconfiguration | 0 |  | element-written | inspector2:ListConnectorScanConfigurations |
| inspector2 | inspector2/delegatedadminaccount | 0 |  | element-written | inspector2:GetDelegatedAdminAccount, inspector2:ListDelegatedAdminAccounts |
| inspector2 | inspector2/finding | 0 |  | sr-resource | inspector2:ListFindings |
| inspector2 | inspector2/findingaggregation | 0 |  | element-written | inspector2:ListFindingAggregations |
| inspector2 | inspector2/getaccountstatus | 0 |  | element-written | inspector2:BatchGetAccountStatus |
| inspector2 | inspector2/getfindingdetail | 1 |  | child-uncatalogued | inspector2:BatchGetFindingDetails |
| inspector2 | inspector2/getmemberec2deepinspectionstatus | 0 |  | element-written | inspector2:BatchGetMemberEc2DeepInspectionStatus |
| inspector2 | inspector2/vulnerability | 0 |  | element-written | inspector2:SearchVulnerabilities |
| interconnect | interconnect/attachpoint | 1 |  | child-uncatalogued | interconnect:ListAttachPoints |
| internetmonitor | internetmonitor/healthevent | 1 |  | smithy-resource | internetmonitor:ListHealthEvents |
| internetmonitor | internetmonitor/internetevent | 0 |  | smithy-resource | internetmonitor:GetInternetEvent, internetmonitor:ListInternetEvents |
| internetmonitor | internetmonitor/queryresult | 1 |  | child-uncatalogued | internetmonitor:GetQueryResults |
| invoicing | invoicing/procurementportalpreference | 0 |  | sr-resource | invoicing:GetProcurementPortalPreference, invoicing:ListProcurementPortalPreferences |
| invoicing | invoicing/procurementportalsupplier | 0 |  | child-uncatalogued | invoicing:ListProcurementPortalSuppliers |
| iot | iot/auditmitigationactionsexecution | 0 |  | child-uncatalogued | iot:ListAuditMitigationActionsExecutions |
| iot | iot/auditsuppression | 0 |  | element-written | iot:DescribeAuditSuppression, iot:ListAuditSuppressions |
| iot | iot/commandexecution | 0 |  | element-written | iot:GetCommandExecution, iot:ListCommandExecutions |
| iot | iot/effectivepolicy | 0 |  | element-arn | iot:GetEffectivePolicies |
| iot | iot/jobexecution | 1 |  | child-uncatalogued | iot:DescribeJobExecution, iot:ListJobExecutionsForJob, iot:ListJobExecutionsForThing |
| iot | iot/managed | 0 |  | element-arn | iot:ListManagedJobTemplates |
| iot | iot/outgoingcertificate | 0 |  | element-arn | iot:ListOutgoingCertificates |
| iot | iot/policyversion | 1 |  | child-uncatalogued | iot:GetPolicyVersion, iot:ListPolicyVersions |
| iot | iot/principalthing | 1 |  | child-uncatalogued | iot:ListPrincipalThings, iot:ListPrincipalThingsV2 |
| iot | iot/provisioningtemplateversion | 1 |  | child-uncatalogued | iot:DescribeProvisioningTemplateVersion, iot:ListProvisioningTemplateVersions |
| iot | iot/relatedresource | 0 |  | child-uncatalogued | iot:ListRelatedResourcesForAuditFinding |
| iot | iot/sbomvalidationresult | 2 |  | child-uncatalogued | iot:ListSbomValidationResults |
| iot | iot/securityprofilesfortarget | 1 |  | child-uncatalogued | iot:ListSecurityProfilesForTarget |
| iot | iot/target | 1 |  | child-uncatalogued | iot:ListTargetsForPolicy, iot:ListTargetsForSecurityProfile |
| iot | iot/thingregistrationtaskreport | 1 |  | child-uncatalogued | iot:ListThingRegistrationTaskReports |
| iot | iot/thingsinthinggroup | 1 |  | child-uncatalogued | iot:ListThingsInThingGroup |
| iot | iot/violationevent | 1 |  | child-uncatalogued | iot:ListViolationEvents |
| iot-jobs-data | iot-jobs-data/pendingjobexecution | 1 |  | child-uncatalogued | iot-jobs-data:GetPendingJobExecutions |
| iotdeviceadvisor | iotdeviceadvisor/suiterun | 0 |  | sr-resource | iotdeviceadvisor:GetSuiteRun, iotdeviceadvisor:ListSuiteRuns |
| iotfleetwise | iotfleetwise/decodermanifestnetworkinterface | 1 |  | child-uncatalogued | iotfleetwise:ListDecoderManifestNetworkInterfaces |
| iotfleetwise | iotfleetwise/decodermanifestsignal | 1 |  | child-uncatalogued | iotfleetwise:ListDecoderManifestSignals |
| iotfleetwise | iotfleetwise/fleetassociation | 1 |  | smithy-resource | iotfleetwise:ListFleetsForVehicle |
| iotfleetwise | iotfleetwise/vehicleassociation | 1 |  | smithy-resource | iotfleetwise:ListVehiclesInFleet |
| iotfleetwise | iotfleetwise/vehiclestatus | 1 |  | child-uncatalogued | iotfleetwise:GetVehicleStatus |
| iotmanagedintegrations | iotmanagedintegrations/cloudconnector | 0 |  | smithy-resource | iotmanagedintegrations:GetCloudConnector, iotmanagedintegrations:ListCloudConnectors |
| iotmanagedintegrations | iotmanagedintegrations/connectordestination | 0 |  | smithy-resource | iotmanagedintegrations:GetConnectorDestination, iotmanagedintegrations:ListConnectorDestinations |
| iotmanagedintegrations | iotmanagedintegrations/destination | 0 |  | smithy-resource | iotmanagedintegrations:GetDestination, iotmanagedintegrations:ListDestinations |
| iotmanagedintegrations | iotmanagedintegrations/devicediscovery | 0 |  | smithy-resource | iotmanagedintegrations:GetDeviceDiscovery, iotmanagedintegrations:ListDeviceDiscoveries |
| iotmanagedintegrations | iotmanagedintegrations/discovereddevice | 1 |  | child-uncatalogued | iotmanagedintegrations:ListDiscoveredDevices |
| iotmanagedintegrations | iotmanagedintegrations/eventlogconfiguration | 0 |  | smithy-resource | iotmanagedintegrations:GetEventLogConfiguration, iotmanagedintegrations:ListEventLogConfigurations |
| iotmanagedintegrations | iotmanagedintegrations/managedthingaccountassociation | 1 |  | child-uncatalogued | iotmanagedintegrations:ListManagedThingAccountAssociations |
| iotmanagedintegrations | iotmanagedintegrations/managedthingschema | 1 |  | child-uncatalogued | iotmanagedintegrations:ListManagedThingSchemas |
| iotmanagedintegrations | iotmanagedintegrations/notificationconfiguration | 0 |  | smithy-resource | iotmanagedintegrations:GetNotificationConfiguration, iotmanagedintegrations:ListNotificationConfigurations |
| iotmanagedintegrations | iotmanagedintegrations/otataskconfiguration | 0 |  | smithy-resource | iotmanagedintegrations:GetOtaTaskConfiguration, iotmanagedintegrations:ListOtaTaskConfigurations |
| iotmanagedintegrations | iotmanagedintegrations/otataskexecution | 1 |  | child-uncatalogued | iotmanagedintegrations:ListOtaTaskExecutions |
| iotmanagedintegrations | iotmanagedintegrations/schemaversion | 0 |  | smithy-resource | iotmanagedintegrations:GetSchemaVersion, iotmanagedintegrations:ListSchemaVersions |
| iotsecuredtunneling | iotsecuredtunneling/tunnel | 0 |  | sr-resource | iotsecuredtunneling:DescribeTunnel, iotsecuredtunneling:ListTunnels |
| iotsitewise | iotsitewise/action | 0 |  | child-uncatalogued | iotsitewise:DescribeAction, iotsitewise:ListActions |
| iotsitewise | iotsitewise/application | 0 |  | sr-resource | iotsitewise:DescribeApplication, iotsitewise:ListApplications |
| iotsitewise | iotsitewise/assetcompositemodel | 2 |  | child-uncatalogued | iotsitewise:DescribeAssetCompositeModel |
| iotsitewise | iotsitewise/assetmodelcompositemodel | 1 |  | child-uncatalogued | iotsitewise:DescribeAssetModelCompositeModel, iotsitewise:ListAssetModelCompositeModels |
| iotsitewise | iotsitewise/assetmodelproperty | 1 |  | child-uncatalogued | iotsitewise:ListAssetModelProperties |
| iotsitewise | iotsitewise/assetproperty | 1 |  | child-uncatalogued | iotsitewise:DescribeAssetProperty, iotsitewise:ListAssetProperties |
| iotsitewise | iotsitewise/assetpropertyaggregate | 1 |  | child-uncatalogued | iotsitewise:BatchGetAssetPropertyAggregates, iotsitewise:GetAssetPropertyAggregates |
| iotsitewise | iotsitewise/assetpropertyvalue | 0 |  | child-uncatalogued | iotsitewise:BatchGetAssetPropertyValue, iotsitewise:GetAssetPropertyValue, iotsitewise:GetAssetPropertyValueHistory |
| iotsitewise | iotsitewise/assetpropertyvaluehistory | 1 |  | child-uncatalogued | iotsitewise:BatchGetAssetPropertyValueHistory |
| iotsitewise | iotsitewise/associatedasset | 1 |  | child-uncatalogued | iotsitewise:ListAssociatedAssets |
| iotsitewise | iotsitewise/compositionrelationship | 1 |  | child-uncatalogued | iotsitewise:ListCompositionRelationships |
| iotsitewise | iotsitewise/computationmodeldatabindingusage | 1 |  | child-uncatalogued | iotsitewise:ListComputationModelDataBindingUsages |
| iotsitewise | iotsitewise/datasetdatasegment | 1 |  | child-uncatalogued | iotsitewise:ListDatasetDataSegments |
| iotsitewise | iotsitewise/datasetdatasegmentrelationship | 1 |  | child-uncatalogued | iotsitewise:ListDatasetDataSegmentRelationships |
| iotsitewise | iotsitewise/datasetexportjob | 1 |  | child-uncatalogued | iotsitewise:DescribeDatasetExportJob, iotsitewise:ListDatasetExportJobs |
| iotsitewise | iotsitewise/enrichmentjob | 1 |  | child-uncatalogued | iotsitewise:DescribeEnrichmentJob, iotsitewise:ListEnrichmentJobs |
| iotsitewise | iotsitewise/execution | 0 |  | child-uncatalogued | iotsitewise:DescribeExecution, iotsitewise:ListExecutions |
| iotsitewise | iotsitewise/interfacerelationship | 1 |  | child-uncatalogued | iotsitewise:ListInterfaceRelationships |
| iotsitewise | iotsitewise/pipeline | 1 |  | sr-resource | iotsitewise:DescribePipeline, iotsitewise:ListPipelines |
| iotsitewise | iotsitewise/pipelineexecution | 2 |  | child-uncatalogued | iotsitewise:DescribePipelineExecution, iotsitewise:ListPipelineExecutions |
| iotsitewise | iotsitewise/projectasset | 1 |  | child-uncatalogued | iotsitewise:ListProjectAssets |
| iotsitewise | iotsitewise/query | 0 |  | child-uncatalogued | iotsitewise:DescribeQuery, iotsitewise:ExecuteQuery, iotsitewise:ListQueries |
| iotsitewise | iotsitewise/queryresult | 1 |  | child-uncatalogued | iotsitewise:GetQueryResults |
| iotsitewise | iotsitewise/search | 1 |  | child-uncatalogued | iotsitewise:DescribeSearch, iotsitewise:ListSearches |
| iotsitewise | iotsitewise/searchresult | 2 |  | child-uncatalogued | iotsitewise:GetSearchResults |
| iotsitewise | iotsitewise/task | 1 |  | sr-resource | iotsitewise:DescribeTask, iotsitewise:ListTasks |
| iotsitewise | iotsitewise/timeseries | 0 |  | sr-resource | iotsitewise:DescribeTimeSeries, iotsitewise:ListTimeSeries |
| iotsitewise | iotsitewise/workspace | 0 |  | sr-resource | iotsitewise:DescribeWorkspace, iotsitewise:ListWorkspaces |
| iottwinmaker | iottwinmaker/component | 2 |  | child-uncatalogued | iottwinmaker:ListComponents |
| iottwinmaker | iottwinmaker/metadatatransferjob | 0 |  | sr-resource | iottwinmaker:GetMetadataTransferJob, iottwinmaker:ListMetadataTransferJobs |
| iottwinmaker | iottwinmaker/property | 1 |  | child-uncatalogued | iottwinmaker:ListProperties |
| iottwinmaker | iottwinmaker/query | 1 |  | child-uncatalogued | iottwinmaker:ExecuteQuery |
| iottwinmaker | iottwinmaker/syncresource | 2 |  | child-uncatalogued | iottwinmaker:ListSyncResources |
| iotwireless | iotwireless/eventconfiguration | 0 |  | element-written | iotwireless:ListEventConfigurations |
| iotwireless | iotwireless/loglevelsbyresourcetype | 0 |  | element-written | iotwireless:GetLogLevelsByResourceTypes |
| iotwireless | iotwireless/multicastgroupsbyfuotatask | 1 |  | child-uncatalogued | iotwireless:ListMulticastGroupsByFuotaTask |
| iotwireless | iotwireless/positionconfiguration | 0 |  | element-written | iotwireless:GetPositionConfiguration, iotwireless:ListPositionConfigurations |
| iotwireless | iotwireless/queuedmessage | 1 |  | child-uncatalogued | iotwireless:ListQueuedMessages |
| ivs | ivs/composition | 0 |  | sr-resource | ivs:GetComposition, ivs:ListCompositions |
| ivs | ivs/participant | 1 |  | child-uncatalogued | ivs:GetParticipant, ivs:ListParticipants |
| ivs | ivs/participantevent | 1 |  | child-uncatalogued | ivs:ListParticipantEvents |
| ivs | ivs/participantreplica | 1 |  | child-uncatalogued | ivs:ListParticipantReplicas |
| ivs | ivs/stagesession | 1 |  | child-uncatalogued | ivs:GetStageSession, ivs:ListStageSessions |
| ivs | ivs/stream | 0 |  | element-arn | ivs:GetStream, ivs:ListStreams |
| ivs | ivs/streamsession | 1 |  | child-uncatalogued | ivs:GetStreamSession, ivs:ListStreamSessions |
| ivs | ivs/viewersessionrevocation | 1 |  | child-uncatalogued | ivs:BatchStartViewerSessionRevocation, ivs:StartViewerSessionRevocation |
| kafka | kafka/channel | 1 |  | sr-resource | kafka:DescribeChannel, kafka:ListChannels |
| kafka | kafka/clientvpcconnection | 1 |  | child-uncatalogued | kafka:ListClientVpcConnections |
| kafka | kafka/clusteroperation | 0 |  | child-uncatalogued | kafka:DescribeClusterOperation, kafka:DescribeClusterOperationV2, kafka:ListClusterOperations, kafka:ListClusterOperationsV2 |
| kafka | kafka/node | 1 |  | child-uncatalogued | kafka:ListNodes |
| kafka | kafka/topic | 1 |  | sr-resource | kafka:DescribeTopic, kafka:ListTopics |
| kafkaconnect | kafkaconnect/connectoroperation | 0 |  | sr-resource | kafkaconnect:DescribeConnectorOperation, kafkaconnect:ListConnectorOperations |
| kendra | kendra/datasourcesyncjob | 2 |  | child-uncatalogued | kendra:ListDataSourceSyncJobs |
| kendra | kendra/entitypersona | 2 |  | child-uncatalogued | kendra:ListEntityPersonas |
| kendra | kendra/experienceentity | 2 |  | child-uncatalogued | kendra:ListExperienceEntities |
| kendra | kendra/groupsolderthanorderingid | 2 |  | child-uncatalogued | kendra:ListGroupsOlderThanOrderingId |
| kendra | kendra/snapshot | 1 |  | child-uncatalogued | kendra:GetSnapshots |
| kinesis | kinesis/channel | 0 |  | sr-resource | kinesis:DescribeChannel, kinesis:ListChannels |
| kinesis | kinesis/record | 1 |  | child-uncatalogued | kinesis:GetRecords |
| kinesisanalytics | kinesisanalytics/applicationoperation | 1 |  | child-uncatalogued | kinesisanalytics:DescribeApplicationOperation, kinesisanalytics:ListApplicationOperations |
| kinesisanalytics | kinesisanalytics/applicationsnapshot | 1 |  | child-uncatalogued | kinesisanalytics:DescribeApplicationSnapshot, kinesisanalytics:ListApplicationSnapshots |
| kinesisanalytics | kinesisanalytics/applicationversion | 1 |  | child-uncatalogued | kinesisanalytics:ListApplicationVersions |
| kinesisvideo | kinesisvideo/edgeagentconfiguration | 0 |  | child-uncatalogued | kinesisvideo:ListEdgeAgentConfigurations |
| kinesisvideo | kinesisvideo/mappedresourceconfiguration | 0 |  | element-arn | kinesisvideo:DescribeMappedResourceConfiguration |
| kms | kms/customkeystore | 0 |  | element-written | kms:DescribeCustomKeyStores |
| kms | kms/keyrotation | 1 |  | child-uncatalogued | kms:ListKeyRotations |
| lakeformation | lakeformation/lakeformationoptin | 0 |  | element-written | lakeformation:ListLakeFormationOptIns |
| lakeformation | lakeformation/transaction | 0 |  | element-written | lakeformation:DescribeTransaction, lakeformation:ListTransactions |
| lambda | lambda/durableexecution | 0 |  | sr-resource | lambda:GetDurableExecution, lambda:ListDurableExecutionsByFunction |
| lambda | lambda/durableexecutionhistory | 1 |  | child-uncatalogued | lambda:GetDurableExecutionHistory |
| lambda | lambda/durableexecutionstate | 1 |  | child-uncatalogued | lambda:GetDurableExecutionState |
| lambda | lambda/managedmicrovmimage | 0 |  | element-arn | lambda:ListManagedMicrovmImages |
| lambda | lambda/managedmicrovmimageversion | 1 |  | child-uncatalogued | lambda:ListManagedMicrovmImageVersions |
| lambda | lambda/microvm | 0 |  | smithy-resource | lambda:GetMicrovm, lambda:ListMicrovms |
| lambda | lambda/microvmimage | 0 |  | smithy-resource | lambda:GetMicrovmImage, lambda:ListMicrovmImages |
| lambda | lambda/microvmimagebuild | 1 |  | child-uncatalogued | lambda:GetMicrovmImageBuild, lambda:ListMicrovmImageBuilds |
| lambda | lambda/microvmimageversion | 1 |  | child-uncatalogued | lambda:GetMicrovmImageVersion, lambda:ListMicrovmImageVersions |
| lambda | lambda/networkconnector | 0 |  | smithy-resource | lambda:GetNetworkConnector, lambda:ListNetworkConnectors |
| lambda | lambda/provisionedconcurrencyconfig | 1 |  | child-uncatalogued | lambda:GetProvisionedConcurrencyConfig, lambda:ListProvisionedConcurrencyConfigs |
| launchwizard | launchwizard/deploymentevent | 1 |  | smithy-resource | launchwizard:ListDeploymentEvents |
| launchwizard | launchwizard/deploymentpatternversion | 2 |  | smithy-resource | launchwizard:GetDeploymentPatternVersion, launchwizard:ListDeploymentPatternVersions |
| launchwizard | launchwizard/workload | 0 |  | smithy-resource | launchwizard:GetWorkload, launchwizard:ListWorkloads |
| launchwizard | launchwizard/workloaddeploymentpattern | 1 |  | smithy-resource | launchwizard:GetWorkloadDeploymentPattern, launchwizard:ListWorkloadDeploymentPatterns |
| lex | lex/botaliasreplica | 2 |  | child-uncatalogued | lex:ListBotAliasReplicas |
| lex | lex/botchannelassociation | 2 |  | child-uncatalogued | lex:GetBotChannelAssociation, lex:GetBotChannelAssociations |
| lex | lex/botlocale | 2 |  | child-uncatalogued | lex:DescribeBotLocale, lex:ListBotLocales |
| lex | lex/botrecommendation | 3 |  | child-uncatalogued | lex:DescribeBotRecommendation, lex:ListBotRecommendations |
| lex | lex/botresourcegeneration | 3 |  | child-uncatalogued | lex:DescribeBotResourceGeneration, lex:ListBotResourceGenerations |
| lex | lex/customvocabularyitem | 3 |  | child-uncatalogued | lex:ListCustomVocabularyItems |
| lex | lex/export | 0 |  | element-written | lex:DescribeExport, lex:GetExport, lex:ListExports |
| lex | lex/import | 0 |  | element-written | lex:DescribeImport, lex:GetImport, lex:ListImports |
| lex | lex/intent | 3 |  | child-uncatalogued | lex:DescribeIntent, lex:GetIntent, lex:ListIntents |
| lex | lex/intentpath | 1 |  | child-uncatalogued | lex:ListIntentPaths |
| lex | lex/intentversion | 4 |  | sr-resource | lex:GetIntentVersions, lex:GetIntents |
| lex | lex/migration | 0 |  | element-written | lex:GetMigrations |
| lex | lex/recommendedintent | 4 |  | child-uncatalogued | lex:ListRecommendedIntents |
| lex | lex/sessionanalyticsdata | 1 |  | child-uncatalogued | lex:ListSessionAnalyticsData |
| lex | lex/slot | 4 |  | child-uncatalogued | lex:DescribeSlot, lex:ListSlots |
| lex | lex/slottype | 3 |  | child-uncatalogued | lex:DescribeSlotType, lex:GetSlotType, lex:ListSlotTypes |
| lex | lex/slottypeversion | 4 |  | sr-resource | lex:GetSlotTypeVersions, lex:GetSlotTypes |
| lex | lex/testexecution | 0 |  | element-written | lex:DescribeTestExecution, lex:ListTestExecutions |
| lex | lex/testsetrecord | 1 |  | child-uncatalogued | lex:ListTestSetRecords |
| lex | lex/utteranceanalyticsdata | 1 |  | child-uncatalogued | lex:ListUtteranceAnalyticsData |
| license-manager | license-manager/asset | 1 |  | child-uncatalogued | license-manager:ListAssetsForLicenseAssetGroup |
| license-manager | license-manager/association | 1 |  | child-uncatalogued | license-manager:ListAssociationsForLicenseConfiguration |
| license-manager | license-manager/failuresforlicenseconfigurationoperation | 1 |  | child-uncatalogued | license-manager:ListFailuresForLicenseConfigurationOperations |
| license-manager | license-manager/licenseconversiontask | 0 |  | element-written | license-manager:GetLicenseConversionTask, license-manager:ListLicenseConversionTasks |
| license-manager | license-manager/licensespecification | 0 |  | child-uncatalogued | license-manager:ListLicenseSpecificationsForResource |
| license-manager | license-manager/receivedlicense | 0 |  | element-written | license-manager:ListReceivedLicenses, license-manager:ListReceivedLicensesForOrganization |
| license-manager | license-manager/resourceinventory | 0 |  | element-arn | license-manager:ListResourceInventory |
| license-manager | license-manager/token | 0 |  | element-written | license-manager:ListTokens |
| license-manager | license-manager/usage | 1 |  | child-uncatalogued | license-manager:ListUsageForLicenseConfiguration |
| lightsail | lightsail/bucketaccesskey | 1 |  | child-uncatalogued | lightsail:GetBucketAccessKeys |
| lightsail | lightsail/cloudformationstackrecord | 0 |  | sr-resource | lightsail:GetCloudFormationStackRecords |
| lightsail | lightsail/exportsnapshotrecord | 0 |  | sr-resource | lightsail:GetExportSnapshotRecords |
| lightsail | lightsail/relationaldatabaseparameter | 1 |  | child-uncatalogued | lightsail:GetRelationalDatabaseParameters |
| lightsail | lightsail/setuphistory | 1 |  | child-uncatalogued | lightsail:GetSetupHistory |
| logs | logs/anomaly | 0 |  | element-arn | logs:ListAnomalies |
| logs | logs/configurationtemplate | 0 |  | element-written | logs:DescribeConfigurationTemplates |
| logs | logs/exporttask | 0 |  | element-written | logs:DescribeExportTasks |
| logs | logs/fieldindex | 1 |  | child-uncatalogued | logs:DescribeFieldIndexes |
| logs | logs/importtask | 0 |  | element-written | logs:DescribeImportTasks |
| logs | logs/importtaskbatch | 0 |  | child-uncatalogued | logs:DescribeImportTaskBatches |
| logs | logs/indexpolicy | 1 |  | child-uncatalogued | logs:DescribeIndexPolicies |
| logs | logs/scheduledqueryhistory | 1 |  | child-uncatalogued | logs:GetScheduledQueryHistory |
| logs | logs/source | 1 |  | child-uncatalogued | logs:ListSourcesForS3TableIntegration |
| logs | logs/syslogconfiguration | 0 |  | element-arn | logs:ListSyslogConfigurations |
| lookoutequipment | lookoutequipment/dataingestionjob | 0 |  | element-arn | lookoutequipment:DescribeDataIngestionJob, lookoutequipment:ListDataIngestionJobs |
| lookoutequipment | lookoutequipment/inferenceevent | 1 |  | child-uncatalogued | lookoutequipment:ListInferenceEvents |
| lookoutequipment | lookoutequipment/inferenceexecution | 1 |  | child-uncatalogued | lookoutequipment:ListInferenceExecutions |
| lookoutequipment | lookoutequipment/label | 1 |  | child-uncatalogued | lookoutequipment:DescribeLabel, lookoutequipment:ListLabels |
| lookoutequipment | lookoutequipment/retrainingscheduler | 0 |  | element-written | lookoutequipment:DescribeRetrainingScheduler, lookoutequipment:ListRetrainingSchedulers |
| lookoutequipment | lookoutequipment/sensorstatistic | 1 |  | child-uncatalogued | lookoutequipment:ListSensorStatistics |
| m2 | m2/batch | 1 |  | child-uncatalogued | m2:GetBatchJobExecution, m2:ListBatchJobExecutions |
| m2 | m2/dataset | 1 |  | child-uncatalogued | m2:ListDataSets |
| m2 | m2/datasetexporthistory | 1 |  | child-uncatalogued | m2:ListDataSetExportHistory |
| m2 | m2/datasetimporthistory | 1 |  | child-uncatalogued | m2:ListDataSetImportHistory |
| machinelearning | machinelearning/batchprediction | 0 |  | sr-resource | machinelearning:DescribeBatchPredictions, machinelearning:GetBatchPrediction |
| machinelearning | machinelearning/datasource | 0 |  | sr-resource | machinelearning:DescribeDataSources, machinelearning:GetDataSource |
| machinelearning | machinelearning/evaluation | 0 |  | sr-resource | machinelearning:DescribeEvaluations, machinelearning:GetEvaluation |
| machinelearning | machinelearning/mlmodel | 0 |  | sr-resource | machinelearning:DescribeMLModels, machinelearning:GetMLModel |
| macie2 | macie2/bucket | 0 |  | element-arn | macie2:DescribeBuckets |
| macie2 | macie2/resourceprofileartifact | 1 |  | child-uncatalogued | macie2:ListResourceProfileArtifacts |
| macie2 | macie2/resourceprofiledetection | 1 |  | child-uncatalogued | macie2:ListResourceProfileDetections |
| managedblockchain | managedblockchain/invitation | 0 |  | sr-resource | managedblockchain:ListInvitations |
| managedblockchain | managedblockchain/proposalvote | 2 |  | child-uncatalogued | managedblockchain:ListProposalVotes |
| mediaconnect | mediaconnect/entitlement | 0 |  | sr-resource | mediaconnect:ListEntitlements |
| mediaconnect | mediaconnect/gatewayinstance | 0 |  | smithy-resource | mediaconnect:DescribeGatewayInstance, mediaconnect:ListGatewayInstances |
| mediaconnect | mediaconnect/offering | 0 |  | smithy-resource | mediaconnect:DescribeOffering, mediaconnect:ListOfferings |
| mediaconvert | mediaconvert/job | 0 |  | sr-resource | mediaconvert:GetJob, mediaconvert:GetJobsQueryResults, mediaconvert:ListJobs, mediaconvert:SearchJobs |
| medialive | medialive/alert | 1 |  | child-uncatalogued | medialive:ListAlerts |
| medialive | medialive/clusteralert | 1 |  | child-uncatalogued | medialive:ListClusterAlerts |
| medialive | medialive/multiplexalert | 1 |  | child-uncatalogued | medialive:ListMultiplexAlerts |
| medialive | medialive/offering | 0 |  | sr-resource | medialive:DescribeOffering, medialive:ListOfferings |
| medialive | medialive/schedule | 1 |  | child-uncatalogued | medialive:DescribeSchedule |
| medialive | medialive/version | 0 |  | element-written | medialive:ListVersions |
| mediapackage | mediapackage/harvestjob | 0 |  | sr-resource | mediapackage:DescribeHarvestJob, mediapackage:ListHarvestJobs |
| mediapackagev2 | mediapackagev2/harvestjob | 1 |  | smithy-resource | mediapackagev2:GetHarvestJob, mediapackagev2:ListHarvestJobs |
| mediastore | mediastore/container | 0 |  | sr-resource | mediastore:DescribeContainer, mediastore:ListContainers |
| mediastore | mediastore/object | 1 |  | sr-resource | mediastore:DescribeObject, mediastore:GetObject |
| mediatailor | mediatailor/alert | 0 |  | child-uncatalogued | mediatailor:ListAlerts |
| mediatailor | mediatailor/function | 0 |  | smithy-resource | mediatailor:GetFunction, mediatailor:ListFunctions |
| medical-imaging | medical-imaging/dicomimportjob | 1 |  | child-uncatalogued | medical-imaging:GetDICOMImportJob, medical-imaging:ListDICOMImportJobs |
| medical-imaging | medical-imaging/imageset | 1 |  | sr-resource | medical-imaging:GetImageSet, medical-imaging:SearchImageSets |
| medical-imaging | medical-imaging/imagesetversion | 2 |  | child-uncatalogued | medical-imaging:ListImageSetVersions |
| memorydb | memorydb/parameter | 1 |  | child-uncatalogued | memorydb:DescribeParameters |
| mgh | mgh/createdartifact | 1 |  | child-uncatalogued | mgh:ListCreatedArtifacts |
| mgh | mgh/discoveredresource | 1 |  | child-uncatalogued | mgh:ListDiscoveredResources |
| mgh | mgh/homeregioncontrol | 0 |  | element-written | mgh:DescribeHomeRegionControls |
| mgh | mgh/migrationtask | 0 |  | sr-resource | mgh:DescribeMigrationTask, mgh:ListMigrationTasks |
| mgh | mgh/sourceresource | 1 |  | child-uncatalogued | mgh:ListSourceResources |
| mgn | mgn/export | 0 |  | smithy-resource | mgn:ListExports |
| mgn | mgn/import | 0 |  | smithy-resource | mgn:ListImports |
| mgn | mgn/importfileenrichment | 0 |  | element-written | mgn:ListImportFileEnrichments |
| mgn | mgn/job | 0 |  | smithy-resource | mgn:DescribeJobs |
| mgn | mgn/networkmigrationanalysis | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationAnalyses |
| mgn | mgn/networkmigrationanalysisresult | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationAnalysisResults |
| mgn | mgn/networkmigrationcodegeneration | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationCodeGenerations |
| mgn | mgn/networkmigrationcodegenerationsegment | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationCodeGenerationSegments |
| mgn | mgn/networkmigrationdeployedstack | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationDeployedStacks |
| mgn | mgn/networkmigrationdeployment | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationDeployments |
| mgn | mgn/networkmigrationexecution | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationExecutions |
| mgn | mgn/networkmigrationmappersegment | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationMapperSegments |
| mgn | mgn/networkmigrationmappersegmentconstruct | 1 |  | child-uncatalogued | mgn:GetNetworkMigrationMapperSegmentConstruct, mgn:ListNetworkMigrationMapperSegmentConstructs |
| mgn | mgn/networkmigrationmapping | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationMappings |
| mgn | mgn/networkmigrationmappingupdate | 1 |  | child-uncatalogued | mgn:ListNetworkMigrationMappingUpdates |
| mgn | mgn/sourceserveraction | 1 |  | child-uncatalogued | mgn:ListSourceServerActions |
| mgn | mgn/templateaction | 1 |  | child-uncatalogued | mgn:ListTemplateActions |
| migrationhub-orchestrator | migrationhub-orchestrator/plugin | 0 |  | smithy-resource | migrationhub-orchestrator:ListPlugins |
| migrationhub-orchestrator | migrationhub-orchestrator/templatestep | 0 |  | smithy-resource | migrationhub-orchestrator:GetTemplateStep, migrationhub-orchestrator:ListTemplateSteps |
| migrationhub-orchestrator | migrationhub-orchestrator/templatestepgroup | 0 |  | smithy-resource | migrationhub-orchestrator:GetTemplateStepGroup, migrationhub-orchestrator:ListTemplateStepGroups |
| migrationhub-orchestrator | migrationhub-orchestrator/workflowstep | 0 |  | smithy-resource | migrationhub-orchestrator:GetWorkflowStep, migrationhub-orchestrator:ListWorkflowSteps |
| migrationhub-orchestrator | migrationhub-orchestrator/workflowstepgroup | 0 |  | smithy-resource | migrationhub-orchestrator:GetWorkflowStepGroup, migrationhub-orchestrator:ListWorkflowStepGroups |
| migrationhub-strategy | migrationhub-strategy/applicationcomponent | 0 |  | element-written | migrationhub-strategy:GetApplicationComponentDetails, migrationhub-strategy:ListApplicationComponents |
| migrationhub-strategy | migrationhub-strategy/serverdetail | 1 |  | child-uncatalogued | migrationhub-strategy:GetServerDetails |
| mobiletargeting | mobiletargeting/campaignactivity | 2 |  | child-uncatalogued | mobiletargeting:GetCampaignActivities |
| mobiletargeting | mobiletargeting/channel | 1 |  | sr-resource | mobiletargeting:GetChannels |
| mobiletargeting | mobiletargeting/exportjob | 1 |  | sr-resource | mobiletargeting:GetExportJob, mobiletargeting:GetExportJobs, mobiletargeting:GetSegmentExportJobs |
| mobiletargeting | mobiletargeting/importjob | 1 |  | sr-resource | mobiletargeting:GetImportJob, mobiletargeting:GetImportJobs, mobiletargeting:GetSegmentImportJobs |
| mobiletargeting | mobiletargeting/journey | 1 |  | sr-resource | mobiletargeting:GetJourney, mobiletargeting:ListJourneys |
| mobiletargeting | mobiletargeting/journeyrun | 2 |  | child-uncatalogued | mobiletargeting:GetJourneyRuns |
| mobiletargeting | mobiletargeting/recommenderconfiguration | 0 |  | element-written | mobiletargeting:GetRecommenderConfiguration, mobiletargeting:GetRecommenderConfigurations |
| mobiletargeting | mobiletargeting/templateversion | 2 |  | child-uncatalogued | mobiletargeting:ListTemplateVersions |
| monitoring | monitoring/alarmcontributor | 1 |  | child-uncatalogued | monitoring:DescribeAlarmContributors |
| monitoring | monitoring/managedinsightrule | 0 |  | child-uncatalogued | monitoring:ListManagedInsightRules |
| monitoring | monitoring/metricdata | 1 |  | child-uncatalogued | monitoring:GetMetricData |
| mpa | mpa/policy | 0 |  | element-arn | mpa:ListPolicies |
| mpa | mpa/policyversion | 0 |  | child-uncatalogued | mpa:GetPolicyVersion, mpa:ListPolicyVersions |
| mpa | mpa/resourcepolicy | 0 |  | child-uncatalogued | mpa:GetResourcePolicy, mpa:ListResourcePolicies |
| mpa | mpa/session | 0 |  | smithy-resource | mpa:ListSessions |
| mq | mq/sharedresource | 1 |  | child-uncatalogued | mq:DescribeSharedResources |
| mturk-requester | mturk-requester/assignment | 0 |  | child-uncatalogued | mturk-requester:GetAssignment, mturk-requester:ListAssignmentsForHIT |
| mturk-requester | mturk-requester/hit | 0 |  | element-written | mturk-requester:GetHIT, mturk-requester:ListHITs, mturk-requester:ListHITsForQualificationType, mturk-requester:ListReviewableHITs |
| mturk-requester | mturk-requester/qualificationtype | 0 |  | element-written | mturk-requester:GetQualificationType, mturk-requester:ListQualificationTypes |
| neptune-graph | neptune-graph/exporttask | 1 |  | sr-resource | neptune-graph:GetExportTask, neptune-graph:ListExportTasks |
| neptune-graph | neptune-graph/importtask | 1 |  | sr-resource | neptune-graph:GetImportTask, neptune-graph:ListImportTasks |
| network-firewall | network-firewall/analysisreport | 0 |  | element-written | network-firewall:ListAnalysisReports |
| network-firewall | network-firewall/containerassociation | 0 |  | sr-resource | network-firewall:DescribeContainerAssociation, network-firewall:ListContainerAssociations |
| network-firewall | network-firewall/flowoperation | 1 |  | child-uncatalogued | network-firewall:DescribeFlowOperation, network-firewall:ListFlowOperations |
| network-firewall | network-firewall/proxy | 0 |  | sr-resource | network-firewall:DescribeProxy, network-firewall:ListProxies |
| networkflowmonitor | networkflowmonitor/queryresultsmonitortopcontributor | 1 |  | child-uncatalogued | networkflowmonitor:GetQueryResultsMonitorTopContributors |
| networkflowmonitor | networkflowmonitor/queryresultsworkloadinsightstopcontributor | 1 |  | child-uncatalogued | networkflowmonitor:GetQueryResultsWorkloadInsightsTopContributors |
| networkmanager | networkmanager/attachmentroutingpolicyassociation | 1 |  | child-uncatalogued | networkmanager:ListAttachmentRoutingPolicyAssociations |
| networkmanager | networkmanager/connectpeerassociation | 1 |  | child-uncatalogued | networkmanager:GetConnectPeerAssociations |
| networkmanager | networkmanager/corenetworkchangeset | 0 |  | child-uncatalogued | networkmanager:GetCoreNetworkChangeSet |
| networkmanager | networkmanager/corenetworkpolicyversion | 1 |  | child-uncatalogued | networkmanager:ListCoreNetworkPolicyVersions |
| networkmanager | networkmanager/networkresource | 1 |  | child-uncatalogued | networkmanager:GetNetworkResources |
| networkmanager | networkmanager/networktelemetry | 1 |  | child-uncatalogued | networkmanager:GetNetworkTelemetry |
| networkmanager | networkmanager/transitgatewayconnectpeerassociation | 1 |  | child-uncatalogued | networkmanager:GetTransitGatewayConnectPeerAssociations |
| notifications | notifications/managednotificationchildevent | 0 |  | smithy-resource | notifications:GetManagedNotificationChildEvent, notifications:ListManagedNotificationChildEvents |
| notifications | notifications/managednotificationevent | 0 |  | smithy-resource | notifications:GetManagedNotificationEvent, notifications:ListManagedNotificationEvents |
| notifications | notifications/memberaccount | 1 |  | child-uncatalogued | notifications:ListMemberAccounts |
| notifications | notifications/notificationevent | 0 |  | smithy-resource | notifications:GetNotificationEvent, notifications:ListNotificationEvents |
| nova-act | nova-act/act | 3 |  | smithy-resource | nova-act:ListActs |
| nova-act | nova-act/model | 0 |  | smithy-resource | nova-act:ListModels |
| nova-act | nova-act/session | 2 |  | smithy-resource | nova-act:ListSessions |
| nova-act | nova-act/workflowrun | 1 |  | smithy-resource | nova-act:GetWorkflowRun, nova-act:ListWorkflowRuns |
| oam | oam/attachedlink | 1 |  | child-uncatalogued | oam:ListAttachedLinks |
| observabilityadmin | observabilityadmin/telemetryevaluationstatus | 0 |  | element-arn | observabilityadmin:GetTelemetryEvaluationStatus, observabilityadmin:GetTelemetryEvaluationStatusForOrganization |
| odb | odb/autonomousdatabasepeer | 1 |  | child-uncatalogued | odb:ListAutonomousDatabasePeers |
| odb | odb/autonomousvirtualmachine | 1 |  | child-uncatalogued | odb:ListAutonomousVirtualMachines |
| odb | odb/dbserver | 1 |  | child-uncatalogued | odb:GetDbServer, odb:ListDbServers |
| odb | odb/exadbvmcluster | 0 |  | smithy-resource | odb:GetExadbVmCluster, odb:ListExadbVmClusters |
| odb | odb/exascaledbstoragevault | 0 |  | smithy-resource | odb:GetExascaleDbStorageVault, odb:ListExascaleDbStorageVaults |
| omics | omics/annotationimportjob | 0 |  | smithy-resource | omics:ListAnnotationImportJobs |
| omics | omics/multipartreadsetupload | 1 |  | child-uncatalogued | omics:ListMultipartReadSetUploads |
| omics | omics/readset | 1 |  | smithy-resource | omics:GetReadSet, omics:GetReadSetMetadata, omics:ListReadSets |
| omics | omics/readsetactivationjob | 1 |  | child-uncatalogued | omics:ListReadSetActivationJobs |
| omics | omics/readsetexportjob | 1 |  | child-uncatalogued | omics:ListReadSetExportJobs |
| omics | omics/readsetimportjob | 1 |  | child-uncatalogued | omics:GetReadSetImportJob, omics:ListReadSetImportJobs |
| omics | omics/referenceimportjob | 1 |  | child-uncatalogued | omics:GetReferenceImportJob, omics:ListReferenceImportJobs |
| omics | omics/run | 0 |  | smithy-resource | omics:GetRun, omics:ListRuns, omics:ListRunsInBatch |
| omics | omics/runbatch | 0 |  | smithy-resource | omics:GetBatch, omics:ListBatch |
| omics | omics/share | 0 |  | smithy-resource | omics:GetShare, omics:ListShares |
| omics | omics/tagging | 0 |  | smithy-resource | omics:ListTagsForResource |
| omics | omics/task | 1 |  | smithy-resource | omics:GetRunTask, omics:ListRunTasks |
| omics | omics/variantimportjob | 0 |  | smithy-resource | omics:ListVariantImportJobs |
| organizations | organizations/children | 2 |  | child-uncatalogued | organizations:ListChildren |
| organizations | organizations/createaccountstatus | 0 |  | element-written | organizations:DescribeCreateAccountStatus, organizations:ListCreateAccountStatus |
| organizations | organizations/handshake | 2 |  | sr-resource | organizations:DescribeHandshake, organizations:ListHandshakesForAccount, organizations:ListHandshakesForOrganization |
| osis | osis/pipelineendpointconnection | 0 |  | element-arn | osis:ListPipelineEndpointConnections |
| outposts | outposts/asset | 1 |  | child-uncatalogued | outposts:ListAssets |
| outposts | outposts/assetinstance | 1 |  | child-uncatalogued | outposts:ListAssetInstances |
| outposts | outposts/blockinginstance | 2 |  | child-uncatalogued | outposts:ListBlockingInstancesForCapacityTask |
| outposts | outposts/capacitytask | 0 |  | element-written | outposts:GetCapacityTask, outposts:ListCapacityTasks |
| outposts | outposts/order | 0 |  | element-written | outposts:GetOrder, outposts:ListOrders |
| outposts | outposts/outpostbillinginformation | 1 |  | child-uncatalogued | outposts:GetOutpostBillingInformation |
| outposts | outposts/quote | 0 |  | element-written | outposts:GetQuote, outposts:ListQuotes |
| partnercentral | partnercentral/marketplacerevenueshare | 1 |  | smithy-resource | partnercentral:GetMarketplaceRevenueShare, partnercentral:ListMarketplaceRevenueShares |
| partnercentral | partnercentral/marketplacerevenueshareallocation | 2 |  | child-uncatalogued | partnercentral:GetMarketplaceRevenueShareAllocation, partnercentral:ListMarketplaceRevenueShareAllocations |
| partnercentral | partnercentral/revenueattribution | 1 |  | smithy-resource | partnercentral:GetRevenueAttribution, partnercentral:ListRevenueAttributions |
| partnercentral | partnercentral/revenueattributionallocation | 2 |  | child-uncatalogued | partnercentral:GetRevenueAttributionAllocation, partnercentral:ListRevenueAttributionAllocations |
| partnercentral-account | partnercentral-account/connection | 1 |  | smithy-resource | partnercentral-account:GetConnection, partnercentral-account:ListConnections |
| partnercentral-account | partnercentral-account/connectioninvitation | 1 |  | smithy-resource | partnercentral-account:GetConnectionInvitation, partnercentral-account:ListConnectionInvitations |
| partnercentral-account | partnercentral-account/connectionpreference | 0 |  | smithy-resource | partnercentral-account:GetConnectionPreferences |
| partnercentral-account | partnercentral-account/partner | 1 |  | smithy-resource | partnercentral-account:GetPartner, partnercentral-account:ListPartners |
| partnercentral-benefits | partnercentral-benefits/benefit | 1 |  | sr-resource | partnercentral-benefits:GetBenefit, partnercentral-benefits:ListBenefits |
| partnercentral-benefits | partnercentral-benefits/benefitallocation | 1 |  | sr-resource | partnercentral-benefits:GetBenefitAllocation, partnercentral-benefits:ListBenefitAllocations |
| partnercentral-benefits | partnercentral-benefits/benefitapplication | 1 |  | sr-resource | partnercentral-benefits:GetBenefitApplication, partnercentral-benefits:ListBenefitApplications |
| partnercentral-channel | partnercentral-channel/channelhandshake | 0 |  | smithy-resource | partnercentral-channel:ListChannelHandshakes |
| partnercentral-channel | partnercentral-channel/programmanagementaccount | 0 |  | smithy-resource | partnercentral-channel:ListProgramManagementAccounts |
| partnercentral-channel | partnercentral-channel/relationship | 0 |  | smithy-resource | partnercentral-channel:GetRelationship, partnercentral-channel:ListRelationships |
| partnercentral-selling | partnercentral-selling/engagement | 0 |  | smithy-resource | partnercentral-selling:GetEngagement, partnercentral-selling:ListEngagements |
| partnercentral-selling | partnercentral-selling/engagementbyacceptinginvitationtask | 0 |  | smithy-resource | partnercentral-selling:ListEngagementByAcceptingInvitationTasks |
| partnercentral-selling | partnercentral-selling/engagementfromopportunitytask | 0 |  | smithy-resource | partnercentral-selling:ListEngagementFromOpportunityTasks |
| partnercentral-selling | partnercentral-selling/engagementinvitation | 0 |  | smithy-resource | partnercentral-selling:GetEngagementInvitation, partnercentral-selling:ListEngagementInvitations |
| partnercentral-selling | partnercentral-selling/engagementmember | 1 |  | child-uncatalogued | partnercentral-selling:ListEngagementMembers |
| partnercentral-selling | partnercentral-selling/engagementresourceassociation | 1 |  | child-uncatalogued | partnercentral-selling:ListEngagementResourceAssociations |
| partnercentral-selling | partnercentral-selling/opportunity | 0 |  | smithy-resource | partnercentral-selling:GetOpportunity, partnercentral-selling:ListOpportunities |
| partnercentral-selling | partnercentral-selling/opportunityfromengagementtask | 0 |  | smithy-resource | partnercentral-selling:ListOpportunityFromEngagementTasks |
| partnercentral-selling | partnercentral-selling/prospectingfromengagementtask | 0 |  | smithy-resource | partnercentral-selling:ListProspectingFromEngagementTasks |
| partnercentral-selling | partnercentral-selling/resourcesnapshot | 0 |  | smithy-resource | partnercentral-selling:GetResourceSnapshot, partnercentral-selling:ListResourceSnapshots |
| partnercentral-selling | partnercentral-selling/resourcesnapshotjob | 0 |  | smithy-resource | partnercentral-selling:GetResourceSnapshotJob, partnercentral-selling:ListResourceSnapshotJobs |
| partnercentral-selling | partnercentral-selling/solution | 0 |  | smithy-resource | partnercentral-selling:ListSolutions |
| personalize | personalize/batchinferencejob | 0 |  | sr-resource | personalize:DescribeBatchInferenceJob, personalize:ListBatchInferenceJobs |
| personalize | personalize/batchsegmentjob | 0 |  | sr-resource | personalize:DescribeBatchSegmentJob, personalize:ListBatchSegmentJobs |
| personalize | personalize/datadeletionjob | 0 |  | sr-resource | personalize:DescribeDataDeletionJob, personalize:ListDataDeletionJobs |
| personalize | personalize/datasetexportjob | 0 |  | sr-resource | personalize:DescribeDatasetExportJob, personalize:ListDatasetExportJobs |
| personalize | personalize/datasetimportjob | 0 |  | sr-resource | personalize:DescribeDatasetImportJob, personalize:ListDatasetImportJobs |
| personalize | personalize/metricattributionmetric | 0 |  | element-written | personalize:ListMetricAttributionMetrics |
| personalize | personalize/solutionversion | 0 |  | element-written | personalize:DescribeSolutionVersion, personalize:ListSolutionVersions |
| pi | pi/performanceanalysisreport | 3 |  | child-uncatalogued | pi:GetPerformanceAnalysisReport, pi:ListPerformanceAnalysisReports |
| pi | pi/performanceanalysisreportrecommendation | 3 |  | child-uncatalogued | pi:ListPerformanceAnalysisReportRecommendations |
| polly | polly/speechsynthesistask | 0 |  | element-written | polly:GetSpeechSynthesisTask, polly:ListSpeechSynthesisTasks |
| pricing | pricing/pricelist | 0 |  | element-arn | pricing:ListPriceLists |
| pricingplanmanager | pricingplanmanager/subscription | 0 |  | sr-resource | pricingplanmanager:GetSubscription, pricingplanmanager:ListSubscriptions |
| profile | profile/calculatedattribute | 2 |  | sr-resource | profile:BatchGetCalculatedAttributeForProfile, profile:GetCalculatedAttributeForProfile, profile:ListCalculatedAttributesForProfile |
| profile | profile/identityresolutionjob | 1 |  | child-uncatalogued | profile:GetIdentityResolutionJob, profile:ListIdentityResolutionJobs |
| profile | profile/match | 1 |  | child-uncatalogued | profile:GetMatches |
| profile | profile/objecttypeattribute | 2 |  | child-uncatalogued | profile:ListObjectTypeAttributes |
| profile | profile/profile | 1 |  | child-uncatalogued | profile:BatchGetProfile, profile:SearchProfiles |
| profile | profile/profilehistoryrecord | 1 |  | child-uncatalogued | profile:GetProfileHistoryRecord, profile:ListProfileHistoryRecords |
| profile | profile/profileobject | 1 |  | child-uncatalogued | profile:ListProfileObjects |
| profile | profile/rulebasedmatch | 1 |  | child-uncatalogued | profile:ListRuleBasedMatches |
| profile | profile/segmentmembership | 2 |  | child-uncatalogued | profile:GetSegmentMembership |
| profile | profile/segmentsubscriptionevent | 2 |  | child-uncatalogued | profile:ListSegmentSubscriptionEvents |
| profile | profile/similarprofile | 1 |  | child-uncatalogued | profile:GetSimilarProfiles |
| profile | profile/uploadjob | 1 |  | child-uncatalogued | profile:GetUploadJob, profile:ListUploadJobs |
| profile | profile/workflow | 1 |  | child-uncatalogued | profile:GetWorkflow, profile:ListWorkflows |
| proton | proton/componentoutput | 0 |  | smithy-resource | proton:ListComponentOutputs |
| proton | proton/componentprovisionedresource | 0 |  | smithy-resource | proton:ListComponentProvisionedResources |
| proton | proton/environmentoutput | 0 |  | smithy-resource | proton:ListEnvironmentOutputs |
| proton | proton/environmentprovisionedresource | 0 |  | smithy-resource | proton:ListEnvironmentProvisionedResources |
| proton | proton/serviceinstanceoutput | 2 |  | smithy-resource | proton:ListServiceInstanceOutputs |
| proton | proton/serviceinstanceprovisionedresource | 1 |  | smithy-resource | proton:ListServiceInstanceProvisionedResources |
| proton | proton/servicepipelineoutput | 1 |  | smithy-resource | proton:ListServicePipelineOutputs |
| proton | proton/servicepipelineprovisionedresource | 1 |  | smithy-resource | proton:ListServicePipelineProvisionedResources |
| qapps | qapps/category | 1 |  | child-uncatalogued | qapps:GetLibraryItem, qapps:ListCategories |
| qapps | qapps/libraryitem | 1 |  | child-uncatalogued | qapps:ListLibraryItems |
| qbusiness | qbusiness/attachment | 1 |  | child-uncatalogued | qbusiness:ListAttachments |
| qbusiness | qbusiness/chatcontrolsconfiguration | 1 |  | child-uncatalogued | qbusiness:GetChatControlsConfiguration |
| qbusiness | qbusiness/conversation | 1 |  | child-uncatalogued | qbusiness:ListConversations |
| qbusiness | qbusiness/datasourcesyncjob | 3 |  | child-uncatalogued | qbusiness:ListDataSourceSyncJobs |
| qbusiness | qbusiness/document | 2 |  | child-uncatalogued | qbusiness:ListDocuments |
| qbusiness | qbusiness/group | 2 |  | child-uncatalogued | qbusiness:GetGroup, qbusiness:ListGroups |
| qbusiness | qbusiness/message | 2 |  | child-uncatalogued | qbusiness:ListMessages |
| qbusiness | qbusiness/pluginaction | 2 |  | child-uncatalogued | qbusiness:ListPluginActions, qbusiness:ListPluginTypeActions |
| qbusiness | qbusiness/relevantcontent | 1 |  | child-uncatalogued | qbusiness:SearchRelevantContent |
| qbusiness | qbusiness/user | 0 |  | child-uncatalogued | qbusiness:GetUser |
| quicksight | quicksight/app | 0 |  | sr-resource | quicksight:DescribeApp, quicksight:ListApps, quicksight:SearchApps |
| quicksight | quicksight/approvalpolicy | 0 |  | sr-resource | quicksight:DescribeApprovalPolicy, quicksight:ListApprovalPolicies |
| quicksight | quicksight/assetbundleexportjob | 0 |  | sr-resource | quicksight:DescribeAssetBundleExportJob, quicksight:ListAssetBundleExportJobs |
| quicksight | quicksight/assetbundleimportjob | 0 |  | sr-resource | quicksight:DescribeAssetBundleImportJob, quicksight:ListAssetBundleImportJobs |
| quicksight | quicksight/dashboardversion | 1 |  | child-uncatalogued | quicksight:ListDashboardVersions |
| quicksight | quicksight/describeuserlimit | 0 |  | element-arn | quicksight:BatchDescribeUserLimits |
| quicksight | quicksight/dlpsetting | 0 |  | sr-resource | quicksight:DescribeDlpSetting, quicksight:ListDlpSettings |
| quicksight | quicksight/foldermember | 1 |  | child-uncatalogued | quicksight:ListFolderMembers |
| quicksight | quicksight/groupmembership | 2 |  | child-uncatalogued | quicksight:DescribeGroupMembership, quicksight:ListGroupMemberships |
| quicksight | quicksight/ingestion | 1 |  | sr-resource | quicksight:DescribeIngestion, quicksight:ListIngestions |
| quicksight | quicksight/keyregistration | 0 |  | element-written | quicksight:DescribeKeyRegistration |
| quicksight | quicksight/limitsprofile | 0 |  | sr-resource | quicksight:DescribeLimitsProfile, quicksight:ListLimitsProfiles |
| quicksight | quicksight/rolemembership | 2 |  | child-uncatalogued | quicksight:ListRoleMemberships |
| quicksight | quicksight/selfupgrade | 1 |  | child-uncatalogued | quicksight:ListSelfUpgrades |
| quicksight | quicksight/spaceresource | 1 |  | child-uncatalogued | quicksight:ListSpaceResources |
| quicksight | quicksight/templatealias | 1 |  | child-uncatalogued | quicksight:DescribeTemplateAlias, quicksight:ListTemplateAliases |
| quicksight | quicksight/templateversion | 1 |  | child-uncatalogued | quicksight:ListTemplateVersions |
| quicksight | quicksight/themealias | 1 |  | child-uncatalogued | quicksight:DescribeThemeAlias, quicksight:ListThemeAliases |
| quicksight | quicksight/themeversion | 1 |  | child-uncatalogued | quicksight:ListThemeVersions |
| quicksight | quicksight/topicrefreshschedule | 1 |  | child-uncatalogued | quicksight:DescribeTopicRefreshSchedule, quicksight:ListTopicRefreshSchedules |
| quicksight | quicksight/topicreviewedanswer | 1 |  | child-uncatalogued | quicksight:ListTopicReviewedAnswers |
| quicksight | quicksight/usersindexcapacity | 0 |  | element-written | quicksight:ListUsersIndexCapacity |
| ram | ram/permissionassociation | 0 |  | element-arn | ram:ListPermissionAssociations |
| ram | ram/principal | 0 |  | element-arn | ram:ListPrincipals |
| ram | ram/replacepermissionassociationswork | 0 |  | element-written | ram:ListReplacePermissionAssociationsWork |
| ram | ram/resource | 0 |  | element-written | ram:ListPendingInvitationResources, ram:ListResources |
| ram | ram/resourceshareassociation | 0 |  | element-written | ram:GetResourceShareAssociations |
| ram | ram/resourceshareinvitation | 0 |  | sr-resource | ram:GetResourceShareInvitations |
| ram | ram/sourceassociation | 0 |  | element-written | ram:ListSourceAssociations |
| rds | rds/certificate | 0 |  | element-written | rds:DescribeCertificates |
| rds | rds/dbclusterbacktrack | 1 |  | child-uncatalogued | rds:DescribeDBClusterBacktracks |
| rds | rds/dbclusterparameter | 1 |  | element-written | rds:DescribeDBClusterParameters, rds:DescribeEngineDefaultClusterParameters |
| rds | rds/dbclustersnapshotattribute | 1 |  | child-uncatalogued | rds:DescribeDBClusterSnapshotAttributes |
| rds | rds/dblogfile | 1 |  | child-uncatalogued | rds:DescribeDBLogFiles |
| rds | rds/dbparameter | 1 |  | element-written | rds:DescribeDBClusterParameters, rds:DescribeDBParameters, rds:DescribeEngineDefaultClusterParameters, rds:DescribeEngineDefaultParameters |
| rds | rds/dbproxytarget | 1 |  | child-uncatalogued | rds:DescribeDBProxyTargets |
| rds | rds/dbrecommendation | 0 |  | element-written | rds:DescribeDBRecommendations |
| rds | rds/dbsnapshotattribute | 1 |  | child-uncatalogued | rds:DescribeDBSnapshotAttributes |
| rds | rds/event | 0 |  | element-arn | rds:DescribeEvents |
| rds | rds/exporttask | 0 |  | element-written | rds:DescribeExportTasks |
| rds | rds/optiongroupoption | 0 |  | child-uncatalogued | rds:DescribeOptionGroupOptions |
| rds | rds/pendingmaintenanceaction | 0 |  | element-written | rds:DescribePendingMaintenanceActions |
| rds | rds/reserveddbinstancesoffering | 0 |  | element-written | rds:DescribeReservedDBInstancesOfferings |
| rds | rds/validdbinstancemodification | 1 |  | child-uncatalogued | rds:DescribeValidDBInstanceModifications |
| redshift | redshift/authenticationprofile | 0 |  | element-written | redshift:DescribeAuthenticationProfiles |
| redshift | redshift/clusterparameter | 1 |  | element-written | redshift:DescribeClusterParameters, redshift:DescribeDefaultClusterParameters |
| redshift | redshift/clustersecuritygroup | 0 |  | element-written | redshift:DescribeClusterSecurityGroups |
| redshift | redshift/customdomainassociation | 0 |  | element-arn | redshift:DescribeCustomDomainAssociations |
| redshift | redshift/inboundintegration | 0 |  | element-written | redshift:DescribeInboundIntegrations |
| redshift | redshift/partner | 1 |  | child-uncatalogued | redshift:DescribePartners |
| redshift | redshift/qev2idcapplication | 0 |  | sr-resource | redshift:DescribeQev2IdcApplications |
| redshift | redshift/recommendation | 0 |  | element-arn | redshift:ListRecommendations |
| redshift | redshift/reservednode | 0 |  | element-written | redshift:DescribeReservedNodes |
| redshift | redshift/reservednodeoffering | 0 |  | element-written | redshift:DescribeReservedNodeOfferings, redshift:GetReservedNodeExchangeOfferings |
| redshift | redshift/tablerestorestatus | 0 |  | element-written | redshift:DescribeTableRestoreStatus |
| redshift-data | redshift-data/database | 1 |  | child-uncatalogued | redshift-data:ListDatabases |
| redshift-data | redshift-data/schema | 1 |  | child-uncatalogued | redshift-data:ListSchemas |
| redshift-data | redshift-data/statement | 0 |  | element-arn | redshift-data:DescribeStatement, redshift-data:ListStatements |
| redshift-data | redshift-data/table | 1 |  | child-uncatalogued | redshift-data:DescribeTable, redshift-data:ListTables |
| redshift-serverless | redshift-serverless/customdomainassociation | 0 |  | element-arn | redshift-serverless:GetCustomDomainAssociation, redshift-serverless:ListCustomDomainAssociations |
| redshift-serverless | redshift-serverless/reservation | 0 |  | smithy-resource | redshift-serverless:GetReservation, redshift-serverless:ListReservations |
| redshift-serverless | redshift-serverless/reservationoffering | 1 |  | child-uncatalogued | redshift-serverless:GetReservationOffering, redshift-serverless:ListReservationOfferings |
| redshift-serverless | redshift-serverless/scheduledaction | 0 |  | smithy-resource | redshift-serverless:GetScheduledAction, redshift-serverless:ListScheduledActions |
| redshift-serverless | redshift-serverless/snapshotcopyconfiguration | 1 |  | child-uncatalogued | redshift-serverless:ListSnapshotCopyConfigurations |
| redshift-serverless | redshift-serverless/tablerestorestatus | 1 |  | child-uncatalogued | redshift-serverless:GetTableRestoreStatus, redshift-serverless:ListTableRestoreStatus |
| redshift-serverless | redshift-serverless/usagelimit | 0 |  | smithy-resource | redshift-serverless:GetUsageLimit, redshift-serverless:ListUsageLimits |
| refactor-spaces | refactor-spaces/environmentvpc | 1 |  | child-uncatalogued | refactor-spaces:ListEnvironmentVpcs |
| rekognition | rekognition/datasetentry | 3 |  | child-uncatalogued | rekognition:ListDatasetEntries |
| rekognition | rekognition/datasetlabel | 3 |  | child-uncatalogued | rekognition:ListDatasetLabels |
| rekognition | rekognition/face | 0 |  | child-uncatalogued | rekognition:CompareFaces, rekognition:ListFaces, rekognition:SearchFacesByImage |
| rekognition | rekognition/mediaanalysisjob | 0 |  | element-written | rekognition:GetMediaAnalysisJob, rekognition:ListMediaAnalysisJobs |
| rekognition | rekognition/projectpolicy | 1 |  | child-uncatalogued | rekognition:ListProjectPolicies |
| rekognition | rekognition/user | 1 |  | child-uncatalogued | rekognition:ListUsers, rekognition:SearchUsers, rekognition:SearchUsersByImage |
| repostspace | repostspace/channel | 1 |  | child-uncatalogued | repostspace:GetChannel, repostspace:ListChannels |
| resiliencehub | resiliencehub/alarmrecommendation | 1 |  | child-uncatalogued | resiliencehub:ListAlarmRecommendations |
| resiliencehub | resiliencehub/appassessmentcompliancedrift | 1 |  | child-uncatalogued | resiliencehub:ListAppAssessmentComplianceDrifts |
| resiliencehub | resiliencehub/appassessmentresourcedrift | 1 |  | child-uncatalogued | resiliencehub:ListAppAssessmentResourceDrifts |
| resiliencehub | resiliencehub/appcomponentcompliance | 1 |  | child-uncatalogued | resiliencehub:ListAppComponentCompliances |
| resiliencehub | resiliencehub/appcomponentrecommendation | 1 |  | child-uncatalogued | resiliencehub:ListAppComponentRecommendations |
| resiliencehub | resiliencehub/appinputsource | 1 |  | child-uncatalogued | resiliencehub:ListAppInputSources |
| resiliencehub | resiliencehub/appversion | 1 |  | child-uncatalogued | resiliencehub:DescribeAppVersion, resiliencehub:ListAppVersions |
| resiliencehub | resiliencehub/appversionappcomponent | 1 |  | child-uncatalogued | resiliencehub:DescribeAppVersionAppComponent, resiliencehub:ListAppVersionAppComponents |
| resiliencehub | resiliencehub/appversionresource | 1 |  | child-uncatalogued | resiliencehub:DescribeAppVersionResource, resiliencehub:ListAppVersionResources |
| resiliencehub | resiliencehub/appversionresourcemapping | 1 |  | child-uncatalogued | resiliencehub:ListAppVersionResourceMappings |
| resiliencehub | resiliencehub/assertion | 1 |  | child-uncatalogued | resiliencehub:ListAssertions |
| resiliencehub | resiliencehub/dependency | 0 |  | element-written | resiliencehub:ListDependencies |
| resiliencehub | resiliencehub/failuremodeassessment | 1 |  | child-uncatalogued | resiliencehub:ListFailureModeAssessments |
| resiliencehub | resiliencehub/failuremodefinding | 1 |  | child-uncatalogued | resiliencehub:GetFailureModeFinding, resiliencehub:ListFailureModeFindings |
| resiliencehub | resiliencehub/inputsource | 1 |  | child-uncatalogued | resiliencehub:ListInputSources |
| resiliencehub | resiliencehub/policy | 0 |  | sr-resource | resiliencehub:GetPolicy, resiliencehub:ListPolicies |
| resiliencehub | resiliencehub/report | 0 |  | element-written | resiliencehub:ListReports |
| resiliencehub | resiliencehub/resolvedtestruntargetresource | 2 |  | child-uncatalogued | resiliencehub:ListResolvedTestRunTargetResources |
| resiliencehub | resiliencehub/resource | 1 |  | child-uncatalogued | resiliencehub:ListResources |
| resiliencehub | resiliencehub/resourcegroupingrecommendation | 0 |  | element-created | resiliencehub:ListResourceGroupingRecommendations |
| resiliencehub | resiliencehub/service | 0 |  | sr-resource | resiliencehub:GetService, resiliencehub:ListServices |
| resiliencehub | resiliencehub/serviceevent | 1 |  | child-uncatalogued | resiliencehub:ListServiceEvents |
| resiliencehub | resiliencehub/servicefunction | 1 |  | child-uncatalogued | resiliencehub:ListServiceFunctions |
| resiliencehub | resiliencehub/servicetopologyedge | 1 |  | child-uncatalogued | resiliencehub:ListServiceTopologyEdges |
| resiliencehub | resiliencehub/soprecommendation | 1 |  | child-uncatalogued | resiliencehub:ListSopRecommendations |
| resiliencehub | resiliencehub/system | 0 |  | sr-resource | resiliencehub:GetSystem, resiliencehub:ListSystems |
| resiliencehub | resiliencehub/systemevent | 1 |  | child-uncatalogued | resiliencehub:ListSystemEvents |
| resiliencehub | resiliencehub/test | 1 |  | child-uncatalogued | resiliencehub:GetTest, resiliencehub:ListTests |
| resiliencehub | resiliencehub/testrecommendation | 1 |  | child-uncatalogued | resiliencehub:ListTestRecommendations |
| resiliencehub | resiliencehub/testrun | 1 |  | child-uncatalogued | resiliencehub:GetTestRun, resiliencehub:ListTestRuns |
| resiliencehub | resiliencehub/testrundependency | 2 |  | child-uncatalogued | resiliencehub:ListTestRunDependencies |
| resiliencehub | resiliencehub/testrunevent | 2 |  | child-uncatalogued | resiliencehub:ListTestRunEvents |
| resiliencehub | resiliencehub/testrunsourceevent | 2 |  | child-uncatalogued | resiliencehub:ListTestRunSourceEvents |
| resiliencehub | resiliencehub/testtemplate | 0 |  | sr-resource | resiliencehub:GetTestTemplate, resiliencehub:ListTestTemplates |
| resiliencehub | resiliencehub/unsupportedappversionresource | 1 |  | child-uncatalogued | resiliencehub:ListUnsupportedAppVersionResources |
| resiliencehub | resiliencehub/userjourney | 1 |  | child-uncatalogued | resiliencehub:GetUserJourney, resiliencehub:ListUserJourneys |
| resource-explorer-2 | resource-explorer-2/indexesformember | 0 |  | element-arn | resource-explorer-2:ListIndexesForMembers |
| resource-explorer-2 | resource-explorer-2/resource | 0 |  | element-arn | resource-explorer-2:ListResources, resource-explorer-2:Search |
| resource-groups | resource-groups/groupingstatus | 1 |  | child-uncatalogued | resource-groups:ListGroupingStatuses |
| resource-groups | resource-groups/resource | 0 |  | element-arn | resource-groups:ListGroupResources, resource-groups:SearchResources |
| route53 | route53/change | 0 |  | sr-resource | route53:GetChange |
| route53 | route53/cidrblock | 1 |  | child-uncatalogued | route53:ListCidrBlocks |
| route53 | route53/cidrlocation | 1 |  | child-uncatalogued | route53:ListCidrLocations |
| route53-recovery-cluster | route53-recovery-cluster/routingcontrol | 0 |  | element-arn | route53-recovery-cluster:ListRoutingControls |
| route53-recovery-control-config | route53-recovery-control-config/associatedroute53healthcheck | 1 |  | child-uncatalogued | route53-recovery-control-config:ListAssociatedRoute53HealthChecks |
| route53-recovery-readiness | route53-recovery-readiness/cellreadinesssummary | 1 |  | child-uncatalogued | route53-recovery-readiness:GetCellReadinessSummary, route53-recovery-readiness:GetRecoveryGroupReadinessSummary |
| route53-recovery-readiness | route53-recovery-readiness/readinesscheckresourcestatus | 2 |  | child-uncatalogued | route53-recovery-readiness:GetReadinessCheckResourceStatus |
| route53-recovery-readiness | route53-recovery-readiness/readinesscheckstatus | 1 |  | child-uncatalogued | route53-recovery-readiness:GetReadinessCheckStatus |
| route53globalresolver | route53globalresolver/firewalldomain | 1 |  | child-uncatalogued | route53globalresolver:ListFirewallDomains |
| route53globalresolver | route53globalresolver/managedfirewalldomainlist | 0 |  | smithy-resource | route53globalresolver:GetManagedFirewallDomainList, route53globalresolver:ListManagedFirewallDomainLists |
| route53globalresolver | route53globalresolver/shareddnsview | 0 |  | element-written | route53globalresolver:ListSharedDNSViews |
| route53resolver | route53resolver/firewalldomain | 1 |  | child-uncatalogued | route53resolver:ListFirewallDomains |
| route53resolver | route53resolver/firewallrule | 1 |  | child-uncatalogued | route53resolver:ListFirewallRules |
| route53resolver | route53resolver/resolverendpointipaddress | 1 |  | child-uncatalogued | route53resolver:ListResolverEndpointIpAddresses |
| rtbfabric | rtbfabric/certificateassociation | 1 |  | child-uncatalogued | rtbfabric:GetCertificateAssociation, rtbfabric:ListCertificateAssociations |
| rum | rum/appmonitordata | 1 |  | child-uncatalogued | rum:GetAppMonitorData |
| rum | rum/rummetricdefinition | 1 |  | child-uncatalogued | rum:BatchGetRumMetricDefinitions |
| rum | rum/rummetricsdestination | 1 |  | child-uncatalogued | rum:ListRumMetricsDestinations |
| s3 | s3/bucketanalyticsconfiguration | 1 |  | child-uncatalogued | s3:GetBucketAnalyticsConfiguration, s3:ListBucketAnalyticsConfigurations |
| s3 | s3/bucketintelligenttieringconfiguration | 1 |  | child-uncatalogued | s3:GetBucketIntelligentTieringConfiguration, s3:ListBucketIntelligentTieringConfigurations |
| s3 | s3/bucketinventoryconfiguration | 1 |  | child-uncatalogued | s3:GetBucketInventoryConfiguration, s3:ListBucketInventoryConfigurations |
| s3 | s3/bucketmetricsconfiguration | 1 |  | child-uncatalogued | s3:GetBucketMetricsConfiguration, s3:ListBucketMetricsConfigurations |
| s3 | s3/calleraccessgrant | 0 |  | element-arn | s3:ListCallerAccessGrants |
| s3 | s3/job | 0 |  | sr-resource | s3:DescribeJob, s3:ListJobs |
| s3 | s3/multipartupload | 1 |  | child-uncatalogued | s3:ListMultipartUploads |
| s3 | s3/object | 1 |  | sr-resource | s3:GetObject, s3:HeadObject, s3:ListObjects, s3:ListObjectsV2 |
| s3 | s3/objectannotation | 2 |  | child-uncatalogued | s3:GetObjectAnnotation, s3:ListObjectAnnotations |
| s3 | s3/objectversion | 1 |  | child-uncatalogued | s3:ListObjectVersions |
| s3vectors | s3vectors/vector | 2 |  | smithy-resource | s3vectors:ListVectors, s3vectors:QueryVectors |
| sagemaker | sagemaker/aibenchmarkjob | 0 |  | sr-resource | sagemaker:DescribeAIBenchmarkJob, sagemaker:ListAIBenchmarkJobs |
| sagemaker | sagemaker/airecommendationjob | 0 |  | sr-resource | sagemaker:DescribeAIRecommendationJob, sagemaker:ListAIRecommendationJobs |
| sagemaker | sagemaker/alias | 2 |  | child-uncatalogued | sagemaker:ListAliases |
| sagemaker | sagemaker/artifact | 0 |  | sr-resource | sagemaker:DescribeArtifact, sagemaker:ListArtifacts |
| sagemaker | sagemaker/association | 0 |  | element-arn | sagemaker:ListAssociations |
| sagemaker | sagemaker/automljob | 0 |  | sr-resource | sagemaker:DescribeAutoMLJob, sagemaker:DescribeAutoMLJobV2, sagemaker:ListAutoMLJobs |
| sagemaker | sagemaker/candidate | 1 |  | child-uncatalogued | sagemaker:ListCandidatesForAutoMLJob |
| sagemaker | sagemaker/clusterevent | 1 |  | child-uncatalogued | sagemaker:DescribeClusterEvent, sagemaker:ListClusterEvents |
| sagemaker | sagemaker/clusternode | 1 |  | child-uncatalogued | sagemaker:DescribeClusterNode, sagemaker:ListClusterNodes |
| sagemaker | sagemaker/compilationjob | 0 |  | sr-resource | sagemaker:DescribeCompilationJob, sagemaker:ListCompilationJobs |
| sagemaker | sagemaker/edgepackagingjob | 0 |  | sr-resource | sagemaker:DescribeEdgePackagingJob, sagemaker:ListEdgePackagingJobs |
| sagemaker | sagemaker/humanloop | 0 |  | sr-resource | sagemaker:DescribeHumanLoop, sagemaker:ListHumanLoops |
| sagemaker | sagemaker/hyperparametertuningjob | 0 |  | sr-resource | sagemaker:DescribeHyperParameterTuningJob, sagemaker:ListHyperParameterTuningJobs |
| sagemaker | sagemaker/inferencerecommendationsjob | 0 |  | sr-resource | sagemaker:DescribeInferenceRecommendationsJob, sagemaker:ListInferenceRecommendationsJobs |
| sagemaker | sagemaker/inferencerecommendationsjobstep | 1 |  | child-uncatalogued | sagemaker:ListInferenceRecommendationsJobSteps |
| sagemaker | sagemaker/job | 0 |  | sr-resource | sagemaker:DescribeJob, sagemaker:ListJobs |
| sagemaker | sagemaker/labelingjob | 0 |  | sr-resource | sagemaker:DescribeLabelingJob, sagemaker:ListLabelingJobs, sagemaker:ListLabelingJobsForWorkteam |
| sagemaker | sagemaker/lineage | 0 |  | element-arn | sagemaker:QueryLineage |
| sagemaker | sagemaker/modelcardexportjob | 1 |  | sr-resource | sagemaker:DescribeModelCardExportJob, sagemaker:ListModelCardExportJobs |
| sagemaker | sagemaker/modelcardversion | 1 |  | child-uncatalogued | sagemaker:ListModelCardVersions |
| sagemaker | sagemaker/monitoringalert | 1 |  | child-uncatalogued | sagemaker:ListMonitoringAlerts |
| sagemaker | sagemaker/monitoringalerthistory | 0 |  | element-written | sagemaker:ListMonitoringAlertHistory |
| sagemaker | sagemaker/monitoringexecution | 0 |  | element-arn | sagemaker:ListMonitoringExecutions |
| sagemaker | sagemaker/optimizationjob | 0 |  | sr-resource | sagemaker:DescribeOptimizationJob, sagemaker:ListOptimizationJobs |
| sagemaker | sagemaker/pipelineexecution | 1 |  | sr-resource | sagemaker:DescribePipelineExecution, sagemaker:ListPipelineExecutions |
| sagemaker | sagemaker/pipelineparameter | 2 |  | child-uncatalogued | sagemaker:ListPipelineParametersForExecution |
| sagemaker | sagemaker/pipelineversion | 1 |  | child-uncatalogued | sagemaker:ListPipelineVersions |
| sagemaker | sagemaker/record | 1 |  | child-uncatalogued | sagemaker:GetRecord, sagemaker:ListRecords |
| sagemaker | sagemaker/resourcecatalog | 0 |  | element-arn | sagemaker:ListResourceCatalogs |
| sagemaker | sagemaker/stagedevice | 0 |  | child-uncatalogued | sagemaker:ListStageDevices |
| sagemaker | sagemaker/subscribedworkteam | 0 |  | element-arn | sagemaker:DescribeSubscribedWorkteam, sagemaker:ListSubscribedWorkteams |
| sagemaker | sagemaker/trainingjob | 0 |  | sr-resource | sagemaker:DescribeTrainingJob, sagemaker:ListTrainingJobs, sagemaker:ListTrainingJobsForHyperParameterTuningJob |
| sagemaker | sagemaker/trainingplanextensionhistory | 1 |  | child-uncatalogued | sagemaker:DescribeTrainingPlanExtensionHistory |
| sagemaker | sagemaker/transformjob | 0 |  | sr-resource | sagemaker:DescribeTransformJob, sagemaker:ListTransformJobs |
| sagemaker | sagemaker/trialcomponent | 0 |  | element-written | sagemaker:DescribeTrialComponent, sagemaker:ListTrialComponents |
| sagemaker | sagemaker/ultraserver | 1 |  | child-uncatalogued | sagemaker:ListUltraServersByReservedCapacity |
| sagemaker-geospatial | sagemaker-geospatial/earthobservationjob | 0 |  | smithy-resource | sagemaker-geospatial:GetEarthObservationJob, sagemaker-geospatial:ListEarthObservationJobs |
| sagemaker-geospatial | sagemaker-geospatial/vectorenrichmentjob | 0 |  | smithy-resource | sagemaker-geospatial:ExportVectorEnrichmentJob, sagemaker-geospatial:GetVectorEnrichmentJob, sagemaker-geospatial:ListVectorEnrichmentJobs |
| schemas | schemas/schemaversion | 2 |  | child-uncatalogued | schemas:ListSchemaVersions |
| scn | scn/dataintegrationevent | 1 |  | child-uncatalogued | scn:GetDataIntegrationEvent, scn:ListDataIntegrationEvents |
| scn | scn/dataintegrationflowexecution | 2 |  | child-uncatalogued | scn:GetDataIntegrationFlowExecution, scn:ListDataIntegrationFlowExecutions |
| sdb | sdb/export | 0 |  | sr-resource | sdb:GetExport, sdb:ListExports |
| secretsmanager | secretsmanager/secretvalue | 0 |  | element-written | secretsmanager:BatchGetSecretValue, secretsmanager:GetSecretValue |
| secretsmanager | secretsmanager/secretversionid | 1 |  | child-uncatalogued | secretsmanager:ListSecretVersionIds |
| security-ir | security-ir/comment | 1 |  | child-uncatalogued | security-ir:ListComments |
| security-ir | security-ir/investigation | 1 |  | child-uncatalogued | security-ir:ListInvestigations |
| securityagent | securityagent/artifact | 1 |  | child-uncatalogued | securityagent:GetArtifact, securityagent:ListArtifacts |
| securityagent | securityagent/codereview | 1 |  | child-uncatalogued | securityagent:ListCodeReviews |
| securityagent | securityagent/codereviewjob | 1 |  | child-uncatalogued | securityagent:BatchGetCodeReviewJobs, securityagent:ListCodeReviewJobsForCodeReview |
| securityagent | securityagent/codereviewjobtask | 1 |  | child-uncatalogued | securityagent:ListCodeReviewJobTasks |
| securityagent | securityagent/discoveredendpoint | 1 |  | child-uncatalogued | securityagent:ListDiscoveredEndpoints |
| securityagent | securityagent/finding | 1 |  | child-uncatalogued | securityagent:BatchGetFindings, securityagent:ListFindings |
| securityagent | securityagent/getcodereview | 1 |  | child-uncatalogued | securityagent:BatchGetCodeReviewJobTasks, securityagent:BatchGetCodeReviews |
| securityagent | securityagent/getpentest | 1 |  | child-uncatalogued | securityagent:BatchGetPentestJobTasks, securityagent:BatchGetPentests |
| securityagent | securityagent/getthreatmodel | 1 |  | child-uncatalogued | securityagent:BatchGetThreatModelJobTasks, securityagent:BatchGetThreatModels |
| securityagent | securityagent/integratedresource | 1 |  | child-uncatalogued | securityagent:ListIntegratedResources |
| securityagent | securityagent/membership | 1 |  | child-uncatalogued | securityagent:ListMemberships |
| securityagent | securityagent/pentestjob | 1 |  | child-uncatalogued | securityagent:BatchGetPentestJobs, securityagent:ListPentestJobsForPentest |
| securityagent | securityagent/pentestjobtask | 1 |  | child-uncatalogued | securityagent:ListPentestJobTasks |
| securityagent | securityagent/securityrequirement | 1 |  | child-uncatalogued | securityagent:BatchGetSecurityRequirements, securityagent:ListSecurityRequirements |
| securityagent | securityagent/threat | 1 |  | child-uncatalogued | securityagent:BatchGetThreats, securityagent:ListThreats |
| securityagent | securityagent/threatmodel | 1 |  | child-uncatalogued | securityagent:ListThreatModels |
| securityagent | securityagent/threatmodeljob | 1 |  | child-uncatalogued | securityagent:BatchGetThreatModelJobs, securityagent:ListThreatModelJobs |
| securityagent | securityagent/threatmodeljobtask | 1 |  | child-uncatalogued | securityagent:ListThreatModelJobTasks |
| securityhub | securityhub/actiontarget | 0 |  | element-written | securityhub:DescribeActionTargets |
| securityhub | securityhub/finding | 0 |  | element-written | securityhub:GetFindings, securityhub:GetFindingsV2 |
| securityhub | securityhub/findinghistory | 1 |  | child-uncatalogued | securityhub:GetFindingHistory |
| securityhub | securityhub/member | 0 |  | child-uncatalogued | securityhub:GetMembers, securityhub:ListMembers |
| securityhub | securityhub/product | 0 |  | sr-resource | securityhub:DescribeProducts, securityhub:DescribeProductsV2 |
| securityhub | securityhub/resource | 0 |  | element-written | securityhub:GetResourcesV2 |
| securityhub | securityhub/standardscontrol | 1 |  | child-uncatalogued | securityhub:DescribeStandardsControls |
| securityhub | securityhub/standardscontrolassociation | 1 |  | child-uncatalogued | securityhub:ListStandardsControlAssociations |
| securitylake | securitylake/datalakesource | 1 |  | child-uncatalogued | securitylake:GetDataLakeSources |
| serverlessrepo | serverlessrepo/applicationdependency | 1 |  | child-uncatalogued | serverlessrepo:ListApplicationDependencies |
| serverlessrepo | serverlessrepo/applicationpolicy | 1 |  | child-uncatalogued | serverlessrepo:GetApplicationPolicy |
| serverlessrepo | serverlessrepo/applicationversion | 1 |  | child-uncatalogued | serverlessrepo:ListApplicationVersions |
| servicecatalog | servicecatalog/associatedattributegroup | 1 |  | child-uncatalogued | servicecatalog:ListAssociatedAttributeGroups |
| servicecatalog | servicecatalog/budget | 2 |  | child-uncatalogued | servicecatalog:ListBudgetsForResource |
| servicecatalog | servicecatalog/launchpath | 1 |  | child-uncatalogued | servicecatalog:ListLaunchPaths |
| servicecatalog | servicecatalog/portfolioaccess | 1 |  | child-uncatalogued | servicecatalog:ListPortfolioAccess |
| servicecatalog | servicecatalog/product | 0 |  | element-written | servicecatalog:DescribeProduct, servicecatalog:DescribeProductView, servicecatalog:SearchProducts |
| servicecatalog | servicecatalog/provisionedproductoutput | 0 |  | element-written | servicecatalog:GetProvisionedProductOutputs |
| servicecatalog | servicecatalog/record | 0 |  | element-written | servicecatalog:DescribeRecord, servicecatalog:ListRecordHistory |
| servicequotas | servicequotas/requestedservicequotachange | 0 |  | element-written | servicequotas:GetRequestedServiceQuotaChange, servicequotas:ListRequestedServiceQuotaChangeHistory, servicequotas:ListRequestedServiceQuotaChangeHistoryByQuota |
| servicequotas | servicequotas/servicequotaincreaserequestsintemplate | 0 |  | element-written | servicequotas:GetServiceQuotaIncreaseRequestFromTemplate, servicequotas:ListServiceQuotaIncreaseRequestsInTemplate |
| ses | ses/addresslistimportjob | 1 |  | child-uncatalogued | ses:GetAddressListImportJob, ses:ListAddressListImportJobs |
| ses | ses/archiveexport | 1 |  | child-uncatalogued | ses:GetArchiveExport, ses:ListArchiveExports |
| ses | ses/archivesearch | 1 |  | child-uncatalogued | ses:GetArchiveSearch, ses:ListArchiveSearches |
| ses | ses/contact | 0 |  | child-uncatalogued | ses:GetContact, ses:ListContacts |
| ses | ses/deliverabilitydashboardoption | 0 |  | element-written | ses:GetDeliverabilityDashboardOptions |
| ses | ses/deliverabilitytestreport | 0 |  | sr-resource | ses:GetDeliverabilityTestReport, ses:ListDeliverabilityTestReports |
| ses | ses/domaindeliverabilitycampaign | 1 |  | child-uncatalogued | ses:GetDomainDeliverabilityCampaign, ses:ListDomainDeliverabilityCampaigns |
| ses | ses/emailidentitycertificate | 1 |  | child-uncatalogued | ses:ListEmailIdentityCertificates |
| ses | ses/exportjob | 0 |  | sr-resource | ses:GetExportJob, ses:ListExportJobs |
| ses | ses/identity | 0 |  | sr-resource | ses:ListIdentities |
| ses | ses/importjob | 0 |  | sr-resource | ses:GetImportJob, ses:ListImportJobs |
| ses | ses/messageinsight | 1 |  | child-uncatalogued | ses:GetMessageInsights |
| ses | ses/recommendation | 0 |  | element-arn | ses:ListRecommendations |
| ses | ses/resourcetenant | 1 |  | child-uncatalogued | ses:ListResourceTenants |
| ses | ses/tenantresource | 1 |  | child-uncatalogued | ses:ListTenantResources |
| shield | shield/attack | 0 |  | sr-resource | shield:DescribeAttack, shield:ListAttacks |
| shield | shield/resource | 1 |  | child-uncatalogued | shield:ListResourcesInProtectionGroup |
| signer | signer/signingjob | 0 |  | sr-resource | signer:DescribeSigningJob, signer:ListSigningJobs |
| sms-voice | sms-voice/availablephonenumber | 0 |  | element-written | sms-voice:ListAvailablePhoneNumbers |
| sms-voice | sms-voice/configurationseteventdestination | 1 |  | child-uncatalogued | sms-voice:GetConfigurationSetEventDestinations |
| sms-voice | sms-voice/notifyconfiguration | 0 |  | sr-resource | sms-voice:DescribeNotifyConfigurations |
| sms-voice | sms-voice/pooloriginationidentity | 1 |  | child-uncatalogued | sms-voice:ListPoolOriginationIdentities |
| sms-voice | sms-voice/rcsagent | 0 |  | sr-resource | sms-voice:DescribeRcsAgents |
| sms-voice | sms-voice/rcsagentcountrylaunchstatus | 1 |  | child-uncatalogued | sms-voice:DescribeRcsAgentCountryLaunchStatus |
| sms-voice | sms-voice/registrationassociation | 1 |  | child-uncatalogued | sms-voice:ListRegistrationAssociations |
| sms-voice | sms-voice/registrationfieldvalue | 1 |  | child-uncatalogued | sms-voice:DescribeRegistrationFieldValues |
| snow-device-management | snow-device-management/deviceresource | 1 |  | child-uncatalogued | snow-device-management:ListDeviceResources |
| snow-device-management | snow-device-management/execution | 1 |  | smithy-resource | snow-device-management:DescribeExecution, snow-device-management:ListExecutions |
| snowball | snowball/address | 0 |  | element-written | snowball:DescribeAddress, snowball:DescribeAddresses, snowball:ListPickupLocations |
| snowball | snowball/cluster | 0 |  | element-written | snowball:DescribeCluster, snowball:ListClusters |
| snowball | snowball/job | 0 |  | element-written | snowball:DescribeJob, snowball:ListClusterJobs, snowball:ListJobs |
| snowball | snowball/longtermpricing | 0 |  | element-written | snowball:ListLongTermPricing |
| snowball | snowball/serviceversion | 0 |  | child-uncatalogued | snowball:ListServiceVersions |
| sns | sns/endpoint | 1 |  | child-uncatalogued | sns:ListEndpointsByPlatformApplication |
| sns | sns/originationnumber | 0 |  | element-created | sns:ListOriginationNumbers |
| sns | sns/platformapplication | 0 |  | element-written | sns:ListPlatformApplications |
| social-messaging | social-messaging/whatsappflow | 1 |  | child-uncatalogued | social-messaging:ListWhatsAppFlows |
| social-messaging | social-messaging/whatsappflowasset | 1 |  | child-uncatalogued | social-messaging:ListWhatsAppFlowAssets |
| social-messaging | social-messaging/whatsappmessagetemplate | 1 |  | child-uncatalogued | social-messaging:ListWhatsAppMessageTemplates |
| social-messaging | social-messaging/whatsapptemplatelibrary | 1 |  | child-uncatalogued | social-messaging:ListWhatsAppTemplateLibrary |
| sqs | sqs/deadlettersourcequeue | 1 |  | child-uncatalogued | sqs:ListDeadLetterSourceQueues |
| sqs | sqs/message | 1 |  | child-uncatalogued | sqs:ReceiveMessage |
| ssm | ssm/activation | 0 |  | element-written | ssm:DescribeActivations |
| ssm | ssm/associationexecution | 1 |  | child-uncatalogued | ssm:DescribeAssociationExecutions |
| ssm | ssm/associationexecutiontarget | 1 |  | child-uncatalogued | ssm:DescribeAssociationExecutionTargets |
| ssm | ssm/associationversion | 1 |  | child-uncatalogued | ssm:ListAssociationVersions |
| ssm | ssm/automationexecution | 0 |  | sr-resource | ssm:DescribeAutomationExecutions, ssm:GetAutomationExecution |
| ssm | ssm/automationstepexecution | 1 |  | child-uncatalogued | ssm:DescribeAutomationStepExecutions |
| ssm | ssm/availablepatch | 0 |  | element-written | ssm:DescribeAvailablePatches |
| ssm | ssm/cloudconnector | 0 |  | sr-resource | ssm:GetCloudConnector, ssm:ListCloudConnectors, ssm:ValidateCloudConnector |
| ssm | ssm/command | 0 |  | element-written | ssm:ListCommands |
| ssm | ssm/commandinvocation | 0 |  | element-written | ssm:GetCommandInvocation, ssm:ListCommandInvocations |
| ssm | ssm/complianceitem | 0 |  | element-written | ssm:ListComplianceItems |
| ssm | ssm/documentpermission | 1 |  | child-uncatalogued | ssm:DescribeDocumentPermission |
| ssm | ssm/documentversion | 1 |  | child-uncatalogued | ssm:ListDocumentVersions |
| ssm | ssm/effectiveinstanceassociation | 1 |  | child-uncatalogued | ssm:DescribeEffectiveInstanceAssociations |
| ssm | ssm/instanceassociationsstatus | 1 |  | child-uncatalogued | ssm:DescribeInstanceAssociationsStatus |
| ssm | ssm/instancepatch | 1 |  | child-uncatalogued | ssm:DescribeInstancePatches |
| ssm | ssm/instancepatchstate | 0 |  | child-uncatalogued | ssm:DescribeInstancePatchStates, ssm:DescribeInstancePatchStatesForPatchGroup |
| ssm | ssm/inventorydeletion | 0 |  | element-written | ssm:DescribeInventoryDeletions |
| ssm | ssm/maintenancewindowexecution | 0 |  | child-uncatalogued | ssm:DescribeMaintenanceWindowExecutions, ssm:GetMaintenanceWindowExecution |
| ssm | ssm/maintenancewindowexecutiontask | 0 |  | child-uncatalogued | ssm:DescribeMaintenanceWindowExecutionTasks, ssm:GetMaintenanceWindowExecutionTask |
| ssm | ssm/maintenancewindowexecutiontaskinvocation | 0 |  | child-uncatalogued | ssm:DescribeMaintenanceWindowExecutionTaskInvocations, ssm:GetMaintenanceWindowExecutionTaskInvocation |
| ssm | ssm/nodessummary | 1 |  | child-uncatalogued | ssm:ListNodesSummary |
| ssm | ssm/opsitem | 0 |  | sr-resource | ssm:DescribeOpsItems, ssm:GetOpsItem |
| ssm | ssm/opsitemevent | 0 |  | element-written | ssm:ListOpsItemEvents |
| ssm | ssm/opsitemrelateditem | 0 |  | element-written | ssm:ListOpsItemRelatedItems |
| ssm | ssm/parameterhistory | 1 |  | child-uncatalogued | ssm:GetParameterHistory |
| ssm | ssm/resourcecompliancesummary | 0 |  | element-written | ssm:ListResourceComplianceSummaries |
| ssm | ssm/resourcepolicy | 1 |  | child-uncatalogued | ssm:GetResourcePolicies |
| ssm | ssm/session | 0 |  | sr-resource | ssm:DescribeSessions |
| ssm-contacts | ssm-contacts/engagement | 0 |  | sr-resource | ssm-contacts:DescribeEngagement, ssm-contacts:ListEngagements |
| ssm-contacts | ssm-contacts/page | 1 |  | sr-resource | ssm-contacts:DescribePage, ssm-contacts:ListPagesByContact, ssm-contacts:ListPagesByEngagement |
| ssm-contacts | ssm-contacts/pagereceipt | 2 |  | child-uncatalogued | ssm-contacts:ListPageReceipts |
| ssm-contacts | ssm-contacts/pageresolution | 2 |  | child-uncatalogued | ssm-contacts:ListPageResolutions |
| ssm-contacts | ssm-contacts/rotationoverride | 1 |  | child-uncatalogued | ssm-contacts:GetRotationOverride, ssm-contacts:ListRotationOverrides |
| ssm-contacts | ssm-contacts/rotationshift | 1 |  | child-uncatalogued | ssm-contacts:ListPreviewRotationShifts, ssm-contacts:ListRotationShifts |
| ssm-incidents | ssm-incidents/incidentfinding | 1 |  | child-uncatalogued | ssm-incidents:ListIncidentFindings |
| ssm-incidents | ssm-incidents/incidentrecord | 0 |  | sr-resource | ssm-incidents:GetIncidentRecord, ssm-incidents:ListIncidentRecords |
| ssm-incidents | ssm-incidents/relateditem | 1 |  | child-uncatalogued | ssm-incidents:ListRelatedItems |
| ssm-incidents | ssm-incidents/resourcepolicy | 1 |  | child-uncatalogued | ssm-incidents:GetResourcePolicies |
| ssm-incidents | ssm-incidents/timelineevent | 1 |  | child-uncatalogued | ssm-incidents:GetTimelineEvent, ssm-incidents:ListTimelineEvents |
| ssm-quicksetup | ssm-quicksetup/configuration | 0 |  | element-arn | ssm-quicksetup:ListConfigurations |
| ssm-sap | ssm-sap/configurationcheckoperation | 0 |  | child-uncatalogued | ssm-sap:GetConfigurationCheckOperation, ssm-sap:ListConfigurationCheckOperations |
| ssm-sap | ssm-sap/operation | 0 |  | child-uncatalogued | ssm-sap:GetOperation, ssm-sap:ListOperations |
| ssm-sap | ssm-sap/subcheckresult | 1 |  | child-uncatalogued | ssm-sap:ListSubCheckResults |
| ssm-sap | ssm-sap/subcheckruleresult | 2 |  | child-uncatalogued | ssm-sap:ListSubCheckRuleResults |
| sso | sso/accountassignmentcreationstatus | 1 |  | child-uncatalogued | sso:DescribeAccountAssignmentCreationStatus, sso:DescribeAccountAssignmentDeletionStatus, sso:ListAccountAssignmentCreationStatus, sso:ListAccountAssignmentDeletionStatus |
| sso | sso/applicationaccessscope | 0 |  | smithy-resource | sso:GetApplicationAccessScope, sso:ListApplicationAccessScopes |
| sso | sso/applicationauthenticationmethod | 0 |  | smithy-resource | sso:GetApplicationAuthenticationMethod, sso:ListApplicationAuthenticationMethods |
| sso | sso/applicationgrant | 0 |  | smithy-resource | sso:GetApplicationGrant, sso:ListApplicationGrants |
| sso | sso/customermanagedpolicyreference | 2 |  | child-uncatalogued | sso:ListCustomerManagedPolicyReferencesInPermissionSet |
| sso | sso/managedpolicy | 2 |  | child-uncatalogued | sso:ListManagedPoliciesInPermissionSet |
| sso | sso/permissionsetprovisioningstatus | 1 |  | child-uncatalogued | sso:DescribePermissionSetProvisioningStatus, sso:ListPermissionSetProvisioningStatus |
| sso | sso/permissionsetsprovisioned | 1 |  | child-uncatalogued | sso:ListPermissionSetsProvisionedToAccount |
| sso | sso/region | 1 |  | child-uncatalogued | sso:DescribeRegion, sso:ListRegions |
| states | states/execution | 0 |  | sr-resource | states:DescribeExecution, states:ListExecutions |
| states | states/executionhistory | 1 |  | child-uncatalogued | states:GetExecutionHistory |
| states | states/maprun | 1 |  | sr-resource | states:DescribeMapRun, states:ListMapRuns |
| storagegateway | storagegateway/automatictapecreationpolicy | 0 |  | element-arn | storagegateway:ListAutomaticTapeCreationPolicies |
| storagegateway | storagegateway/cache | 1 |  | child-uncatalogued | storagegateway:DescribeCache |
| storagegateway | storagegateway/localdisk | 1 |  | child-uncatalogued | storagegateway:ListLocalDisks |
| storagegateway | storagegateway/nfsfileshare | 1 |  | child-uncatalogued | storagegateway:DescribeNFSFileShares |
| storagegateway | storagegateway/smbfileshare | 1 |  | child-uncatalogued | storagegateway:DescribeSMBFileShares |
| storagegateway | storagegateway/tapearchive | 0 |  | element-written | storagegateway:DescribeTapeArchives |
| storagegateway | storagegateway/taperecoverypoint | 1 |  | child-uncatalogued | storagegateway:DescribeTapeRecoveryPoints |
| storagegateway | storagegateway/uploadbuffer | 1 |  | child-uncatalogued | storagegateway:DescribeUploadBuffer |
| storagegateway | storagegateway/volumerecoverypoint | 1 |  | child-uncatalogued | storagegateway:ListVolumeRecoveryPoints |
| storagegateway | storagegateway/workingstorage | 1 |  | child-uncatalogued | storagegateway:DescribeWorkingStorage |
| support | support/case | 0 |  | element-written | support:DescribeCases |
| support | support/communication | 1 |  | child-uncatalogued | support:DescribeCommunications |
| support | support/trustedadvisorcheckrefreshstatus | 0 |  | child-uncatalogued | support:DescribeTrustedAdvisorCheckRefreshStatuses |
| supportauthz | supportauthz/supportpermit | 0 |  | sr-resource | supportauthz:GetSupportPermit, supportauthz:ListSupportPermits |
| supportauthz | supportauthz/supportpermitrequest | 0 |  | sr-resource | supportauthz:ListSupportPermitRequests |
| swf | swf/workflowexecutionhistory | 1 |  | child-uncatalogued | swf:GetWorkflowExecutionHistory |
| synthetics | synthetics/canarieslastrun | 0 |  | element-written | synthetics:DescribeCanariesLastRun |
| synthetics | synthetics/canaryrun | 1 |  | child-uncatalogued | synthetics:GetCanaryRuns |
| tagging | tagging/resource | 0 |  | element-arn | tagging:GetResources |
| tax | tax/supplementaltaxregistration | 0 |  | element-written | tax:ListSupplementalTaxRegistrations |
| tax | tax/taxregistration | 0 |  | element-written | tax:GetTaxRegistration, tax:ListTaxRegistrations |
| timestream | timestream/batchloadtask | 0 |  | element-created | timestream:DescribeBatchLoadTask, timestream:ListBatchLoadTasks |
| timestream-influxdb | timestream-influxdb/dbbackup | 0 |  | smithy-resource | timestream-influxdb:GetDbBackup, timestream-influxdb:ListDbBackups |
| transcribe | transcribe/callanalyticsjob | 0 |  | sr-resource | transcribe:GetCallAnalyticsJob, transcribe:ListCallAnalyticsJobs |
| transcribe | transcribe/medicalscribejob | 0 |  | sr-resource | transcribe:GetMedicalScribeJob, transcribe:ListMedicalScribeJobs |
| transcribe | transcribe/medicaltranscriptionjob | 0 |  | sr-resource | transcribe:GetMedicalTranscriptionJob, transcribe:ListMedicalTranscriptionJobs |
| transcribe | transcribe/transcriptionjob | 0 |  | sr-resource | transcribe:GetTranscriptionJob, transcribe:ListTranscriptionJobs |
| transfer | transfer/access | 1 |  | child-uncatalogued | transfer:DescribeAccess, transfer:ListAccesses |
| transfer | transfer/execution | 1 |  | child-uncatalogued | transfer:DescribeExecution, transfer:ListExecutions |
| translate | translate/texttranslationjob | 0 |  | element-written | translate:DescribeTextTranslationJob, translate:ListTextTranslationJobs |
| trustedadvisor | trustedadvisor/organizationrecommendation | 0 |  | element-arn | trustedadvisor:GetOrganizationRecommendation, trustedadvisor:ListOrganizationRecommendations |
| trustedadvisor | trustedadvisor/organizationrecommendationaccount | 1 |  | child-uncatalogued | trustedadvisor:ListOrganizationRecommendationAccounts |
| trustedadvisor | trustedadvisor/organizationrecommendationresource | 1 |  | child-uncatalogued | trustedadvisor:ListOrganizationRecommendationResources |
| trustedadvisor | trustedadvisor/recommendation | 0 |  | element-arn | trustedadvisor:GetRecommendation, trustedadvisor:ListRecommendations, trustedadvisor:ListRecommendationsForResource |
| trustedadvisor | trustedadvisor/recommendationresource | 1 |  | child-uncatalogued | trustedadvisor:ListRecommendationResources |
| voiceid | voiceid/fraudster | 1 |  | child-uncatalogued | voiceid:DescribeFraudster, voiceid:ListFraudsters |
| voiceid | voiceid/fraudsterregistrationjob | 1 |  | child-uncatalogued | voiceid:DescribeFraudsterRegistrationJob, voiceid:ListFraudsterRegistrationJobs |
| voiceid | voiceid/speaker | 1 |  | child-uncatalogued | voiceid:DescribeSpeaker, voiceid:ListSpeakers |
| voiceid | voiceid/speakerenrollmentjob | 1 |  | child-uncatalogued | voiceid:DescribeSpeakerEnrollmentJob, voiceid:ListSpeakerEnrollmentJobs |
| voiceid | voiceid/watchlist | 1 |  | child-uncatalogued | voiceid:DescribeWatchlist, voiceid:ListWatchlists |
| vpc-lattice | vpc-lattice/servicenetworkvpcendpointassociation | 1 |  | child-uncatalogued | vpc-lattice:ListServiceNetworkVpcEndpointAssociations |
| vpc-lattice | vpc-lattice/target | 1 |  | child-uncatalogued | vpc-lattice:ListTargets |
| waf | waf/loggingconfiguration | 0 |  | element-written | waf:GetLoggingConfiguration, waf:ListLoggingConfigurations |
| waf-regional | waf-regional/loggingconfiguration | 0 |  | element-written | waf-regional:GetLoggingConfiguration, waf-regional:ListLoggingConfigurations |
| wafv2 | wafv2/apikey | 0 |  | element-created | wafv2:ListAPIKeys |
| wafv2 | wafv2/availablemanagedrulegroupversion | 0 |  | child-uncatalogued | wafv2:ListAvailableManagedRuleGroupVersions |
| wafv2 | wafv2/settlementrecord | 0 |  | element-arn | wafv2:ListSettlementRecords |
| wellarchitected | wellarchitected/agentcontext | 1 |  | child-uncatalogued | wellarchitected:GetAgentContext, wellarchitected:ListAgentContexts |
| wellarchitected | wellarchitected/agentgoal | 1 |  | child-uncatalogued | wellarchitected:GetAgentGoal, wellarchitected:ListAgentGoals |
| wellarchitected | wellarchitected/agentprofile | 0 |  | sr-resource | wellarchitected:GetAgentProfile, wellarchitected:ListAgentProfiles |
| wellarchitected | wellarchitected/agentrecommendation | 1 |  | sr-resource | wellarchitected:GetAgentRecommendation, wellarchitected:ListAgentRecommendations |
| wellarchitected | wellarchitected/agentrecommendationgeneration | 1 |  | child-uncatalogued | wellarchitected:GetAgentRecommendationGeneration, wellarchitected:ListAgentRecommendationGenerations |
| wellarchitected | wellarchitected/agentrecommendationitem | 2 |  | child-uncatalogued | wellarchitected:ListAgentRecommendationItems |
| wellarchitected | wellarchitected/answer | 2 |  | child-uncatalogued | wellarchitected:GetAnswer, wellarchitected:ListAnswers |
| wellarchitected | wellarchitected/checkdetail | 1 |  | child-uncatalogued | wellarchitected:ListCheckDetails |
| wellarchitected | wellarchitected/checksummary | 1 |  | child-uncatalogued | wellarchitected:ListCheckSummaries |
| wellarchitected | wellarchitected/consolidatedreport | 0 |  | element-written | wellarchitected:GetConsolidatedReport |
| wellarchitected | wellarchitected/lensreview | 1 |  | child-uncatalogued | wellarchitected:GetLensReview, wellarchitected:ListLensReviews |
| wellarchitected | wellarchitected/lensreviewimprovement | 2 |  | child-uncatalogued | wellarchitected:ListLensReviewImprovements |
| wellarchitected | wellarchitected/lensshare | 1 |  | child-uncatalogued | wellarchitected:ListLensShares |
| wellarchitected | wellarchitected/milestone | 1 |  | child-uncatalogued | wellarchitected:GetMilestone, wellarchitected:ListMilestones |
| wellarchitected | wellarchitected/profilenotification | 0 |  | element-written | wellarchitected:ListProfileNotifications |
| wellarchitected | wellarchitected/profileshare | 1 |  | child-uncatalogued | wellarchitected:ListProfileShares |
| wellarchitected | wellarchitected/reviewtemplateanswer | 2 |  | child-uncatalogued | wellarchitected:GetReviewTemplateAnswer, wellarchitected:ListReviewTemplateAnswers |
| wellarchitected | wellarchitected/shareinvitation | 0 |  | element-written | wellarchitected:ListShareInvitations |
| wellarchitected | wellarchitected/templateshare | 1 |  | child-uncatalogued | wellarchitected:ListTemplateShares |
| wellarchitected | wellarchitected/workloadshare | 1 |  | child-uncatalogued | wellarchitected:ListWorkloadShares |
| wickr | wickr/bot | 1 |  | child-uncatalogued | wickr:GetBot, wickr:ListBots |
| wickr | wickr/device | 2 |  | child-uncatalogued | wickr:ListDevicesForUser |
| wickr | wickr/network | 0 |  | sr-resource | wickr:GetNetwork, wickr:ListNetworks |
| wickr | wickr/networksetting | 1 |  | child-uncatalogued | wickr:GetNetworkSettings |
| wickr | wickr/securitygroup | 1 |  | child-uncatalogued | wickr:GetSecurityGroup, wickr:ListSecurityGroups |
| wickr | wickr/user | 1 |  | child-uncatalogued | wickr:GetUser, wickr:ListSecurityGroupUsers, wickr:ListUsers |
| wisdom | wisdom/importjob | 1 |  | child-uncatalogued | wisdom:GetImportJob, wisdom:ListImportJobs |
| wisdom | wisdom/message | 2 |  | child-uncatalogued | wisdom:ListMessages |
| wisdom | wisdom/model | 1 |  | child-uncatalogued | wisdom:ListModels |
| wisdom | wisdom/session | 1 |  | sr-resource | wisdom:GetSession, wisdom:SearchSessions |
| wisdom | wisdom/span | 2 |  | child-uncatalogued | wisdom:ListSpans |
| workdocs | workdocs/activity | 0 |  | element-written | workdocs:DescribeActivities |
| workdocs | workdocs/comment | 2 |  | child-uncatalogued | workdocs:DescribeComments |
| workdocs | workdocs/documentversion | 1 |  | child-uncatalogued | workdocs:DescribeDocumentVersions, workdocs:GetDocumentVersion |
| workdocs | workdocs/folder | 0 |  | element-written | workdocs:DescribeRootFolders, workdocs:GetFolder |
| workdocs | workdocs/notificationsubscription | 1 |  | child-uncatalogued | workdocs:DescribeNotificationSubscriptions |
| workdocs | workdocs/resource | 0 |  | element-written | workdocs:DescribeFolderContents, workdocs:GetDocument, workdocs:GetResources, workdocs:SearchResources |
| workdocs | workdocs/resourcepermission | 1 |  | child-uncatalogued | workdocs:DescribeResourcePermissions |
| workdocs | workdocs/user | 0 |  | element-written | workdocs:DescribeUsers, workdocs:GetCurrentUser |
| workmail | workmail/accesscontrolrule | 1 |  | child-uncatalogued | workmail:ListAccessControlRules |
| workmail | workmail/alias | 1 |  | child-uncatalogued | workmail:ListAliases |
| workmail | workmail/availabilityconfiguration | 1 |  | child-uncatalogued | workmail:ListAvailabilityConfigurations, workmail:TestAvailabilityConfiguration |
| workmail | workmail/defaultretentionpolicy | 1 |  | child-uncatalogued | workmail:GetDefaultRetentionPolicy |
| workmail | workmail/group | 1 |  | child-uncatalogued | workmail:DescribeGroup, workmail:ListGroups, workmail:ListGroupsForEntity |
| workmail | workmail/groupmember | 1 |  | child-uncatalogued | workmail:ListGroupMembers |
| workmail | workmail/impersonationrole | 1 |  | child-uncatalogued | workmail:GetImpersonationRole, workmail:ListImpersonationRoles |
| workmail | workmail/mailboxexportjob | 1 |  | child-uncatalogued | workmail:DescribeMailboxExportJob, workmail:ListMailboxExportJobs |
| workmail | workmail/mailboxpermission | 1 |  | child-uncatalogued | workmail:ListMailboxPermissions |
| workmail | workmail/maildomain | 1 |  | child-uncatalogued | workmail:ListMailDomains |
| workmail | workmail/mobiledeviceaccessoverride | 1 |  | child-uncatalogued | workmail:GetMobileDeviceAccessOverride, workmail:ListMobileDeviceAccessOverrides |
| workmail | workmail/mobiledeviceaccessrule | 1 |  | child-uncatalogued | workmail:ListMobileDeviceAccessRules |
| workmail | workmail/personalaccesstoken | 1 |  | child-uncatalogued | workmail:ListPersonalAccessTokens |
| workmail | workmail/resource | 1 |  | child-uncatalogued | workmail:DescribeResource, workmail:ListResources |
| workmail | workmail/resourcedelegate | 1 |  | child-uncatalogued | workmail:ListResourceDelegates |
| workmail | workmail/user | 1 |  | child-uncatalogued | workmail:DescribeUser, workmail:ListUsers |
| workspaces | workspaces/accountlink | 0 |  | element-written | workspaces:GetAccountLink, workspaces:ListAccountLinks |
| workspaces | workspaces/applicationassociation | 1 |  | child-uncatalogued | workspaces:DescribeApplicationAssociations |
| workspaces | workspaces/bundleassociation | 1 |  | child-uncatalogued | workspaces:DescribeBundleAssociations |
| workspaces | workspaces/clientproperty | 1 |  | child-uncatalogued | workspaces:DescribeClientProperties |
| workspaces | workspaces/connectclientaddin | 1 |  | child-uncatalogued | workspaces:DescribeConnectClientAddIns |
| workspaces | workspaces/connectionaliaspermission | 1 |  | child-uncatalogued | workspaces:DescribeConnectionAliasPermissions |
| workspaces | workspaces/imageassociation | 1 |  | child-uncatalogued | workspaces:DescribeImageAssociations |
| workspaces | workspaces/workspaceassociation | 1 |  | child-uncatalogued | workspaces:DescribeWorkspaceAssociations |
| workspaces | workspaces/workspacespoolsession | 1 |  | child-uncatalogued | workspaces:DescribeWorkspacesPoolSessions |
| workspaces-web | workspaces-web/session | 1 |  | child-uncatalogued | workspaces-web:GetSession, workspaces-web:ListSessions |
| xray | xray/indexingrule | 0 |  | element-written | xray:GetIndexingRules |
| xray | xray/insightsummary | 0 |  | element-arn | xray:GetInsightSummaries |
| xray | xray/tracegraph | 0 |  | child-uncatalogued | xray:GetServiceGraph, xray:GetTraceGraph |
| xray | xray/tracesummary | 0 |  | element-arn | xray:GetTraceSummaries |


## AZURE

**Coverage:** 16.1% (398/2478 listable) · depth0 46.4% · depth1 3.2% · depth2 4.2% · depth3 0.0% · depth4 0.0% · depth5 0.0% · attribute 0 · excluded 917 · disco-only 8 (0 unexplained)

Pins: azure-sdk-for-go@f3847e8925003b3ae044a0ce2eaebd0e9983ed90

The denominator is every candidate the provider's own SDK can list that the extractor classified `resource` — not every API operation, and not a curated list. Attributes (detail reads), catalogs (provider-published, read-only) and non-resources are outside it and are listed below. Each row names the rule that classified it; the table below gives the coverage of each rule, so a rule that admits rows no scanner can close is visible as a low percentage rather than as a smaller number.

| Admitting rule | Covered | Uncovered | % |
|---|---|---|---|
| item-write | 388 | 1625 | 19.3 |
| arm-envelope | 10 | 455 | 2.2 |

| Service | Covered | Uncovered | % |
|---|---|---|---|
| microsoft.apimanagement | 1 | 108 | 0.9 |
| microsoft.network | 57 | 100 | 36.3 |
| microsoft.web | 10 | 93 | 9.7 |
| microsoft.sql | 41 | 73 | 36.0 |
| microsoft.security | 1 | 58 | 1.7 |
| microsoft.machinelearningservices | 2 | 50 | 3.8 |
| microsoft.migrate | 1 | 45 | 2.2 |
| microsoft.securityinsights | 0 | 41 | 0.0 |
| microsoft.synapse | 2 | 41 | 4.7 |
| microsoft.billing | 0 | 38 | 0.0 |
| microsoft.documentdb | 4 | 37 | 9.8 |
| microsoft.app | 5 | 31 | 13.9 |
| microsoft.recoveryservices | 1 | 31 | 3.1 |
| microsoft.dbforpostgresql | 2 | 30 | 6.2 |
| microsoft.insights | 1 | 29 | 3.3 |
| microsoft.avs | 1 | 28 | 3.4 |
| microsoft.cognitiveservices | 2 | 28 | 6.7 |
| microsoft.containerservice | 3 | 28 | 9.7 |
| microsoft.automation | 1 | 26 | 3.7 |
| microsoft.devcenter | 3 | 26 | 10.3 |
| microsoft.dbformysql | 1 | 25 | 3.8 |
| microsoft.edge | 0 | 25 | 0.0 |
| microsoft.netapp | 1 | 23 | 4.2 |
| microsoft.storage | 1 | 20 | 4.8 |
| microsoft.authorization | 5 | 19 | 20.8 |
| microsoft.compute | 26 | 19 | 57.8 |
| microsoft.containerregistry | 1 | 18 | 5.3 |
| microsoft.eventgrid | 9 | 18 | 33.3 |
| microsoft.logic | 3 | 18 | 14.3 |
| microsoft.m365securityandcompliance | 0 | 18 | 0.0 |
| microsoft.storsimple | 0 | 18 | 0.0 |
| microsoft.devtestlab | 2 | 17 | 10.5 |
| microsoft.cdn | 2 | 16 | 11.1 |
| microsoft.azurestackhci | 8 | 15 | 34.8 |
| microsoft.customerinsights | 0 | 14 | 0.0 |
| oracle.database | 0 | 14 | 0.0 |
| paloaltonetworks.cloudngfw | 0 | 14 | 0.0 |
| microsoft.dbformariadb | 0 | 13 | 0.0 |
| microsoft.discovery | 0 | 13 | 0.0 |
| microsoft.hybridnetwork | 1 | 13 | 7.1 |
| microsoft.servicebus | 1 | 13 | 7.1 |
| microsoft.signalrservice | 2 | 13 | 13.3 |
| microsoft.azureresiliencemanagement | 1 | 12 | 7.7 |
| microsoft.databoxedge | 1 | 12 | 7.7 |
| microsoft.datafactory | 1 | 12 | 7.7 |
| microsoft.dataprotection | 2 | 12 | 14.3 |
| microsoft.iotoperations | 1 | 12 | 7.7 |
| microsoft.testbase | 0 | 12 | 0.0 |
| microsoft.chaos | 1 | 11 | 8.3 |
| microsoft.eventhub | 2 | 11 | 15.4 |
| microsoft.operationalinsights | 2 | 11 | 15.4 |
| microsoft.providerhub | 0 | 11 | 0.0 |
| microsoft.cache | 2 | 10 | 16.7 |
| microsoft.datareplication | 2 | 10 | 16.7 |
| microsoft.deviceregistry | 3 | 10 | 23.1 |
| microsoft.kusto | 1 | 10 | 9.1 |
| purestorage.block | 0 | 10 | 0.0 |
| microsoft.billingbenefits | 0 | 9 | 0.0 |
| microsoft.desktopvirtualization | 5 | 9 | 35.7 |
| microsoft.keyvault | 2 | 9 | 18.2 |
| microsoft.managednetworkfabric | 17 | 9 | 65.4 |
| microsoft.media | 0 | 9 | 0.0 |
| microsoft.mission | 0 | 9 | 0.0 |
| microsoft.resources | 1 | 9 | 10.0 |
| microsoft.servicefabric | 2 | 9 | 18.2 |
| microsoft.confluent | 0 | 8 | 0.0 |
| microsoft.datashare | 1 | 8 | 11.1 |
| microsoft.healthcareapis | 2 | 8 | 20.0 |
| microsoft.hybridcompute | 2 | 8 | 20.0 |
| microsoft.networkcloud | 13 | 8 | 61.9 |
| microsoft.workloads | 2 | 8 | 20.0 |
| microsoft.advisor | 0 | 7 | 0.0 |
| microsoft.apicenter | 1 | 7 | 12.5 |
| microsoft.batch | 1 | 7 | 12.5 |
| microsoft.costmanagement | 0 | 7 | 0.0 |
| microsoft.datamigration | 1 | 7 | 12.5 |
| microsoft.devices | 2 | 7 | 22.2 |
| microsoft.hybriddata | 0 | 7 | 0.0 |
| microsoft.kubernetesconfiguration | 0 | 7 | 0.0 |
| microsoft.relay | 1 | 7 | 12.5 |
| microsoft.securitydevops | 0 | 7 | 0.0 |
| microsoft.servicefabricmesh | 0 | 7 | 0.0 |
| commvault.contentstore | 0 | 6 | 0.0 |
| microsoft.azuresphere | 1 | 6 | 14.3 |
| microsoft.datalakeanalytics | 0 | 6 | 0.0 |
| microsoft.deploymentmanager | 0 | 6 | 0.0 |
| microsoft.horizondb | 2 | 6 | 25.0 |
| microsoft.iotsecurity | 0 | 6 | 0.0 |
| microsoft.storagecache | 1 | 6 | 14.3 |
| microsoft.storagemover | 1 | 6 | 14.3 |
| microsoft.storagesync | 1 | 6 | 14.3 |
| microsoft.appcomplianceautomation | 0 | 5 | 0.0 |
| microsoft.appconfiguration | 1 | 5 | 16.7 |
| microsoft.cloudhealth | 1 | 5 | 16.7 |
| microsoft.communication | 2 | 5 | 28.6 |
| microsoft.computelimit | 0 | 5 | 0.0 |
| microsoft.dashboard | 1 | 5 | 16.7 |
| microsoft.datadog | 0 | 5 | 0.0 |
| microsoft.hybridconnectivity | 1 | 5 | 16.7 |
| microsoft.hybridcontainerservice | 1 | 5 | 16.7 |
| microsoft.integrationspaces | 1 | 5 | 16.7 |
| microsoft.logz | 0 | 5 | 0.0 |
| microsoft.marketplace | 0 | 5 | 0.0 |
| microsoft.notificationhubs | 1 | 5 | 16.7 |
| microsoft.servicenetworking | 1 | 5 | 16.7 |
| microsoft.support | 0 | 5 | 0.0 |
| nginx.nginxplus | 0 | 5 | 0.0 |
| dynatrace.observability | 0 | 4 | 0.0 |
| microsoft.agfoodplatform | 0 | 4 | 0.0 |
| microsoft.blueprint | 2 | 4 | 33.3 |
| microsoft.botservice | 1 | 4 | 20.0 |
| microsoft.connectedvmwarevsphere | 7 | 4 | 63.6 |
| microsoft.databasewatcher | 1 | 4 | 20.0 |
| microsoft.datalakestore | 0 | 4 | 0.0 |
| microsoft.deviceupdate | 1 | 4 | 20.0 |
| microsoft.digitaltwins | 1 | 4 | 20.0 |
| microsoft.durabletask | 1 | 4 | 20.0 |
| microsoft.elasticsan | 1 | 4 | 20.0 |
| microsoft.hardwaresecuritymodules | 1 | 4 | 20.0 |
| microsoft.hdinsight | 1 | 4 | 20.0 |
| microsoft.impact | 0 | 4 | 0.0 |
| microsoft.kubernetesruntime | 0 | 4 | 0.0 |
| microsoft.labservices | 2 | 4 | 33.3 |
| microsoft.managednetwork | 0 | 4 | 0.0 |
| microsoft.monitor | 0 | 4 | 0.0 |
| microsoft.offazurespringboot | 1 | 4 | 20.0 |
| microsoft.peering | 3 | 4 | 42.9 |
| microsoft.powerbi | 0 | 4 | 0.0 |
| microsoft.quota | 1 | 4 | 20.0 |
| microsoft.scvmm | 5 | 4 | 55.6 |
| microsoft.streamanalytics | 2 | 4 | 33.3 |
| microsoft.timeseriesinsights | 0 | 4 | 0.0 |
| microsoft.aadiam | 0 | 3 | 0.0 |
| microsoft.alertsmanagement | 0 | 3 | 0.0 |
| microsoft.automanage | 3 | 3 | 50.0 |
| microsoft.capacity | 0 | 3 | 0.0 |
| microsoft.containerinstance | 1 | 3 | 25.0 |
| microsoft.databricks | 2 | 3 | 40.0 |
| microsoft.devhub | 1 | 3 | 25.0 |
| microsoft.education | 0 | 3 | 0.0 |
| microsoft.elastic | 1 | 3 | 25.0 |
| microsoft.fileshares | 1 | 3 | 25.0 |
| microsoft.iotfirmwaredefense | 1 | 3 | 25.0 |
| microsoft.maps | 1 | 3 | 25.0 |
| microsoft.mixedreality | 0 | 3 | 0.0 |
| microsoft.networkanalytics | 0 | 3 | 0.0 |
| microsoft.orbital | 1 | 3 | 25.0 |
| microsoft.programmableconnectivity | 0 | 3 | 0.0 |
| microsoft.purview | 1 | 3 | 25.0 |
| microsoft.redhatopenshift | 1 | 3 | 25.0 |
| microsoft.search | 1 | 3 | 25.0 |
| microsoft.servicelinker | 0 | 3 | 0.0 |
| microsoft.standbypool | 2 | 3 | 40.0 |
| microsoft.subscription | 0 | 3 | 0.0 |
| microsoft.visualstudio | 0 | 3 | 0.0 |
| microsoft.vmwarecloudsimple | 0 | 3 | 0.0 |
| mongodb.atlas | 0 | 3 | 0.0 |
| newrelic.observability | 0 | 3 | 0.0 |
| informatica.datamanagement | 0 | 2 | 0.0 |
| microsoft.aad | 0 | 2 | 0.0 |
| microsoft.applink | 0 | 2 | 0.0 |
| microsoft.azuredata | 0 | 2 | 0.0 |
| microsoft.billingtrust | 0 | 2 | 0.0 |
| microsoft.blockchain | 0 | 2 | 0.0 |
| microsoft.certificateregistration | 1 | 2 | 33.3 |
| microsoft.computeschedule | 0 | 2 | 0.0 |
| microsoft.connectedcache | 2 | 2 | 50.0 |
| microsoft.delegatednetwork | 0 | 2 | 0.0 |
| microsoft.domainregistration | 1 | 2 | 33.3 |
| microsoft.engagementfabric | 0 | 2 | 0.0 |
| microsoft.features | 0 | 2 | 0.0 |
| microsoft.hanaonazure | 0 | 2 | 0.0 |
| microsoft.loadtestservice | 1 | 2 | 33.3 |
| microsoft.management | 1 | 2 | 33.3 |
| microsoft.operationsmanagement | 1 | 2 | 33.3 |
| microsoft.portal | 0 | 2 | 0.0 |
| microsoft.powerplatform | 2 | 2 | 50.0 |
| microsoft.scheduler | 0 | 2 | 0.0 |
| microsoft.storagepool | 0 | 2 | 0.0 |
| microsoft.virtualmachineimages | 1 | 2 | 33.3 |
| microsoft.voiceservices | 0 | 2 | 0.0 |
| arizeai.observabilityeval | 0 | 1 | 0.0 |
| astronomer.astro | 0 | 1 | 0.0 |
| dell.storage | 0 | 1 | 0.0 |
| lambdatest.hyperexecute | 0 | 1 | 0.0 |
| microsoft.agricultureplatform | 0 | 1 | 0.0 |
| microsoft.attestation | 1 | 1 | 50.0 |
| microsoft.azurearcdata | 4 | 1 | 80.0 |
| microsoft.azureplaywrightservice | 1 | 1 | 50.0 |
| microsoft.baremetalinfrastructure | 1 | 1 | 50.0 |
| microsoft.codesigning | 1 | 1 | 50.0 |
| microsoft.computebulkactions | 0 | 1 | 0.0 |
| microsoft.confidentialledger | 1 | 1 | 50.0 |
| microsoft.consumption | 0 | 1 | 0.0 |
| microsoft.customerlockbox | 0 | 1 | 0.0 |
| microsoft.customproviders | 1 | 1 | 50.0 |
| microsoft.datacatalog | 0 | 1 | 0.0 |
| microsoft.dependencymap | 1 | 1 | 50.0 |
| microsoft.devops | 0 | 1 | 0.0 |
| microsoft.extendedlocation | 1 | 1 | 50.0 |
| microsoft.fluidrelay | 1 | 1 | 50.0 |
| microsoft.guestconfiguration | 0 | 1 | 0.0 |
| microsoft.healthdataaiservices | 1 | 1 | 50.0 |
| microsoft.importexport | 0 | 1 | 0.0 |
| microsoft.maintenance | 3 | 1 | 75.0 |
| microsoft.managedidentity | 1 | 1 | 50.0 |
| microsoft.managedops | 0 | 1 | 0.0 |
| microsoft.managementpartner | 0 | 1 | 0.0 |
| microsoft.networkfunction | 1 | 1 | 50.0 |
| microsoft.openenergyplatform | 0 | 1 | 0.0 |
| microsoft.programenrollment | 0 | 1 | 0.0 |
| microsoft.resourcegraph | 0 | 1 | 0.0 |
| microsoft.resourcehealth | 0 | 1 | 0.0 |
| microsoft.saas | 1 | 1 | 50.0 |
| microsoft.serialconsole | 0 | 1 | 0.0 |
| microsoft.sqlvirtualmachine | 2 | 1 | 66.7 |
| microsoft.weightsandbiases | 0 | 1 | 0.0 |
| microsoft.windowsesu | 0 | 1 | 0.0 |
| microsoft.windowsiot | 0 | 1 | 0.0 |
| microsoft.workloadmonitor | 0 | 1 | 0.0 |
| napster.companionapi | 0 | 1 | 0.0 |
| pinecone.vectordb | 0 | 1 | 0.0 |
| qumulo.storage | 0 | 1 | 0.0 |
| microsoft.analysisservices | 1 | 0 | 100.0 |
| microsoft.azurefleet | 1 | 0 | 100.0 |
| microsoft.azurelargeinstance | 2 | 0 | 100.0 |
| microsoft.databox | 1 | 0 | 100.0 |
| microsoft.devopsinfrastructure | 1 | 0 | 100.0 |
| microsoft.edgeorder | 3 | 0 | 100.0 |
| microsoft.edgezones | 1 | 0 | 100.0 |
| microsoft.fabric | 1 | 0 | 100.0 |
| microsoft.graphservices | 1 | 0 | 100.0 |
| microsoft.healthbot | 1 | 0 | 100.0 |
| microsoft.iotcentral | 1 | 0 | 100.0 |
| microsoft.kubernetes | 1 | 0 | 100.0 |
| microsoft.managedservices | 2 | 0 | 100.0 |
| microsoft.onlineexperimentation | 1 | 0 | 100.0 |
| microsoft.policyinsights | 2 | 0 | 100.0 |
| microsoft.powerbidedicated | 2 | 0 | 100.0 |
| microsoft.quantum | 1 | 0 | 100.0 |
| microsoft.resourceconnector | 1 | 0 | 100.0 |
| microsoft.solutions | 3 | 0 | 100.0 |
| microsoft.storageactions | 1 | 0 | 100.0 |
| microsoft.storagediscovery | 1 | 0 | 100.0 |

### Uncovered (listable, no scanner) (2080)

| Service | Key | Depth | Scope | Rule | Ops |
| --- | --- | --- | --- | --- | --- |
| arizeai.observabilityeval | arizeai.observabilityeval/organizations | 0 | subscription | item-write | armarizeaiobservabilityeval:Organizations.ListByResourceGroup, armarizeaiobservabilityeval:Organizations.ListBySubscription |
| astronomer.astro | astronomer.astro/organizations | 0 | subscription | item-write | armastro:Organizations.ListByResourceGroup, armastro:Organizations.ListBySubscription |
| commvault.contentstore | commvault.contentstore/cloudaccounts | 0 | subscription | item-write | armcommvaultcontentstore:CloudAccounts.ListByResourceGroup, armcommvaultcontentstore:CloudAccounts.ListBySubscription |
| commvault.contentstore | commvault.contentstore/cloudaccounts/plans | 1 | resource-group | item-write | armcommvaultcontentstore:Plans.ListByCloudAccount |
| commvault.contentstore | commvault.contentstore/cloudaccounts/protectiongroups | 1 | resource-group | item-write | armcommvaultcontentstore:ProtectionGroups.ListByCloudAccount |
| commvault.contentstore | commvault.contentstore/cloudaccounts/protectiongroups/protecteditems | 2 | resource-group | arm-envelope | armcommvaultcontentstore:ProtectedItems.ListByProtectionGroup |
| commvault.contentstore | commvault.contentstore/cloudaccounts/rolemappings | 1 | resource-group | item-write | armcommvaultcontentstore:RoleMappings.List |
| commvault.contentstore | commvault.contentstore/cloudaccounts/storages | 1 | resource-group | item-write | armcommvaultcontentstore:Storages.ListByCloudAccount |
| dell.storage | dell.storage/filesystems | 0 | subscription | item-write | armdellstorage:FileSystems.ListByResourceGroup, armdellstorage:FileSystems.ListBySubscription |
| dynatrace.observability | dynatrace.observability/monitors | 0 | subscription | item-write | armdynatrace:Monitors.ListByResourceGroup, armdynatrace:Monitors.ListBySubscriptionID |
| dynatrace.observability | dynatrace.observability/monitors/monitoredsubscriptions | 1 | resource-group | item-write | armdynatrace:MonitoredSubscriptions.List |
| dynatrace.observability | dynatrace.observability/monitors/singlesignonconfigurations | 1 | resource-group | item-write | armdynatrace:SingleSignOn.List |
| dynatrace.observability | dynatrace.observability/monitors/tagrules | 1 | resource-group | item-write | armdynatrace:TagRules.List |
| informatica.datamanagement | informatica.datamanagement/organizations | 0 | subscription | item-write | arminformaticadatamgmt:Organizations.ListByResourceGroup, arminformaticadatamgmt:Organizations.ListBySubscription |
| informatica.datamanagement | informatica.datamanagement/organizations/serverlessruntimes | 1 | resource-group | item-write | arminformaticadatamgmt:ServerlessRuntimes.ListByInformaticaOrganizationResource |
| lambdatest.hyperexecute | lambdatest.hyperexecute/organizations | 0 | subscription | item-write | armlambdatesthyperexecute:Organizations.ListByResourceGroup, armlambdatesthyperexecute:Organizations.ListBySubscription |
| microsoft.aad | microsoft.aad/domainservices | 0 | subscription | item-write | armdomainservices:Client.List, armdomainservices:Client.ListByResourceGroup |
| microsoft.aad | microsoft.aad/domainservices/oucontainer | 1 | resource-group | item-write | armdomainservices:OuContainer.List |
| microsoft.aadiam | microsoft.aadiam/privatelinkforazuread | 0 | subscription | item-write | armaad:PrivateLinkForAzureAd.List, armaad:PrivateLinkForAzureAd.ListBySubscription |
| microsoft.aadiam | microsoft.aadiam/privatelinkforazuread/privateendpointconnections | 1 | resource-group | item-write | armaad:PrivateEndpointConnections.ListByPolicyName |
| microsoft.aadiam | microsoft.aadiam/privatelinkforazuread/privatelinkresources | 1 | resource-group | arm-envelope | armaad:PrivateLinkResources.ListByPrivateLinkPolicy |
| microsoft.advisor | microsoft.advisor/advisorscore | 0 | subscription | arm-envelope | armadvisor:Scores.List |
| microsoft.advisor | microsoft.advisor/assessments | 0 | subscription | item-write | armadvisor:Assessments.List |
| microsoft.advisor | microsoft.advisor/configurations | 0 | subscription | item-write | armadvisor:Configurations.ListByResourceGroup, armadvisor:Configurations.ListBySubscription |
| microsoft.advisor | microsoft.advisor/recommendations | 0 | extension | item-write | armadvisor:Recommendations.List, armadvisor:Recommendations.ListByTenant |
| microsoft.advisor | microsoft.advisor/resiliencyreviews | 0 | subscription | arm-envelope | armadvisor:ResiliencyReviews.List |
| microsoft.advisor | microsoft.advisor/triagerecommendations | 0 | subscription | arm-envelope | armadvisor:TriageRecommendations.List |
| microsoft.advisor | microsoft.advisor/triageresources | 0 | subscription | arm-envelope | armadvisor:TriageResources.List |
| microsoft.agfoodplatform | microsoft.agfoodplatform/farmbeats | 0 | subscription | item-write | armagrifood:FarmBeatsModels.ListByResourceGroup, armagrifood:FarmBeatsModels.ListBySubscription |
| microsoft.agfoodplatform | microsoft.agfoodplatform/farmbeats/extensions | 1 | resource-group | item-write | armagrifood:Extensions.ListByFarmBeats |
| microsoft.agfoodplatform | microsoft.agfoodplatform/farmbeats/privateendpointconnections | 1 | resource-group | item-write | armagrifood:PrivateEndpointConnections.ListByResource |
| microsoft.agfoodplatform | microsoft.agfoodplatform/farmbeats/privatelinkresources | 1 | resource-group | arm-envelope | armagrifood:PrivateLinkResources.ListByResource |
| microsoft.agricultureplatform | microsoft.agricultureplatform/agriservices | 0 | subscription | item-write | armagricultureplatform:AgriService.ListByResourceGroup, armagricultureplatform:AgriService.ListBySubscription |
| microsoft.alertsmanagement | microsoft.alertsmanagement/actionrules | 0 | subscription | item-write | armalertprocessingrules:Client.ListByResourceGroup, armalertprocessingrules:Client.ListBySubscription |
| microsoft.alertsmanagement | microsoft.alertsmanagement/prometheusrulegroups | 0 | subscription | item-write | armprometheusrulegroups:Client.ListByResourceGroup, armprometheusrulegroups:Client.ListBySubscription |
| microsoft.alertsmanagement | microsoft.alertsmanagement/tenantactivitylogalerts | 0 | tenant | item-write | armtenantactivitylogalerts:Client.ListByManagementGroup, armtenantactivitylogalerts:Client.ListByTenant |
| microsoft.apicenter | microsoft.apicenter/services/metadataschemas | 1 | resource-group | item-write | armapicenter:MetadataSchemas.List |
| microsoft.apicenter | microsoft.apicenter/services/workspaces | 1 | resource-group | item-write | armapicenter:Workspaces.List |
| microsoft.apicenter | microsoft.apicenter/services/workspaces/apis | 2 | resource-group | item-write | armapicenter:Apis.List |
| microsoft.apicenter | microsoft.apicenter/services/workspaces/apis/deployments | 3 | resource-group | item-write | armapicenter:Deployments.List |
| microsoft.apicenter | microsoft.apicenter/services/workspaces/apis/versions | 3 | resource-group | item-write | armapicenter:APIVersions.List |
| microsoft.apicenter | microsoft.apicenter/services/workspaces/apis/versions/definitions | 4 | resource-group | item-write | armapicenter:APIDefinitions.List |
| microsoft.apicenter | microsoft.apicenter/services/workspaces/environments | 2 | resource-group | item-write | armapicenter:Environments.List |
| microsoft.apimanagement | microsoft.apimanagement/deletedservices | 0 | subscription | item-write | armapimanagement:DeletedServices.ListBySubscription |
| microsoft.apimanagement | microsoft.apimanagement/gateways | 0 | subscription | item-write | armapimanagement:APIGateway.List, armapimanagement:APIGateway.ListByResourceGroup |
| microsoft.apimanagement | microsoft.apimanagement/gateways/configconnections | 1 | resource-group | item-write | armapimanagement:APIGatewayConfigConnection.ListByGateway |
| microsoft.apimanagement | microsoft.apimanagement/gateways/hostnamebindings | 1 | resource-group | item-write | armapimanagement:APIGatewayHostnameBinding.ListByGateway |
| microsoft.apimanagement | microsoft.apimanagement/service/apis | 1 | resource-group | item-write | armapimanagement:API.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/diagnostics | 2 | resource-group | item-write | armapimanagement:APIDiagnostic.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/issues | 2 | resource-group | item-write | armapimanagement:APIIssue.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/issues/attachments | 3 | resource-group | item-write | armapimanagement:APIIssueAttachment.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/issues/comments | 3 | resource-group | item-write | armapimanagement:APIIssueComment.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/operations | 2 | resource-group | item-write | armapimanagement:APIOperation.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/operations/policies | 3 | resource-group | item-write | armapimanagement:APIOperationPolicy.ListByOperation |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/operations/tags | 3 | resource-group | item-write | armapimanagement:Tag.ListByOperation |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/policies | 2 | resource-group | item-write | armapimanagement:APIPolicy.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/releases | 2 | resource-group | item-write | armapimanagement:APIRelease.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/resolvers | 2 | resource-group | item-write | armapimanagement:GraphQLAPIResolver.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/resolvers/policies | 3 | resource-group | item-write | armapimanagement:GraphQLAPIResolverPolicy.ListByResolver |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/schemas | 2 | resource-group | item-write | armapimanagement:APISchema.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/tagdescriptions | 2 | resource-group | item-write | armapimanagement:APITagDescription.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/tags | 2 | resource-group | item-write | armapimanagement:Tag.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/tools | 2 | resource-group | item-write | armapimanagement:APITool.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/apis/wikis | 2 | resource-group | item-write | armapimanagement:APIWikis.List |
| microsoft.apimanagement | microsoft.apimanagement/service/apiversionsets | 1 | resource-group | item-write | armapimanagement:APIVersionSet.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/authorizationproviders | 1 | resource-group | item-write | armapimanagement:AuthorizationProvider.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/authorizationproviders/authorizations | 2 | resource-group | item-write | armapimanagement:Authorization.ListByAuthorizationProvider |
| microsoft.apimanagement | microsoft.apimanagement/service/authorizationproviders/authorizations/accesspolicies | 3 | resource-group | item-write | armapimanagement:AuthorizationAccessPolicy.ListByAuthorization |
| microsoft.apimanagement | microsoft.apimanagement/service/authorizationservers | 1 | resource-group | item-write | armapimanagement:AuthorizationServer.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/backends | 1 | resource-group | item-write | armapimanagement:Backend.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/caches | 1 | resource-group | item-write | armapimanagement:Cache.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/certificates | 1 | resource-group | item-write | armapimanagement:Certificate.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/clientapplications | 1 | resource-group | item-write | armapimanagement:ClientApplication.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/clientapplications/productlinks | 2 | resource-group | item-write | armapimanagement:ClientApplicationProductLink.ListByClientApplications |
| microsoft.apimanagement | microsoft.apimanagement/service/contenttypes | 1 | resource-group | item-write | armapimanagement:ContentType.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/contenttypes/contentitems | 2 | resource-group | item-write | armapimanagement:ContentItem.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/diagnostics | 1 | resource-group | item-write | armapimanagement:Diagnostic.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/documentations | 1 | resource-group | item-write | armapimanagement:Documentation.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/gateways | 1 | resource-group | item-write | armapimanagement:Gateway.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/gateways/apis | 2 | resource-group | item-write | armapimanagement:GatewayAPI.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/gateways/certificateauthorities | 2 | resource-group | item-write | armapimanagement:GatewayCertificateAuthority.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/gateways/hostnameconfigurations | 2 | resource-group | item-write | armapimanagement:GatewayHostnameConfiguration.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/groups | 1 | resource-group | item-write | armapimanagement:Group.ListByService, armapimanagement:UserGroup.List |
| microsoft.apimanagement | microsoft.apimanagement/service/groups/users | 2 | resource-group | item-write | armapimanagement:GroupUser.List |
| microsoft.apimanagement | microsoft.apimanagement/service/identityproviders | 1 | resource-group | item-write | armapimanagement:IdentityProvider.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/issues | 1 | resource-group | arm-envelope | armapimanagement:Issue.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/loggers | 1 | resource-group | item-write | armapimanagement:Logger.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/namedvalues | 1 | resource-group | item-write | armapimanagement:NamedValue.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/notifications | 1 | resource-group | item-write | armapimanagement:Notification.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/notifications/recipientemails | 2 | resource-group | item-write | armapimanagement:NotificationRecipientEmail.ListByNotification |
| microsoft.apimanagement | microsoft.apimanagement/service/notifications/recipientusers | 2 | resource-group | item-write | armapimanagement:NotificationRecipientUser.ListByNotification |
| microsoft.apimanagement | microsoft.apimanagement/service/openidconnectproviders | 1 | resource-group | item-write | armapimanagement:OpenIDConnectProvider.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/policies | 1 | resource-group | item-write | armapimanagement:Policy.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/policyfragments | 1 | resource-group | item-write | armapimanagement:PolicyFragment.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/policyrestrictions | 1 | resource-group | item-write | armapimanagement:PolicyRestriction.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/portalconfigs | 1 | resource-group | item-write | armapimanagement:PortalConfig.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/portalrevisions | 1 | resource-group | item-write | armapimanagement:PortalRevision.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/portalsettings | 1 | resource-group | item-write | armapimanagement:PortalSettings.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/privateendpointconnections | 1 | resource-group | item-write | armapimanagement:PrivateEndpointConnection.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/privatelinkresources | 1 | resource-group | arm-envelope | armapimanagement:PrivateEndpointConnection.ListPrivateLinkResources |
| microsoft.apimanagement | microsoft.apimanagement/service/products | 1 | resource-group | item-write | armapimanagement:APIProduct.ListByApis, armapimanagement:Product.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/products/apilinks | 2 | resource-group | item-write | armapimanagement:ProductAPILink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/products/apis | 2 | resource-group | item-write | armapimanagement:ProductAPI.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/products/grouplinks | 2 | resource-group | item-write | armapimanagement:ProductGroupLink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/products/groups | 2 | resource-group | item-write | armapimanagement:ProductGroup.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/products/policies | 2 | resource-group | item-write | armapimanagement:ProductPolicy.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/products/tags | 2 | resource-group | item-write | armapimanagement:Tag.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/products/wikis | 2 | resource-group | item-write | armapimanagement:ProductWikis.List |
| microsoft.apimanagement | microsoft.apimanagement/service/schemas | 1 | resource-group | item-write | armapimanagement:GlobalSchema.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/settings | 1 | resource-group | arm-envelope | armapimanagement:TenantSettings.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/subscriptions | 1 | resource-group | item-write | armapimanagement:ProductSubscriptions.List, armapimanagement:Subscription.List |
| microsoft.apimanagement | microsoft.apimanagement/service/tags | 1 | resource-group | item-write | armapimanagement:Tag.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/tags/apilinks | 2 | resource-group | item-write | armapimanagement:TagAPILink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/tags/operationlinks | 2 | resource-group | item-write | armapimanagement:TagOperationLink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/tags/productlinks | 2 | resource-group | item-write | armapimanagement:TagProductLink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/templates | 1 | resource-group | item-write | armapimanagement:EmailTemplate.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/tenant | 1 | resource-group | item-write | armapimanagement:TenantAccess.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/users | 1 | resource-group | item-write | armapimanagement:User.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/users/subscriptions | 2 | resource-group | arm-envelope | armapimanagement:UserSubscription.List |
| microsoft.apimanagement | microsoft.apimanagement/service/workspacelinks | 1 | resource-group | arm-envelope | armapimanagement:WorkspaceLinks.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces | 1 | resource-group | item-write | armapimanagement:Workspace.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apis | 2 | resource-group | item-write | armapimanagement:WorkspaceAPI.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apis/diagnostics | 3 | resource-group | item-write | armapimanagement:WorkspaceAPIDiagnostic.ListByWorkspace |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apis/operations | 3 | resource-group | item-write | armapimanagement:WorkspaceAPIOperation.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apis/operations/policies | 4 | resource-group | item-write | armapimanagement:WorkspaceAPIOperationPolicy.ListByOperation |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apis/policies | 3 | resource-group | item-write | armapimanagement:WorkspaceAPIPolicy.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apis/releases | 3 | resource-group | item-write | armapimanagement:WorkspaceAPIRelease.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apis/schemas | 3 | resource-group | item-write | armapimanagement:WorkspaceAPISchema.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/apiversionsets | 2 | resource-group | item-write | armapimanagement:WorkspaceAPIVersionSet.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/backends | 2 | resource-group | item-write | armapimanagement:WorkspaceBackend.ListByWorkspace |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/certificates | 2 | resource-group | item-write | armapimanagement:WorkspaceCertificate.ListByWorkspace |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/diagnostics | 2 | resource-group | item-write | armapimanagement:WorkspaceDiagnostic.ListByWorkspace |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/groups | 2 | resource-group | item-write | armapimanagement:WorkspaceGroup.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/groups/users | 3 | resource-group | item-write | armapimanagement:WorkspaceGroupUser.List |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/loggers | 2 | resource-group | item-write | armapimanagement:WorkspaceLogger.ListByWorkspace |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/namedvalues | 2 | resource-group | item-write | armapimanagement:WorkspaceNamedValue.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/notifications | 2 | resource-group | item-write | armapimanagement:WorkspaceNotification.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/notifications/recipientemails | 3 | resource-group | item-write | armapimanagement:WorkspaceNotificationRecipientEmail.ListByNotification |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/notifications/recipientusers | 3 | resource-group | item-write | armapimanagement:WorkspaceNotificationRecipientUser.ListByNotification |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/policies | 2 | resource-group | item-write | armapimanagement:WorkspacePolicy.ListByAPI |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/policyfragments | 2 | resource-group | item-write | armapimanagement:WorkspacePolicyFragment.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/products | 2 | resource-group | item-write | armapimanagement:WorkspaceProduct.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/products/apilinks | 3 | resource-group | item-write | armapimanagement:WorkspaceProductAPILink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/products/grouplinks | 3 | resource-group | item-write | armapimanagement:WorkspaceProductGroupLink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/products/policies | 3 | resource-group | item-write | armapimanagement:WorkspaceProductPolicy.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/schemas | 2 | resource-group | item-write | armapimanagement:WorkspaceGlobalSchema.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/subscriptions | 2 | resource-group | item-write | armapimanagement:WorkspaceSubscription.List |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/tags | 2 | resource-group | item-write | armapimanagement:WorkspaceTag.ListByService |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/tags/apilinks | 3 | resource-group | item-write | armapimanagement:WorkspaceTagAPILink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/tags/operationlinks | 3 | resource-group | item-write | armapimanagement:WorkspaceTagOperationLink.ListByProduct |
| microsoft.apimanagement | microsoft.apimanagement/service/workspaces/tags/productlinks | 3 | resource-group | item-write | armapimanagement:WorkspaceTagProductLink.ListByProduct |
| microsoft.app | microsoft.app/connectedenvironments/certificates | 1 | resource-group | item-write | armappcontainers:ConnectedEnvironmentsCertificates.List |
| microsoft.app | microsoft.app/connectedenvironments/daprcomponents | 1 | resource-group | item-write | armappcontainers:ConnectedEnvironmentsDaprComponents.List |
| microsoft.app | microsoft.app/connectedenvironments/storages | 1 | resource-group | item-write | armappcontainers:ConnectedEnvironmentsStorages.List |
| microsoft.app | microsoft.app/containerapps/authconfigs | 1 | resource-group | item-write | armappcontainers:ContainerAppsAuthConfigs.ListByContainerApp |
| microsoft.app | microsoft.app/containerapps/detectorproperties/revisionsapi/revisions | 1 | resource-group | arm-envelope | armappcontainers:ContainerAppsDiagnostics.ListRevisions |
| microsoft.app | microsoft.app/containerapps/detectors | 1 | resource-group | arm-envelope | armappcontainers:ContainerAppsDiagnostics.ListDetectors |
| microsoft.app | microsoft.app/containerapps/functions | 1 | resource-group | arm-envelope | armappcontainers:ContainerAppsFunctions.List |
| microsoft.app | microsoft.app/containerapps/labelhistories | 1 | resource-group | item-write | armappcontainers:ContainerAppsLabelHistory.ListLabelHistory |
| microsoft.app | microsoft.app/containerapps/privateendpointconnections | 1 | resource-group | item-write | armappcontainers:ContainerAppPrivateEndpointConnections.List |
| microsoft.app | microsoft.app/containerapps/privatelinkresources | 1 | resource-group | arm-envelope | armappcontainers:ContainerAppPrivateLinkResources.List |
| microsoft.app | microsoft.app/containerapps/revisions | 1 | resource-group | arm-envelope | armappcontainers:ContainerAppsRevisions.ListRevisions |
| microsoft.app | microsoft.app/containerapps/revisions/functions | 2 | resource-group | arm-envelope | armappcontainers:ContainerAppsRevisionFunctions.List |
| microsoft.app | microsoft.app/containerapps/revisions/replicas | 2 | resource-group | arm-envelope | armappcontainers:ContainerAppsRevisionReplicas.ListReplicas |
| microsoft.app | microsoft.app/containerapps/sourcecontrols | 1 | resource-group | item-write | armappcontainers:ContainerAppsSourceControls.ListByContainerApp |
| microsoft.app | microsoft.app/jobs/detectors | 1 | resource-group | arm-envelope | armappcontainers:Jobs.ListDetectors |
| microsoft.app | microsoft.app/jobs/executions | 1 | resource-group | arm-envelope | armappcontainers:JobsExecutions.List |
| microsoft.app | microsoft.app/logicapps/workflows | 1 | resource-group | arm-envelope | armappcontainers:LogicApps.ListWorkflows |
| microsoft.app | microsoft.app/managedenvironments/certificates | 1 | resource-group | item-write | armappcontainers:Certificates.List |
| microsoft.app | microsoft.app/managedenvironments/daprcomponents | 1 | resource-group | item-write | armappcontainers:DaprComponents.List |
| microsoft.app | microsoft.app/managedenvironments/daprcomponents/resiliencypolicies | 2 | resource-group | item-write | armappcontainers:DaprComponentResiliencyPolicies.List |
| microsoft.app | microsoft.app/managedenvironments/detectors | 1 | resource-group | arm-envelope | armappcontainers:ManagedEnvironmentDiagnostics.ListDetectors |
| microsoft.app | microsoft.app/managedenvironments/dotnetcomponents | 1 | resource-group | item-write | armappcontainers:DotNetComponents.List |
| microsoft.app | microsoft.app/managedenvironments/httprouteconfigs | 1 | resource-group | item-write | armappcontainers:HTTPRouteConfig.List |
| microsoft.app | microsoft.app/managedenvironments/javacomponents | 1 | resource-group | item-write | armappcontainers:JavaComponents.List |
| microsoft.app | microsoft.app/managedenvironments/maintenanceconfigurations | 1 | resource-group | item-write | armappcontainers:MaintenanceConfigurations.List |
| microsoft.app | microsoft.app/managedenvironments/managedcertificates | 1 | resource-group | item-write | armappcontainers:ManagedCertificates.List |
| microsoft.app | microsoft.app/managedenvironments/privateendpointconnections | 1 | resource-group | item-write | armappcontainers:ManagedEnvironmentPrivateEndpointConnections.List |
| microsoft.app | microsoft.app/managedenvironments/privatelinkresources | 1 | resource-group | arm-envelope | armappcontainers:ManagedEnvironmentPrivateLinkResources.List |
| microsoft.app | microsoft.app/managedenvironments/storages | 1 | resource-group | item-write | armappcontainers:ManagedEnvironmentsStorages.List |
| microsoft.app | microsoft.app/sandboxgroups | 0 | subscription | item-write | armappcontainers:SandboxGroups.ListByResourceGroup, armappcontainers:SandboxGroups.ListBySubscription |
| microsoft.app | microsoft.app/sandboxgroups/vnetconnections | 1 | resource-group | item-write | armappcontainers:VnetConnections.ListBySandboxGroup |
| microsoft.appcomplianceautomation | microsoft.appcomplianceautomation/reports | 0 | tenant | item-write | armappcomplianceautomation:Report.List |
| microsoft.appcomplianceautomation | microsoft.appcomplianceautomation/reports/evidences | 1 | tenant | item-write | armappcomplianceautomation:Evidence.ListByReport |
| microsoft.appcomplianceautomation | microsoft.appcomplianceautomation/reports/scopingconfigurations | 1 | tenant | item-write | armappcomplianceautomation:ScopingConfiguration.List |
| microsoft.appcomplianceautomation | microsoft.appcomplianceautomation/reports/snapshots | 1 | tenant | arm-envelope | armappcomplianceautomation:Snapshot.List |
| microsoft.appcomplianceautomation | microsoft.appcomplianceautomation/reports/webhooks | 1 | tenant | item-write | armappcomplianceautomation:Webhook.List |
| microsoft.appconfiguration | microsoft.appconfiguration/configurationstores/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armappconfiguration:NetworkSecurityPerimeterConfigurations.ListByConfigurationStore |
| microsoft.appconfiguration | microsoft.appconfiguration/configurationstores/privateendpointconnections | 1 | resource-group | item-write | armappconfiguration:PrivateEndpointConnections.ListByConfigurationStore |
| microsoft.appconfiguration | microsoft.appconfiguration/configurationstores/privatelinkresources | 1 | resource-group | arm-envelope | armappconfiguration:PrivateLinkResources.ListByConfigurationStore |
| microsoft.appconfiguration | microsoft.appconfiguration/configurationstores/replicas | 1 | resource-group | item-write | armappconfiguration:Replicas.ListByConfigurationStore |
| microsoft.appconfiguration | microsoft.appconfiguration/deletedconfigurationstores | 0 | subscription | arm-envelope | armappconfiguration:ConfigurationStores.ListDeleted |
| microsoft.applink | microsoft.applink/applinks | 0 | subscription | item-write | armappnetwork:AppLinks.ListByResourceGroup, armappnetwork:AppLinks.ListBySubscription |
| microsoft.applink | microsoft.applink/applinks/applinkmembers | 1 | resource-group | item-write | armappnetwork:AppLinkMembers.ListByAppLink |
| microsoft.attestation | microsoft.attestation/attestationproviders/privateendpointconnections | 1 | resource-group | item-write | armattestation:PrivateEndpointConnections.List |
| microsoft.authorization | microsoft.authorization/accessreviewhistorydefinitions | 0 | extension | item-write | armauthorization:AccessReviewHistoryDefinitions.List, armauthorization:ScopeAccessReviewHistoryDefinitions.List |
| microsoft.authorization | microsoft.authorization/accessreviewscheduledefinitions | 0 | extension | item-write | armauthorization:AccessReviewScheduleDefinitions.List, armauthorization:AccessReviewScheduleDefinitionsAssignedForMyApproval.List, armauthorization:ScopeAccessReviewScheduleDefinitions.List |
| microsoft.authorization | microsoft.authorization/accessreviewscheduledefinitions/instances | 1 | extension | item-write | armauthorization:AccessReviewInstances.List, armauthorization:AccessReviewInstancesAssignedForMyApproval.List, armauthorization:ScopeAccessReviewInstances.List |
| microsoft.authorization | microsoft.authorization/accessreviewscheduledefinitions/instances/decisions | 2 | extension | item-write | armauthorization:AccessReviewInstanceDecisions.List, armauthorization:AccessReviewInstanceMyDecisions.List, armauthorization:ScopeAccessReviewInstanceDecisions.List |
| microsoft.authorization | microsoft.authorization/denyassignments | 0 | extension | item-write | armauthorization:DenyAssignments.List, armauthorization:DenyAssignments.ListForResource, armauthorization:DenyAssignments.ListForResourceGroup, armauthorization:DenyAssignments.ListForScope |
| microsoft.authorization | microsoft.authorization/locks | 0 | extension | item-write | armlocks:ManagementLocks.ListAtResourceGroupLevel, armlocks:ManagementLocks.ListAtResourceLevel, armlocks:ManagementLocks.ListAtSubscriptionLevel, armlocks:ManagementLocks.ListByScope |
| microsoft.authorization | microsoft.authorization/policydefinitions/versions | 1 | tenant | item-write | armpolicy:DefinitionVersions.List, armpolicy:DefinitionVersions.ListBuiltIn, armpolicy:DefinitionVersions.ListByManagementGroup |
| microsoft.authorization | microsoft.authorization/policyenrollments | 0 | extension | item-write | armpolicy:Enrollments.List, armpolicy:Enrollments.ListForManagementGroup, armpolicy:Enrollments.ListForResource, armpolicy:Enrollments.ListForResourceGroup |
| microsoft.authorization | microsoft.authorization/policyexemptions | 0 | extension | item-write | armpolicy:Exemptions.List, armpolicy:Exemptions.ListForManagementGroup, armpolicy:Exemptions.ListForResource, armpolicy:Exemptions.ListForResourceGroup |
| microsoft.authorization | microsoft.authorization/policysetdefinitions/versions | 1 | tenant | item-write | armpolicy:SetDefinitionVersions.List, armpolicy:SetDefinitionVersions.ListBuiltIn, armpolicy:SetDefinitionVersions.ListByManagementGroup |
| microsoft.authorization | microsoft.authorization/roleassignmentschedulerequests | 0 | extension | item-write | armauthorization:RoleAssignmentScheduleRequests.ListForScope |
| microsoft.authorization | microsoft.authorization/roleeligibilityschedulerequests | 0 | extension | item-write | armauthorization:RoleEligibilityScheduleRequests.ListForScope |
| microsoft.authorization | microsoft.authorization/rolemanagementalertconfigurations | 0 | extension | item-write | armauthorization:AlertConfigurations.ListForScope |
| microsoft.authorization | microsoft.authorization/rolemanagementalerts | 0 | extension | item-write | armauthorization:Alerts.ListForScope |
| microsoft.authorization | microsoft.authorization/rolemanagementalerts/alertincidents | 1 | extension | arm-envelope | armauthorization:AlertIncidents.ListForScope |
| microsoft.authorization | microsoft.authorization/rolemanagementpolicies | 0 | extension | item-write | armauthorization:RoleManagementPolicies.ListForScope |
| microsoft.authorization | microsoft.authorization/rolemanagementpolicyassignments | 0 | extension | item-write | armauthorization:RoleManagementPolicyAssignments.ListForScope |
| microsoft.authorization | microsoft.authorization/variables | 0 | management-group | item-write | armpolicy:Variables.List, armpolicy:Variables.ListForManagementGroup |
| microsoft.authorization | microsoft.authorization/variables/values | 1 | management-group | item-write | armpolicy:VariableValues.List, armpolicy:VariableValues.ListForManagementGroup |
| microsoft.automanage | microsoft.automanage/bestpractices/versions | 1 | tenant | arm-envelope | armautomanage:BestPracticesVersions.ListByTenant |
| microsoft.automanage | microsoft.automanage/configurationprofileassignments/reports | 1 | resource-group | arm-envelope | armautomanage:HCIReports.ListByConfigurationProfileAssignments, armautomanage:HCRPReports.ListByConfigurationProfileAssignments, armautomanage:Reports.ListByConfigurationProfileAssignments |
| microsoft.automanage | microsoft.automanage/configurationprofiles/versions | 1 | resource-group | item-write | armautomanage:ConfigurationProfilesVersions.ListChildResources |
| microsoft.automation | microsoft.automation/automationaccounts/certificates | 1 | resource-group | item-write | armautomation:Certificate.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/configurations | 1 | resource-group | item-write | armautomation:DscConfiguration.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/connections | 1 | resource-group | item-write | armautomation:Connection.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/connectiontypes | 1 | resource-group | item-write | armautomation:ConnectionType.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/credentials | 1 | resource-group | item-write | armautomation:Credential.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/hybridrunbookworkergroups | 1 | resource-group | item-write | armautomation:HybridRunbookWorkerGroup.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/hybridrunbookworkergroups/hybridrunbookworkers | 2 | resource-group | item-write | armautomation:HybridRunbookWorkers.ListByHybridRunbookWorkerGroup |
| microsoft.automation | microsoft.automation/automationaccounts/jobs | 1 | resource-group | item-write | armautomation:Job.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/jobschedules | 1 | resource-group | item-write | armautomation:JobSchedule.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/modules | 1 | resource-group | item-write | armautomation:Module.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/nodeconfigurations | 1 | resource-group | item-write | armautomation:DscNodeConfiguration.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/nodes | 1 | resource-group | item-write | armautomation:DscNode.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/nodes/reports | 2 | resource-group | arm-envelope | armautomation:NodeReports.ListByNode |
| microsoft.automation | microsoft.automation/automationaccounts/privateendpointconnections | 1 | resource-group | item-write | armautomation:PrivateEndpointConnections.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/python2packages | 1 | resource-group | item-write | armautomation:Python2Package.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/python3packages | 1 | resource-group | item-write | armautomation:Python3Package.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/runbooks | 1 | resource-group | item-write | armautomation:Runbook.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/runtimeenvironments | 1 | resource-group | item-write | armautomation:RuntimeEnvironments.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/runtimeenvironments/packages | 2 | resource-group | item-write | armautomation:Package.ListByRuntimeEnvironment |
| microsoft.automation | microsoft.automation/automationaccounts/schedules | 1 | resource-group | item-write | armautomation:Schedule.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/softwareupdateconfigurations | 1 | resource-group | item-write | armautomation:SoftwareUpdateConfigurations.List |
| microsoft.automation | microsoft.automation/automationaccounts/sourcecontrols | 1 | resource-group | item-write | armautomation:SourceControl.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/sourcecontrols/sourcecontrolsyncjobs | 2 | resource-group | item-write | armautomation:SourceControlSyncJob.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/variables | 1 | resource-group | item-write | armautomation:Variable.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/watchers | 1 | resource-group | item-write | armautomation:Watcher.ListByAutomationAccount |
| microsoft.automation | microsoft.automation/automationaccounts/webhooks | 1 | resource-group | item-write | armautomation:Webhook.ListByAutomationAccount |
| microsoft.avs | microsoft.avs/privateclouds/addons | 1 | resource-group | item-write | armavs:Addons.List |
| microsoft.avs | microsoft.avs/privateclouds/authorizations | 1 | resource-group | item-write | armavs:Authorizations.List |
| microsoft.avs | microsoft.avs/privateclouds/cloudlinks | 1 | resource-group | item-write | armavs:CloudLinks.List |
| microsoft.avs | microsoft.avs/privateclouds/clusters | 1 | resource-group | item-write | armavs:Clusters.List |
| microsoft.avs | microsoft.avs/privateclouds/clusters/datastores | 2 | resource-group | item-write | armavs:Datastores.List |
| microsoft.avs | microsoft.avs/privateclouds/clusters/hosts | 2 | resource-group | item-write | armavs:Hosts.List |
| microsoft.avs | microsoft.avs/privateclouds/clusters/placementpolicies | 2 | resource-group | item-write | armavs:PlacementPolicies.List |
| microsoft.avs | microsoft.avs/privateclouds/clusters/virtualmachines | 2 | resource-group | arm-envelope | armavs:VirtualMachines.List |
| microsoft.avs | microsoft.avs/privateclouds/globalreachconnections | 1 | resource-group | item-write | armavs:GlobalReachConnections.List |
| microsoft.avs | microsoft.avs/privateclouds/hcxenterprisesites | 1 | resource-group | item-write | armavs:HcxEnterpriseSites.List |
| microsoft.avs | microsoft.avs/privateclouds/iscsipaths | 1 | resource-group | item-write | armavs:IscsiPaths.ListByPrivateCloud |
| microsoft.avs | microsoft.avs/privateclouds/licenses | 1 | resource-group | item-write | armavs:Licenses.List |
| microsoft.avs | microsoft.avs/privateclouds/maintenances | 1 | resource-group | arm-envelope | armavs:Maintenances.List |
| microsoft.avs | microsoft.avs/privateclouds/provisionednetworks | 1 | resource-group | arm-envelope | armavs:ProvisionedNetworks.List |
| microsoft.avs | microsoft.avs/privateclouds/purestoragepolicies | 1 | resource-group | item-write | armavs:PureStoragePolicies.List |
| microsoft.avs | microsoft.avs/privateclouds/scriptexecutions | 1 | resource-group | item-write | armavs:ScriptExecutions.List |
| microsoft.avs | microsoft.avs/privateclouds/scriptpackages | 1 | resource-group | arm-envelope | armavs:ScriptPackages.List |
| microsoft.avs | microsoft.avs/privateclouds/scriptpackages/scriptcmdlets | 2 | resource-group | arm-envelope | armavs:ScriptCmdlets.List |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks | 1 | resource-group | arm-envelope | armavs:WorkloadNetworks.List |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/dhcpconfigurations | 2 | resource-group | item-write | armavs:WorkloadNetworks.ListDhcp |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/dnsservices | 2 | resource-group | item-write | armavs:WorkloadNetworks.ListDNSServices |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/dnszones | 2 | resource-group | item-write | armavs:WorkloadNetworks.ListDNSZones |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/gateways | 2 | resource-group | arm-envelope | armavs:WorkloadNetworks.ListGateways |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/portmirroringprofiles | 2 | resource-group | item-write | armavs:WorkloadNetworks.ListPortMirroring |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/publicips | 2 | resource-group | item-write | armavs:WorkloadNetworks.ListPublicIPs |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/segments | 2 | resource-group | item-write | armavs:WorkloadNetworks.ListSegments |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/virtualmachines | 2 | resource-group | arm-envelope | armavs:WorkloadNetworks.ListVirtualMachines |
| microsoft.avs | microsoft.avs/privateclouds/workloadnetworks/vmgroups | 2 | resource-group | item-write | armavs:WorkloadNetworks.ListVMGroups |
| microsoft.azurearcdata | microsoft.azurearcdata/datacontrollers/activedirectoryconnectors | 1 | resource-group | item-write | armazurearcdata:ActiveDirectoryConnectors.List |
| microsoft.azuredata | microsoft.azuredata/sqlserverregistrations | 0 | subscription | item-write | armazuredata:SQLServerRegistrations.List, armazuredata:SQLServerRegistrations.ListByResourceGroup |
| microsoft.azuredata | microsoft.azuredata/sqlserverregistrations/sqlservers | 1 | resource-group | item-write | armazuredata:SQLServers.ListByResourceGroup |
| microsoft.azureplaywrightservice | microsoft.azureplaywrightservice/accounts/quotas | 1 | resource-group | arm-envelope | armplaywrighttesting:AccountQuotas.ListByAccount |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/drills | 0 | tenant | item-write | armresiliencemanagement:Drills.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/drills/drillresources | 1 | tenant | arm-envelope | armresiliencemanagement:DrillResources.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/drills/drillruns | 1 | tenant | arm-envelope | armresiliencemanagement:DrillRuns.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/drills/drillruns/drillrunresources | 2 | tenant | arm-envelope | armresiliencemanagement:DrillRunResources.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/goalassignments | 0 | tenant | item-write | armresiliencemanagement:GoalAssignments.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/goalassignments/goalresources | 1 | tenant | arm-envelope | armresiliencemanagement:GoalResources.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/goaltemplates | 0 | tenant | item-write | armresiliencemanagement:GoalTemplates.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/recoveryplans | 0 | tenant | item-write | armresiliencemanagement:RecoveryPlans.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/recoveryplans/recoveryjobs | 1 | tenant | arm-envelope | armresiliencemanagement:RecoveryJobs.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/recoveryplans/recoveryjobs/recoveryjobresources | 2 | tenant | arm-envelope | armresiliencemanagement:RecoveryJobResources.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/recoveryplans/recoveryresources | 1 | tenant | arm-envelope | armresiliencemanagement:RecoveryResources.List |
| microsoft.azureresiliencemanagement | microsoft.azureresiliencemanagement/usageplans/enrollments | 1 | resource-group | item-write | armresiliencemanagement:Enrollments.List |
| microsoft.azuresphere | microsoft.azuresphere/catalogs/certificates | 1 | resource-group | arm-envelope | armsphere:Certificates.ListByCatalog |
| microsoft.azuresphere | microsoft.azuresphere/catalogs/images | 1 | resource-group | item-write | armsphere:Images.ListByCatalog |
| microsoft.azuresphere | microsoft.azuresphere/catalogs/products | 1 | resource-group | item-write | armsphere:Products.ListByCatalog |
| microsoft.azuresphere | microsoft.azuresphere/catalogs/products/devicegroups | 2 | resource-group | item-write | armsphere:DeviceGroups.ListByProduct |
| microsoft.azuresphere | microsoft.azuresphere/catalogs/products/devicegroups/deployments | 3 | resource-group | item-write | armsphere:Deployments.ListByDeviceGroup |
| microsoft.azuresphere | microsoft.azuresphere/catalogs/products/devicegroups/devices | 3 | resource-group | item-write | armsphere:Devices.ListByDeviceGroup |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/arcsettings | 1 | resource-group | item-write | armazurestackhci:ArcSettings.ListByCluster |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/arcsettings/extensions | 2 | resource-group | item-write | armazurestackhci:Extensions.ListByArcSetting |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/deploymentsettings | 1 | resource-group | item-write | armazurestackhci:DeploymentSettings.ListByClusters |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/publishers/offers | 2 | resource-group | arm-envelope | armazurestackhci:Offers.ListByPublisher |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/publishers/offers/skus | 3 | resource-group | arm-envelope | armazurestackhci:SKUs.ListByOffer |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/securitysettings | 1 | resource-group | item-write | armazurestackhci:SecuritySettings.ListByClusters |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/updates | 1 | resource-group | item-write | armazurestackhci:Updates.List |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/updates/updateruns | 2 | resource-group | item-write | armazurestackhci:UpdateRuns.List |
| microsoft.azurestackhci | microsoft.azurestackhci/clusters/updatesummaries | 1 | resource-group | item-write | armazurestackhci:UpdateSummaries.List |
| microsoft.azurestackhci | microsoft.azurestackhci/edgedevices | 0 | extension | item-write | armazurestackhci:EdgeDevices.List |
| microsoft.azurestackhci | microsoft.azurestackhci/edgedevices/jobs | 1 | extension | item-write | armazurestackhci:EdgeDeviceJobs.ListByEdgeDevice |
| microsoft.azurestackhci | microsoft.azurestackhci/networksecuritygroups/securityrules | 1 | resource-group | item-write | armazurestackhcivm:SecurityRules.ListByNetworkSecurityGroup |
| microsoft.azurestackhci | microsoft.azurestackhci/virtualmachineinstances | 0 | extension | item-write | armazurestackhcivm:VirtualMachineInstances.List |
| microsoft.azurestackhci | microsoft.azurestackhci/virtualmachineinstances/guestagents | 1 | extension | item-write | armazurestackhcivm:GuestAgents.ListByVirtualMachineInstance |
| microsoft.azurestackhci | microsoft.azurestackhci/virtualmachineinstances/hybrididentitymetadata | 1 | extension | arm-envelope | armazurestackhcivm:HybridIdentityMetadata.ListByVirtualMachineInstance |
| microsoft.baremetalinfrastructure | microsoft.baremetalinfrastructure/baremetalstorageinstances | 0 | subscription | item-write | armbaremetalinfrastructure:AzureBareMetalStorageInstances.ListByResourceGroup, armbaremetalinfrastructure:AzureBareMetalStorageInstances.ListBySubscription |
| microsoft.batch | microsoft.batch/batchaccounts/applications | 1 | resource-group | item-write | armbatch:Application.List |
| microsoft.batch | microsoft.batch/batchaccounts/applications/versions | 2 | resource-group | item-write | armbatch:ApplicationPackage.List |
| microsoft.batch | microsoft.batch/batchaccounts/detectors | 1 | resource-group | arm-envelope | armbatch:Account.ListDetectors |
| microsoft.batch | microsoft.batch/batchaccounts/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armbatch:NetworkSecurityPerimeter.ListConfigurations |
| microsoft.batch | microsoft.batch/batchaccounts/pools | 1 | resource-group | item-write | armbatch:Pool.ListByBatchAccount |
| microsoft.batch | microsoft.batch/batchaccounts/privateendpointconnections | 1 | resource-group | item-write | armbatch:PrivateEndpointConnection.ListByBatchAccount |
| microsoft.batch | microsoft.batch/batchaccounts/privatelinkresources | 1 | resource-group | arm-envelope | armbatch:PrivateLinkResource.ListByBatchAccount |
| microsoft.billing | microsoft.billing/billingaccounts | 0 | tenant | item-write | armbilling:Accounts.List |
| microsoft.billing | microsoft.billing/billingaccounts/agreements | 1 | tenant | arm-envelope | armbilling:Agreements.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/associatedtenants | 1 | tenant | item-write | armbilling:AssociatedTenants.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles | 1 | tenant | item-write | armbilling:Profiles.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/billingroleassignments | 2 | tenant | item-write | armbilling:RoleAssignments.ListByBillingProfile |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/billingroledefinitions | 2 | tenant | arm-envelope | armbilling:RoleDefinition.ListByBillingProfile |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/billingsubscriptions | 2 | tenant | arm-envelope | armbilling:Subscriptions.ListByBillingProfile |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/customers | 2 | tenant | arm-envelope | armbilling:Customers.ListByBillingProfile |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/customers/billingroleassignments | 3 | tenant | item-write | armbilling:RoleAssignments.ListByCustomer |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/customers/billingroledefinitions | 3 | tenant | arm-envelope | armbilling:RoleDefinition.ListByCustomer |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/customers/transfers | 3 | tenant | item-write | armbilling:PartnerTransfers.List |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/invoicesections | 2 | tenant | item-write | armbilling:InvoiceSections.ListByBillingProfile |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/invoicesections/billingroleassignments | 3 | tenant | item-write | armbilling:RoleAssignments.ListByInvoiceSection |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/invoicesections/billingroledefinitions | 3 | tenant | arm-envelope | armbilling:RoleDefinition.ListByInvoiceSection |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/invoicesections/transfers | 3 | tenant | item-write | armbilling:Transfers.List |
| microsoft.billing | microsoft.billing/billingaccounts/billingprofiles/paymentmethodlinks | 2 | tenant | arm-envelope | armbilling:PaymentMethods.ListByBillingProfile |
| microsoft.billing | microsoft.billing/billingaccounts/billingroleassignments | 1 | tenant | item-write | armbilling:RoleAssignments.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/billingroledefinitions | 1 | tenant | arm-envelope | armbilling:RoleDefinition.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/billingsubscriptionaliases | 1 | tenant | item-write | armbilling:SubscriptionsAliases.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/billingsubscriptions | 1 | tenant | item-write | armbilling:Subscriptions.ListByBillingAccount, armbilling:Subscriptions.ListByCustomer, armbilling:Subscriptions.ListByCustomerAtBillingAccount, armbilling:Subscriptions.ListByEnrollmentAccount, armbilling:Subscriptions.ListByInvoiceSection |
| microsoft.billing | microsoft.billing/billingaccounts/billingsubscriptions/invoices | 2 | tenant | arm-envelope | armbilling:Invoices.ListByBillingSubscription |
| microsoft.billing | microsoft.billing/billingaccounts/customers | 1 | tenant | arm-envelope | armbilling:Customers.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/departments | 1 | tenant | arm-envelope | armbilling:Departments.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/departments/billingroleassignments | 2 | tenant | item-write | armbilling:RoleAssignments.ListByDepartment |
| microsoft.billing | microsoft.billing/billingaccounts/departments/billingroledefinitions | 2 | tenant | arm-envelope | armbilling:RoleDefinition.ListByDepartment |
| microsoft.billing | microsoft.billing/billingaccounts/departments/enrollmentaccounts | 2 | tenant | arm-envelope | armbilling:EnrollmentAccounts.ListByDepartment |
| microsoft.billing | microsoft.billing/billingaccounts/enrollmentaccounts | 1 | tenant | arm-envelope | armbilling:EnrollmentAccounts.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/enrollmentaccounts/billingroleassignments | 2 | tenant | item-write | armbilling:RoleAssignments.ListByEnrollmentAccount |
| microsoft.billing | microsoft.billing/billingaccounts/enrollmentaccounts/billingroledefinitions | 2 | tenant | arm-envelope | armbilling:RoleDefinition.ListByEnrollmentAccount |
| microsoft.billing | microsoft.billing/billingaccounts/invoices | 1 | tenant | arm-envelope | armbilling:Invoices.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/paymentmethods | 1 | tenant | arm-envelope | armbilling:PaymentMethods.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/products | 1 | tenant | item-write | armbilling:Products.ListByBillingAccount, armbilling:Products.ListByBillingProfile, armbilling:Products.ListByCustomer, armbilling:Products.ListByInvoiceSection |
| microsoft.billing | microsoft.billing/billingaccounts/reservationorders | 1 | tenant | arm-envelope | armbilling:ReservationOrders.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/reservationorders/reservations | 2 | tenant | item-write | armbilling:Reservations.ListByBillingAccount, armbilling:Reservations.ListByBillingProfile, armbilling:Reservations.ListByReservationOrder |
| microsoft.billing | microsoft.billing/billingaccounts/savingsplanorders | 1 | tenant | arm-envelope | armbilling:SavingsPlanOrders.ListByBillingAccount |
| microsoft.billing | microsoft.billing/billingaccounts/savingsplanorders/savingsplans | 2 | tenant | item-write | armbilling:SavingsPlans.ListByBillingAccount, armbilling:SavingsPlans.ListBySavingsPlanOrder |
| microsoft.billing | microsoft.billing/billingrequests | 0 | tenant | item-write | armbilling:Requests.ListByBillingAccount, armbilling:Requests.ListByBillingProfile, armbilling:Requests.ListByCustomer, armbilling:Requests.ListByInvoiceSection, armbilling:Requests.ListByUser |
| microsoft.billing | microsoft.billing/paymentmethods | 0 | tenant | item-write | armbilling:PaymentMethods.ListByUser |
| microsoft.billingbenefits | microsoft.billingbenefits/conditionalcredits | 0 | subscription | item-write | armbillingbenefits:ConditionalCredits.ListByResourceGroup, armbillingbenefits:ConditionalCredits.ListBySubscription, armbillingbenefits:ConditionalCredits.ScopeList |
| microsoft.billingbenefits | microsoft.billingbenefits/conditionalcredits/contributors | 1 | resource-group | arm-envelope | armbillingbenefits:ConditionalCreditContributors.ListFromPrimary |
| microsoft.billingbenefits | microsoft.billingbenefits/credits | 0 | subscription | item-write | armbillingbenefits:Credits.ListApplicable, armbillingbenefits:Credits.ListByResourceGroup, armbillingbenefits:Credits.ListBySubscription |
| microsoft.billingbenefits | microsoft.billingbenefits/credits/sources | 1 | resource-group | item-write | armbillingbenefits:Sources.ListByCredit |
| microsoft.billingbenefits | microsoft.billingbenefits/discounts | 0 | subscription | item-write | armbillingbenefits:Discounts.ResourceGroupList, armbillingbenefits:Discounts.ScopeList, armbillingbenefits:Discounts.SubscriptionList |
| microsoft.billingbenefits | microsoft.billingbenefits/freeservices | 0 | subscription | item-write | armbillingbenefits:FreeServices.ListByResourceGroup, armbillingbenefits:FreeServices.ListBySubscription |
| microsoft.billingbenefits | microsoft.billingbenefits/maccs | 0 | subscription | item-write | armbillingbenefits:Maccs.ListByResourceGroup, armbillingbenefits:Maccs.ListBySubscription |
| microsoft.billingbenefits | microsoft.billingbenefits/maccs/contributors | 1 | resource-group | arm-envelope | armbillingbenefits:Contributors.ListFromPrimary |
| microsoft.billingbenefits | microsoft.billingbenefits/savingsplanorders/savingsplans | 1 | tenant | item-write | armbillingbenefits:SavingsPlan.List, armbillingbenefits:SavingsPlan.ListAll |
| microsoft.billingtrust | microsoft.billingtrust/assessments | 0 | extension | item-write | armbillingtrust:Assessments.List |
| microsoft.billingtrust | microsoft.billingtrust/assessments/rules | 1 | extension | item-write | armbillingtrust:Rules.List |
| microsoft.blockchain | microsoft.blockchain/blockchainmembers | 0 | subscription | item-write | armblockchain:Members.List, armblockchain:Members.ListAll |
| microsoft.blockchain | microsoft.blockchain/blockchainmembers/transactionnodes | 1 | resource-group | item-write | armblockchain:TransactionNodes.List |
| microsoft.blueprint | microsoft.blueprint/blueprintassignments/assignmentoperations | 1 | extension | arm-envelope | armblueprint:AssignmentOperations.List |
| microsoft.blueprint | microsoft.blueprint/blueprints/artifacts | 1 | extension | item-write | armblueprint:Artifacts.List |
| microsoft.blueprint | microsoft.blueprint/blueprints/versions | 1 | extension | item-write | armblueprint:PublishedBlueprints.List |
| microsoft.blueprint | microsoft.blueprint/blueprints/versions/artifacts | 2 | extension | arm-envelope | armblueprint:PublishedArtifacts.List |
| microsoft.botservice | microsoft.botservice/botservices/channels | 1 | resource-group | item-write | armbotservice:Channels.ListByResourceGroup |
| microsoft.botservice | microsoft.botservice/botservices/connections | 1 | resource-group | item-write | armbotservice:BotConnection.ListByBotService |
| microsoft.botservice | microsoft.botservice/botservices/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armbotservice:NetworkSecurityPerimeterConfigurations.List |
| microsoft.botservice | microsoft.botservice/botservices/privateendpointconnections | 1 | resource-group | item-write | armbotservice:PrivateEndpointConnections.List |
| microsoft.cache | microsoft.cache/redis/accesspolicies | 1 | resource-group | item-write | armredis:AccessPolicy.List |
| microsoft.cache | microsoft.cache/redis/accesspolicyassignments | 1 | resource-group | item-write | armredis:AccessPolicyAssignment.List |
| microsoft.cache | microsoft.cache/redis/firewallrules | 1 | resource-group | item-write | armredis:FirewallRules.List |
| microsoft.cache | microsoft.cache/redis/linkedservers | 1 | resource-group | item-write | armredis:LinkedServer.List |
| microsoft.cache | microsoft.cache/redis/patchschedules | 1 | resource-group | item-write | armredis:PatchSchedules.ListByRedisResource |
| microsoft.cache | microsoft.cache/redis/privateendpointconnections | 1 | resource-group | item-write | armredis:PrivateEndpointConnections.List |
| microsoft.cache | microsoft.cache/redisenterprise/databases | 1 | resource-group | item-write | armredisenterprise:Databases.ListByCluster |
| microsoft.cache | microsoft.cache/redisenterprise/databases/accesspolicyassignments | 2 | resource-group | item-write | armredisenterprise:AccessPolicyAssignment.List |
| microsoft.cache | microsoft.cache/redisenterprise/migrations | 1 | resource-group | item-write | armredisenterprise:Migrations.List |
| microsoft.cache | microsoft.cache/redisenterprise/privateendpointconnections | 1 | resource-group | item-write | armredisenterprise:PrivateEndpointConnections.List |
| microsoft.capacity | microsoft.capacity/reservationorders | 0 | tenant | item-write | armreservations:ReservationOrder.List |
| microsoft.capacity | microsoft.capacity/reservationorders/reservations | 1 | tenant | item-write | armreservations:Reservation.List, armreservations:Reservation.ListAll, armreservations:Reservation.ListRevisions |
| microsoft.capacity | microsoft.capacity/resourceproviders/servicelimits | 1 | subscription | item-write | armreservations:Quota.List |
| microsoft.cdn | microsoft.cdn/edgeactions | 0 | subscription | item-write | armedgeactions:Client.ListByResourceGroup, armedgeactions:Client.ListBySubscription |
| microsoft.cdn | microsoft.cdn/edgeactions/executionfilters | 1 | resource-group | item-write | armedgeactions:EdgeActionExecutionFilters.ListByEdgeAction |
| microsoft.cdn | microsoft.cdn/edgeactions/versions | 1 | resource-group | item-write | armedgeactions:EdgeActionVersions.ListByEdgeAction |
| microsoft.cdn | microsoft.cdn/profiles/afdendpoints | 1 | resource-group | item-write | armcdn:AFDEndpoints.ListByProfile |
| microsoft.cdn | microsoft.cdn/profiles/afdendpoints/routes | 2 | resource-group | item-write | armcdn:Routes.ListByEndpoint |
| microsoft.cdn | microsoft.cdn/profiles/customdomains | 1 | resource-group | item-write | armcdn:AFDCustomDomains.ListByProfile |
| microsoft.cdn | microsoft.cdn/profiles/endpoints | 1 | resource-group | item-write | armcdn:Endpoints.ListByProfile |
| microsoft.cdn | microsoft.cdn/profiles/endpoints/customdomains | 2 | resource-group | item-write | armcdn:CustomDomains.ListByEndpoint |
| microsoft.cdn | microsoft.cdn/profiles/endpoints/origingroups | 2 | resource-group | item-write | armcdn:OriginGroups.ListByEndpoint |
| microsoft.cdn | microsoft.cdn/profiles/endpoints/origins | 2 | resource-group | item-write | armcdn:Origins.ListByEndpoint |
| microsoft.cdn | microsoft.cdn/profiles/origingroups | 1 | resource-group | item-write | armcdn:AFDOriginGroups.ListByProfile |
| microsoft.cdn | microsoft.cdn/profiles/origingroups/origins | 2 | resource-group | item-write | armcdn:AFDOrigins.ListByOriginGroup |
| microsoft.cdn | microsoft.cdn/profiles/rulesets | 1 | resource-group | item-write | armcdn:RuleSets.ListByProfile |
| microsoft.cdn | microsoft.cdn/profiles/rulesets/rules | 2 | resource-group | item-write | armcdn:Rules.ListByRuleSet |
| microsoft.cdn | microsoft.cdn/profiles/secrets | 1 | resource-group | item-write | armcdn:Secrets.ListByProfile |
| microsoft.cdn | microsoft.cdn/profiles/securitypolicies | 1 | resource-group | item-write | armcdn:SecurityPolicies.ListByProfile |
| microsoft.certificateregistration | microsoft.certificateregistration/certificateorders/certificates | 1 | resource-group | item-write | armcertificateregistration:AppServiceCertificateOrders.ListCertificates |
| microsoft.certificateregistration | microsoft.certificateregistration/certificateorders/detectors | 1 | resource-group | arm-envelope | armcertificateregistration:CertificateOrdersDiagnostics.ListAppServiceCertificateOrderDetectorResponse |
| microsoft.chaos | microsoft.chaos/experiments/executions | 1 | resource-group | arm-envelope | armchaos:Experiments.ListAllExecutions |
| microsoft.chaos | microsoft.chaos/privateaccesses | 0 | subscription | item-write | armchaos:PrivateAccesses.List, armchaos:PrivateAccesses.ListAll |
| microsoft.chaos | microsoft.chaos/privateaccesses/privateendpointconnections | 1 | resource-group | item-write | armchaos:PrivateAccesses.ListPrivateEndpointConnections |
| microsoft.chaos | microsoft.chaos/targets | 0 | extension | item-write | armchaos:Targets.List |
| microsoft.chaos | microsoft.chaos/targets/capabilities | 1 | extension | item-write | armchaos:Capabilities.List |
| microsoft.chaos | microsoft.chaos/workspaces | 0 | subscription | item-write | armchaos:Workspaces.List, armchaos:Workspaces.ListAll |
| microsoft.chaos | microsoft.chaos/workspaces/connections | 1 | resource-group | item-write | armchaos:Connections.ListAll |
| microsoft.chaos | microsoft.chaos/workspaces/discoveredresources | 1 | resource-group | arm-envelope | armchaos:DiscoveredResources.ListByWorkspace |
| microsoft.chaos | microsoft.chaos/workspaces/scenarios | 1 | resource-group | item-write | armchaos:Scenarios.ListAll |
| microsoft.chaos | microsoft.chaos/workspaces/scenarios/configurations | 2 | resource-group | item-write | armchaos:ScenarioConfigurations.ListAll |
| microsoft.chaos | microsoft.chaos/workspaces/scenarios/runs | 2 | resource-group | arm-envelope | armchaos:ScenarioRuns.ListAll |
| microsoft.cloudhealth | microsoft.cloudhealth/healthmodels/authenticationsettings | 1 | resource-group | item-write | armcloudhealth:AuthenticationSettings.ListByHealthModel |
| microsoft.cloudhealth | microsoft.cloudhealth/healthmodels/discoveryrules | 1 | resource-group | item-write | armcloudhealth:DiscoveryRules.ListByHealthModel |
| microsoft.cloudhealth | microsoft.cloudhealth/healthmodels/entities | 1 | resource-group | item-write | armcloudhealth:Entities.ListByHealthModel |
| microsoft.cloudhealth | microsoft.cloudhealth/healthmodels/relationships | 1 | resource-group | item-write | armcloudhealth:Relationships.ListByHealthModel |
| microsoft.cloudhealth | microsoft.cloudhealth/healthmodels/signaldefinitions | 1 | resource-group | item-write | armcloudhealth:SignalDefinitions.ListByHealthModel |
| microsoft.codesigning | microsoft.codesigning/codesigningaccounts/certificateprofiles | 1 | resource-group | item-write | armartifactsigning:CertificateProfiles.ListByCodeSigningAccount, armtrustedsigning:CertificateProfiles.ListByCodeSigningAccount |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/arcdeployments | 1 | resource-group | item-write | armcognitiveservices:ArcDeployments.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/capabilityhosts | 1 | resource-group | item-write | armcognitiveservices:AccountCapabilityHosts.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/commitmentplans | 1 | resource-group | item-write | armcognitiveservices:CommitmentPlans.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/computes | 1 | resource-group | item-write | armcognitiveservices:Computes.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/connections | 1 | resource-group | item-write | armcognitiveservices:AccountConnections.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/defenderforaisettings | 1 | resource-group | item-write | armcognitiveservices:DefenderForAISettings.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/deployments | 1 | resource-group | item-write | armcognitiveservices:Deployments.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/encryptionscopes | 1 | resource-group | item-write | armcognitiveservices:EncryptionScopes.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/managedcomputedeployments | 1 | resource-group | item-write | armcognitiveservices:ManagedComputeDeployments.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/managednetworks | 1 | resource-group | item-write | armcognitiveservices:ManagedNetworkSettings.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/managednetworks/outboundrules | 2 | resource-group | item-write | armcognitiveservices:OutboundRule.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armcognitiveservices:NetworkSecurityPerimeterConfigurations.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/privateendpointconnections | 1 | resource-group | item-write | armcognitiveservices:PrivateEndpointConnections.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/projects | 1 | resource-group | item-write | armcognitiveservices:Projects.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/projects/applications | 2 | resource-group | item-write | armcognitiveservices:AgentApplications.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/projects/applications/agentdeployments | 3 | resource-group | item-write | armcognitiveservices:AgentDeployments.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/projects/capabilityhosts | 2 | resource-group | item-write | armcognitiveservices:ProjectCapabilityHosts.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/projects/connections | 2 | resource-group | item-write | armcognitiveservices:ProjectConnections.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/projects/workbenches | 2 | resource-group | item-write | armcognitiveservices:Workbenches.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/raiblocklists | 1 | resource-group | item-write | armcognitiveservices:RaiBlocklists.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/raiblocklists/raiblocklistitems | 2 | resource-group | item-write | armcognitiveservices:RaiBlocklistItems.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/raipolicies | 1 | resource-group | item-write | armcognitiveservices:RaiPolicies.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/raitoollabels | 1 | resource-group | item-write | armcognitiveservices:RaiToolLabels.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/accounts/raitopics | 1 | resource-group | item-write | armcognitiveservices:RaiTopics.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/commitmentplans/accountassociations | 1 | resource-group | item-write | armcognitiveservices:CommitmentPlans.ListAssociations |
| microsoft.cognitiveservices | microsoft.cognitiveservices/deletedaccounts | 0 | subscription | item-write | armcognitiveservices:DeletedAccounts.List |
| microsoft.cognitiveservices | microsoft.cognitiveservices/quotatiers | 0 | subscription | item-write | armcognitiveservices:QuotaTiers.ListBySubscription |
| microsoft.cognitiveservices | microsoft.cognitiveservices/raiexternalsafetyproviders | 0 | subscription | item-write | armcognitiveservices:RaiExternalSafetyProviders.List |
| microsoft.communication | microsoft.communication/communicationservices/smtpusernames | 1 | resource-group | item-write | armcommunication:SMTPUsernames.List |
| microsoft.communication | microsoft.communication/emailservices/domains | 1 | resource-group | item-write | armcommunication:Domains.ListByEmailServiceResource |
| microsoft.communication | microsoft.communication/emailservices/domains/senderusernames | 2 | resource-group | item-write | armcommunication:SenderUsernames.ListByDomains |
| microsoft.communication | microsoft.communication/emailservices/domains/suppressionlists | 2 | resource-group | item-write | armcommunication:SuppressionLists.ListByDomain |
| microsoft.communication | microsoft.communication/emailservices/domains/suppressionlists/suppressionlistaddresses | 3 | resource-group | item-write | armcommunication:SuppressionListAddresses.List |
| microsoft.compute | microsoft.compute/bulkcreate | 0 | subscription | item-write | armbulkactions:BulkCreate.ListByResourceGroup, armbulkactions:BulkCreate.ListBySubscription |
| microsoft.compute | microsoft.compute/bulkcreatecustom | 0 | subscription | item-write | armbulkactions:BulkCreateCustom.ListByResourceGroup, armbulkactions:BulkCreateCustom.ListBySubscription |
| microsoft.compute | microsoft.compute/cloudservices/roleinstances/networkinterfaces | 2 | resource-group | arm-envelope | armnetwork:Interfaces.ListCloudServiceRoleInstanceNetworkInterfaces |
| microsoft.compute | microsoft.compute/cloudservices/roleinstances/networkinterfaces/ipconfigurations/publicipaddresses | 4 | resource-group | arm-envelope | armnetwork:PublicIPAddresses.ListCloudServiceRoleInstancePublicIPAddresses |
| microsoft.compute | microsoft.compute/diskaccesses/privateendpointconnections | 1 | resource-group | item-write | armcompute:DiskAccesses.ListPrivateEndpointConnections |
| microsoft.compute | microsoft.compute/galleries/scripts | 1 | resource-group | item-write | armcompute:GalleryScripts.ListByGallery |
| microsoft.compute | microsoft.compute/galleries/scripts/versions | 2 | resource-group | item-write | armcompute:GalleryScriptVersions.ListByGalleryScript |
| microsoft.compute | microsoft.compute/interconnectblocks | 0 | subscription | item-write | armcompute:InterconnectBlocks.ListByResourceGroup, armcompute:InterconnectBlocks.ListBySubscription |
| microsoft.compute | microsoft.compute/restorepointcollections/restorepoints/diskrestorepoints | 2 | resource-group | arm-envelope | armcompute:DiskRestorePoint.ListByRestorePoint |
| microsoft.compute | microsoft.compute/scheduledactions | 0 | subscription | item-write | armbulkactions:ScheduledActions.ListByResourceGroup, armbulkactions:ScheduledActions.ListBySubscription |
| microsoft.compute | microsoft.compute/scheduledactions/occurrences | 1 | resource-group | arm-envelope | armbulkactions:Occurrences.ListByScheduledAction |
| microsoft.compute | microsoft.compute/virtualmachines/diagnosticruncommands | 1 | resource-group | item-write | armcompute:VirtualMachineDiagnosticRunCommands.DiagnosticListByVirtualMachine |
| microsoft.compute | microsoft.compute/virtualmachines/runcommands | 1 | resource-group | item-write | armcompute:VirtualMachineRunCommands.ListByVirtualMachine |
| microsoft.compute | microsoft.compute/virtualmachinescalesets/lifecyclehookevents | 1 | resource-group | item-write | armcompute:VirtualMachineScaleSetLifeCycleHookEvents.List |
| microsoft.compute | microsoft.compute/virtualmachinescalesets/virtualmachines/diagnosticruncommands | 2 | resource-group | item-write | armcompute:VirtualMachineScaleSetVMDiagnosticRunCommands.DiagnosticList |
| microsoft.compute | microsoft.compute/virtualmachinescalesets/virtualmachines/networkinterfaces | 2 | resource-group | arm-envelope | armnetwork:Interfaces.ListVirtualMachineScaleSetVMNetworkInterfaces |
| microsoft.compute | microsoft.compute/virtualmachinescalesets/virtualmachines/networkinterfaces/ipconfigurations | 3 | resource-group | arm-envelope | armnetwork:Interfaces.ListVirtualMachineScaleSetIPConfigurations |
| microsoft.compute | microsoft.compute/virtualmachinescalesets/virtualmachines/networkinterfaces/ipconfigurations/publicipaddresses | 4 | resource-group | arm-envelope | armnetwork:PublicIPAddresses.ListVirtualMachineScaleSetVMPublicIPAddresses |
| microsoft.compute | microsoft.compute/virtualmachinescalesets/virtualmachines/runcommands | 2 | resource-group | item-write | armcompute:VirtualMachineScaleSetVMRunCommands.List |
| microsoft.computebulkactions | microsoft.computebulkactions/launchbulkinstancesoperations | 0 | subscription | item-write | armcomputebulkactions:BulkActions.ListByResourceGroup, armcomputebulkactions:BulkActions.ListBySubscription |
| microsoft.computelimit | microsoft.computelimit/guestsubscriptions | 0 | subscription | item-write | armcomputelimit:GuestSubscriptions.ListBySubscriptionLocationResource |
| microsoft.computelimit | microsoft.computelimit/sharedlimitcaps | 0 | subscription | item-write | armcomputelimit:SharedLimitCaps.ListBySubscriptionLocationResource |
| microsoft.computelimit | microsoft.computelimit/sharedlimitcaps/membercapoverrides | 1 | subscription | item-write | armcomputelimit:MemberCapOverrides.ListByParent |
| microsoft.computelimit | microsoft.computelimit/sharedlimits | 0 | subscription | item-write | armcomputelimit:SharedLimits.ListBySubscriptionLocationResource |
| microsoft.computelimit | microsoft.computelimit/trustedhostsubscriptions | 0 | subscription | item-write | armcomputelimit:TrustedHostSubscriptions.ListBySubscriptionLocationResource |
| microsoft.computeschedule | microsoft.computeschedule/scheduledactions | 0 | subscription | item-write | armcomputeschedule:ScheduledActions.ListByResourceGroup, armcomputeschedule:ScheduledActions.ListBySubscription |
| microsoft.computeschedule | microsoft.computeschedule/scheduledactions/occurrences | 1 | resource-group | arm-envelope | armcomputeschedule:Occurrences.ListByScheduledAction |
| microsoft.confidentialledger | microsoft.confidentialledger/managedccfs | 0 | subscription | item-write | armconfidentialledger:ManagedCCF.ListByResourceGroup, armconfidentialledger:ManagedCCF.ListBySubscription |
| microsoft.confluent | microsoft.confluent/agreements | 0 | subscription | item-write | armconfluent:MarketplaceAgreements.List |
| microsoft.confluent | microsoft.confluent/organizations | 0 | subscription | item-write | armconfluent:Organization.ListByResourceGroup, armconfluent:Organization.ListBySubscription |
| microsoft.confluent | microsoft.confluent/organizations/environments | 1 | resource-group | item-write | armconfluent:Organization.ListEnvironments |
| microsoft.confluent | microsoft.confluent/organizations/environments/clusters | 2 | resource-group | item-write | armconfluent:Organization.ListClusters |
| microsoft.confluent | microsoft.confluent/organizations/environments/clusters/connectors | 3 | resource-group | item-write | armconfluent:Connector.List |
| microsoft.confluent | microsoft.confluent/organizations/environments/clusters/topics | 3 | resource-group | item-write | armconfluent:Topics.List |
| microsoft.confluent | microsoft.confluent/organizations/environments/networkgateways | 2 | resource-group | item-write | armconfluent:NetworkGatewayResources.List |
| microsoft.confluent | microsoft.confluent/organizations/environments/networkgateways/accesspoints | 3 | resource-group | item-write | armconfluent:AccessPointResources.List |
| microsoft.connectedcache | microsoft.connectedcache/enterprisemcccustomers/enterprisemcccachenodes | 1 | resource-group | item-write | armconnectedcache:EnterpriseMccCacheNodesOperations.ListByEnterpriseMccCustomerResource |
| microsoft.connectedcache | microsoft.connectedcache/ispcustomers/ispcachenodes | 1 | resource-group | item-write | armconnectedcache:IspCacheNodesOperations.ListByIspCustomerResource |
| microsoft.connectedvmwarevsphere | microsoft.connectedvmwarevsphere/vcenters/inventoryitems | 1 | resource-group | item-write | armconnectedvmware:InventoryItems.ListByVCenter |
| microsoft.connectedvmwarevsphere | microsoft.connectedvmwarevsphere/virtualmachineinstances | 0 | extension | item-write | armconnectedvmware:VirtualMachineInstances.List |
| microsoft.connectedvmwarevsphere | microsoft.connectedvmwarevsphere/virtualmachineinstances/guestagents | 1 | extension | item-write | armconnectedvmware:VMInstanceGuestAgents.List |
| microsoft.connectedvmwarevsphere | microsoft.connectedvmwarevsphere/virtualmachineinstances/hybrididentitymetadata | 1 | extension | arm-envelope | armconnectedvmware:VMInstanceHybridIdentityMetadata.List |
| microsoft.consumption | microsoft.consumption/budgets | 0 | extension | item-write | armconsumption:Budgets.List |
| microsoft.containerinstance | microsoft.containerinstance/containergroupprofiles | 0 | subscription | item-write | armcontainerinstance:CGProfiles.ListByResourceGroup, armcontainerinstance:CGProfiles.ListBySubscription |
| microsoft.containerinstance | microsoft.containerinstance/containergroupprofiles/revisions | 1 | resource-group | arm-envelope | armcontainerinstance:CGProfile.ListAllRevisions |
| microsoft.containerinstance | microsoft.containerinstance/ngroups | 0 | subscription | item-write | armcontainerinstance:NGroups.List, armcontainerinstance:NGroups.ListByResourceGroup |
| microsoft.containerregistry | microsoft.containerregistry/registries/agentpools | 1 | resource-group | item-write | armcontainerregistrytasks:AgentPools.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/cacherules | 1 | resource-group | item-write | armcontainerregistry:CacheRules.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/connectedregistries | 1 | resource-group | item-write | armcontainerregistry:ConnectedRegistries.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/credentialsets | 1 | resource-group | item-write | armcontainerregistry:CredentialSets.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/exportpipelines | 1 | resource-group | item-write | armcontainerregistry:ExportPipelines.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/importpipelines | 1 | resource-group | item-write | armcontainerregistry:ImportPipelines.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/packages/archives | 2 | resource-group | item-write | armcontainerregistry:Archives.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/packages/archives/versions | 3 | resource-group | item-write | armcontainerregistry:ArchiveVersions.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/pipelineruns | 1 | resource-group | item-write | armcontainerregistry:PipelineRuns.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/privateendpointconnections | 1 | resource-group | item-write | armcontainerregistry:PrivateEndpointConnections.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/privatelinkresources | 1 | resource-group | arm-envelope | armcontainerregistry:Registries.ListPrivateLinkResources |
| microsoft.containerregistry | microsoft.containerregistry/registries/replications | 1 | resource-group | item-write | armcontainerregistry:Replications.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/runs | 1 | resource-group | item-write | armcontainerregistrytasks:Runs.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/scopemaps | 1 | resource-group | item-write | armcontainerregistry:ScopeMaps.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/taskruns | 1 | resource-group | item-write | armcontainerregistrytasks:TaskRuns.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/tasks | 1 | resource-group | item-write | armcontainerregistrytasks:Tasks.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/tokens | 1 | resource-group | item-write | armcontainerregistry:Tokens.List |
| microsoft.containerregistry | microsoft.containerregistry/registries/webhooks | 1 | resource-group | item-write | armcontainerregistry:Webhooks.List |
| microsoft.containerservice | microsoft.containerservice/aimanagers | 0 | subscription | item-write | armcontainerserviceaimanager:AIManagers.ListByResourceGroup, armcontainerserviceaimanager:AIManagers.ListBySubscription |
| microsoft.containerservice | microsoft.containerservice/aimanagers/modelsources | 1 | resource-group | item-write | armcontainerserviceaimanager:ModelSources.List |
| microsoft.containerservice | microsoft.containerservice/aimanagers/namespaces | 1 | resource-group | item-write | armcontainerserviceaimanager:AIManagerNamespaces.ListByAIManager |
| microsoft.containerservice | microsoft.containerservice/aimanagers/namespaces/modeldeployments | 2 | resource-group | item-write | armcontainerserviceaimanager:ModelDeployments.ListByAIManagerNamespace |
| microsoft.containerservice | microsoft.containerservice/deploymentsafeguards | 0 | extension | item-write | armdeploymentsafeguards:Client.List |
| microsoft.containerservice | microsoft.containerservice/fleets/autoupgradeprofiles | 1 | resource-group | item-write | armcontainerservicefleet:AutoUpgradeProfiles.ListByFleet |
| microsoft.containerservice | microsoft.containerservice/fleets/clustermeshprofiles | 1 | resource-group | item-write | armcontainerservicefleet:ClusterMeshProfiles.ListByFleet |
| microsoft.containerservice | microsoft.containerservice/fleets/gates | 1 | resource-group | item-write | armcontainerservicefleet:Gates.ListByFleet |
| microsoft.containerservice | microsoft.containerservice/fleets/managednamespaces | 1 | resource-group | item-write | armcontainerservicefleet:FleetManagedNamespaces.ListByFleet |
| microsoft.containerservice | microsoft.containerservice/fleets/members | 1 | resource-group | item-write | armcontainerservicefleet:FleetMembers.ListByFleet |
| microsoft.containerservice | microsoft.containerservice/fleets/updateruns | 1 | resource-group | item-write | armcontainerservicefleet:UpdateRuns.ListByFleet |
| microsoft.containerservice | microsoft.containerservice/fleets/updatestrategies | 1 | resource-group | item-write | armcontainerservicefleet:FleetUpdateStrategies.ListByFleet |
| microsoft.containerservice | microsoft.containerservice/maintenancewindows | 0 | subscription | item-write | armcontainerservice:MaintenanceWindows.List, armcontainerservice:MaintenanceWindows.ListBySubscription |
| microsoft.containerservice | microsoft.containerservice/managedclusters/agentpools | 1 | resource-group | item-write | armcontainerservice:AgentPools.List |
| microsoft.containerservice | microsoft.containerservice/managedclusters/agentpools/machines | 2 | resource-group | item-write | armcontainerservice:Machines.List |
| microsoft.containerservice | microsoft.containerservice/managedclusters/alertconfigurations | 1 | resource-group | item-write | armcontainerservice:AlertConfigurations.ListByManagedCluster |
| microsoft.containerservice | microsoft.containerservice/managedclusters/identitybindings | 1 | resource-group | item-write | armcontainerservice:IdentityBindings.ListByManagedCluster |
| microsoft.containerservice | microsoft.containerservice/managedclusters/jwtauthenticators | 1 | resource-group | item-write | armcontainerservice:JWTAuthenticators.ListByManagedCluster |
| microsoft.containerservice | microsoft.containerservice/managedclusters/loadbalancers | 1 | resource-group | item-write | armcontainerservice:LoadBalancers.ListByManagedCluster |
| microsoft.containerservice | microsoft.containerservice/managedclusters/maintenanceconfigurations | 1 | resource-group | item-write | armcontainerservice:MaintenanceConfigurations.ListByManagedCluster |
| microsoft.containerservice | microsoft.containerservice/managedclusters/managednamespaces | 1 | resource-group | item-write | armcontainerservice:ManagedNamespaces.ListByManagedCluster |
| microsoft.containerservice | microsoft.containerservice/managedclusters/meshmemberships | 1 | resource-group | item-write | armcontainerservice:MeshMemberships.ListByManagedCluster |
| microsoft.containerservice | microsoft.containerservice/managedclusters/meshupgradeprofiles | 1 | resource-group | arm-envelope | armcontainerservice:ManagedClusters.ListMeshUpgradeProfiles |
| microsoft.containerservice | microsoft.containerservice/managedclusters/privateendpointconnections | 1 | resource-group | item-write | armcontainerservice:PrivateEndpointConnections.List |
| microsoft.containerservice | microsoft.containerservice/managedclusters/trustedaccessrolebindings | 1 | resource-group | item-write | armcontainerservice:TrustedAccessRoleBindings.List |
| microsoft.containerservice | microsoft.containerservice/managedclustersnapshots | 0 | subscription | item-write | armcontainerservice:ManagedClusterSnapshots.List, armcontainerservice:ManagedClusterSnapshots.ListByResourceGroup |
| microsoft.containerservice | microsoft.containerservice/preparedimagespecifications | 0 | subscription | item-write | armcontainerservicepreparedimgspec:PreparedImageSpecifications.ListByResourceGroup, armcontainerservicepreparedimgspec:PreparedImageSpecifications.ListBySubscription |
| microsoft.containerservice | microsoft.containerservice/preparedimagespecifications/versions | 1 | resource-group | item-write | armcontainerservicepreparedimgspec:PreparedImageSpecifications.ListVersions |
| microsoft.costmanagement | microsoft.costmanagement/alerts | 0 | extension | item-write | armcostmanagement:Alerts.List, armcostmanagement:Alerts.ListExternal |
| microsoft.costmanagement | microsoft.costmanagement/budgets | 0 | extension | item-write | armcostmanagement:Budgets.List |
| microsoft.costmanagement | microsoft.costmanagement/costallocationrules | 0 | tenant | item-write | armcostmanagement:CostAllocationRules.List |
| microsoft.costmanagement | microsoft.costmanagement/exports | 0 | extension | item-write | armcostmanagement:Exports.List |
| microsoft.costmanagement | microsoft.costmanagement/scheduledactions | 0 | extension | item-write | armcostmanagement:ScheduledActions.List, armcostmanagement:ScheduledActions.ListByScope |
| microsoft.costmanagement | microsoft.costmanagement/settings | 0 | extension | item-write | armcostmanagement:Settings.List |
| microsoft.costmanagement | microsoft.costmanagement/views | 0 | extension | item-write | armcostmanagement:Views.List, armcostmanagement:Views.ListByScope |
| microsoft.customerinsights | microsoft.customerinsights/hubs | 0 | subscription | item-write | armcustomerinsights:Hubs.List, armcustomerinsights:Hubs.ListByResourceGroup |
| microsoft.customerinsights | microsoft.customerinsights/hubs/authorizationpolicies | 1 | resource-group | item-write | armcustomerinsights:AuthorizationPolicies.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/connectors | 1 | resource-group | item-write | armcustomerinsights:Connectors.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/connectors/mappings | 2 | resource-group | item-write | armcustomerinsights:ConnectorMappings.ListByConnector |
| microsoft.customerinsights | microsoft.customerinsights/hubs/interactions | 1 | resource-group | item-write | armcustomerinsights:Interactions.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/kpi | 1 | resource-group | item-write | armcustomerinsights:Kpi.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/links | 1 | resource-group | item-write | armcustomerinsights:Links.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/predictions | 1 | resource-group | item-write | armcustomerinsights:Predictions.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/profiles | 1 | resource-group | item-write | armcustomerinsights:Profiles.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/relationshiplinks | 1 | resource-group | item-write | armcustomerinsights:RelationshipLinks.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/relationships | 1 | resource-group | item-write | armcustomerinsights:Relationships.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/roleassignments | 1 | resource-group | item-write | armcustomerinsights:RoleAssignments.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/views | 1 | resource-group | item-write | armcustomerinsights:Views.ListByHub |
| microsoft.customerinsights | microsoft.customerinsights/hubs/widgettypes | 1 | resource-group | arm-envelope | armcustomerinsights:WidgetTypes.ListByHub |
| microsoft.customerlockbox | microsoft.customerlockbox/requests | 0 | subscription | arm-envelope | armcustomerlockbox:Requests.List |
| microsoft.customproviders | microsoft.customproviders/associations | 0 | extension | item-write | armcustomproviders:Associations.ListAll |
| microsoft.dashboard | microsoft.dashboard/dashboards | 0 | subscription | item-write | armdashboard:ManagedDashboards.List, armdashboard:ManagedDashboards.ListBySubscription |
| microsoft.dashboard | microsoft.dashboard/grafana/integrationfabrics | 1 | resource-group | item-write | armdashboard:IntegrationFabrics.List |
| microsoft.dashboard | microsoft.dashboard/grafana/managedprivateendpoints | 1 | resource-group | item-write | armdashboard:ManagedPrivateEndpoints.List |
| microsoft.dashboard | microsoft.dashboard/grafana/privateendpointconnections | 1 | resource-group | item-write | armdashboard:PrivateEndpointConnections.List |
| microsoft.dashboard | microsoft.dashboard/grafana/privatelinkresources | 1 | resource-group | arm-envelope | armdashboard:PrivateLinkResources.List |
| microsoft.databasewatcher | microsoft.databasewatcher/watchers/alertruleresources | 1 | resource-group | item-write | armdatabasewatcher:AlertRuleResources.ListByParent |
| microsoft.databasewatcher | microsoft.databasewatcher/watchers/healthvalidations | 1 | resource-group | arm-envelope | armdatabasewatcher:HealthValidations.ListByParent |
| microsoft.databasewatcher | microsoft.databasewatcher/watchers/sharedprivatelinkresources | 1 | resource-group | item-write | armdatabasewatcher:SharedPrivateLinkResources.ListByWatcher |
| microsoft.databasewatcher | microsoft.databasewatcher/watchers/targets | 1 | resource-group | item-write | armdatabasewatcher:Targets.ListByWatcher |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/alerts | 1 | resource-group | arm-envelope | armdataboxedge:Alerts.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/bandwidthschedules | 1 | resource-group | item-write | armdataboxedge:BandwidthSchedules.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/orders | 1 | resource-group | item-write | armdataboxedge:Orders.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/roles | 1 | resource-group | item-write | armdataboxedge:Roles.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/roles/addons | 2 | resource-group | item-write | armdataboxedge:Addons.ListByRole |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/roles/monitoringconfig | 2 | resource-group | item-write | armdataboxedge:MonitoringConfig.List |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/shares | 1 | resource-group | item-write | armdataboxedge:Shares.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/storageaccountcredentials | 1 | resource-group | item-write | armdataboxedge:StorageAccountCredentials.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/storageaccounts | 1 | resource-group | item-write | armdataboxedge:StorageAccounts.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/storageaccounts/containers | 2 | resource-group | item-write | armdataboxedge:Containers.ListByStorageAccount |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/triggers | 1 | resource-group | item-write | armdataboxedge:Triggers.ListByDataBoxEdgeDevice |
| microsoft.databoxedge | microsoft.databoxedge/databoxedgedevices/users | 1 | resource-group | item-write | armdataboxedge:Users.ListByDataBoxEdgeDevice |
| microsoft.databricks | microsoft.databricks/workspaces/privateendpointconnections | 1 | resource-group | item-write | armdatabricks:PrivateEndpointConnections.List |
| microsoft.databricks | microsoft.databricks/workspaces/privatelinkresources | 1 | resource-group | arm-envelope | armdatabricks:PrivateLinkResources.List |
| microsoft.databricks | microsoft.databricks/workspaces/virtualnetworkpeerings | 1 | resource-group | item-write | armdatabricks:VNetPeering.ListByWorkspace |
| microsoft.datacatalog | microsoft.datacatalog/catalogs | 0 | resource-group | item-write | armdatacatalog:ADCCatalogs.ListtByResourceGroup |
| microsoft.datadog | microsoft.datadog/agreements | 0 | subscription | item-write | armdatadog:MarketplaceAgreements.List |
| microsoft.datadog | microsoft.datadog/monitors | 0 | subscription | item-write | armdatadog:Monitors.List, armdatadog:Monitors.ListByResourceGroup |
| microsoft.datadog | microsoft.datadog/monitors/monitoredsubscriptions | 1 | resource-group | item-write | armdatadog:MonitoredSubscriptions.List |
| microsoft.datadog | microsoft.datadog/monitors/singlesignonconfigurations | 1 | resource-group | item-write | armdatadog:SingleSignOnConfigurations.List |
| microsoft.datadog | microsoft.datadog/monitors/tagrules | 1 | resource-group | item-write | armdatadog:TagRules.List |
| microsoft.datafactory | microsoft.datafactory/factories/adfcdcs | 1 | resource-group | item-write | armdatafactory:ChangeDataCapture.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/credentials | 1 | resource-group | item-write | armdatafactory:CredentialOperations.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/dataflows | 1 | resource-group | item-write | armdatafactory:DataFlows.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/datasets | 1 | resource-group | item-write | armdatafactory:Datasets.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/globalparameters | 1 | resource-group | item-write | armdatafactory:GlobalParameters.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/integrationruntimes | 1 | resource-group | item-write | armdatafactory:IntegrationRuntimes.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/linkedservices | 1 | resource-group | item-write | armdatafactory:LinkedServices.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/managedvirtualnetworks | 1 | resource-group | item-write | armdatafactory:ManagedVirtualNetworks.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/managedvirtualnetworks/managedprivateendpoints | 2 | resource-group | item-write | armdatafactory:ManagedPrivateEndpoints.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/pipelines | 1 | resource-group | item-write | armdatafactory:Pipelines.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/privateendpointconnections | 1 | resource-group | item-write | armdatafactory:PrivateEndPointConnections.ListByFactory |
| microsoft.datafactory | microsoft.datafactory/factories/triggers | 1 | resource-group | item-write | armdatafactory:Triggers.ListByFactory |
| microsoft.datalakeanalytics | microsoft.datalakeanalytics/accounts | 0 | subscription | item-write | armdatalakeanalytics:Accounts.List, armdatalakeanalytics:Accounts.ListByResourceGroup |
| microsoft.datalakeanalytics | microsoft.datalakeanalytics/accounts/computepolicies | 1 | resource-group | item-write | armdatalakeanalytics:ComputePolicies.ListByAccount |
| microsoft.datalakeanalytics | microsoft.datalakeanalytics/accounts/datalakestoreaccounts | 1 | resource-group | item-write | armdatalakeanalytics:DataLakeStoreAccounts.ListByAccount |
| microsoft.datalakeanalytics | microsoft.datalakeanalytics/accounts/firewallrules | 1 | resource-group | item-write | armdatalakeanalytics:FirewallRules.ListByAccount |
| microsoft.datalakeanalytics | microsoft.datalakeanalytics/accounts/storageaccounts | 1 | resource-group | item-write | armdatalakeanalytics:StorageAccounts.ListByAccount |
| microsoft.datalakeanalytics | microsoft.datalakeanalytics/accounts/storageaccounts/containers | 2 | resource-group | arm-envelope | armdatalakeanalytics:StorageAccounts.ListStorageContainers |
| microsoft.datalakestore | microsoft.datalakestore/accounts | 0 | subscription | item-write | armdatalakestore:Accounts.List, armdatalakestore:Accounts.ListByResourceGroup |
| microsoft.datalakestore | microsoft.datalakestore/accounts/firewallrules | 1 | resource-group | item-write | armdatalakestore:FirewallRules.ListByAccount |
| microsoft.datalakestore | microsoft.datalakestore/accounts/trustedidproviders | 1 | resource-group | item-write | armdatalakestore:TrustedIDProviders.ListByAccount |
| microsoft.datalakestore | microsoft.datalakestore/accounts/virtualnetworkrules | 1 | resource-group | item-write | armdatalakestore:VirtualNetworkRules.ListByAccount |
| microsoft.datamigration | microsoft.datamigration/databasemigrations | 0 | resource-group | item-write | armdatamigration:DatabaseMigrationsMongoToCosmosDbRUMongo.GetForScope, armdatamigration:DatabaseMigrationsMongoToCosmosDbvCoreMongo.GetForScope |
| microsoft.datamigration | microsoft.datamigration/migrationservices | 0 | subscription | item-write | armdatamigration:MigrationServices.ListByResourceGroup, armdatamigration:MigrationServices.ListBySubscription |
| microsoft.datamigration | microsoft.datamigration/services/projects | 1 | resource-group | item-write | armdatamigration:Projects.List |
| microsoft.datamigration | microsoft.datamigration/services/projects/files | 2 | resource-group | item-write | armdatamigration:Files.List |
| microsoft.datamigration | microsoft.datamigration/services/projects/tasks | 2 | resource-group | item-write | armdatamigration:Tasks.List |
| microsoft.datamigration | microsoft.datamigration/services/servicetasks | 1 | resource-group | item-write | armdatamigration:ServiceTasks.List |
| microsoft.datamigration | microsoft.datamigration/sqlmigrationservices | 0 | subscription | item-write | armdatamigration:SQLMigrationServices.ListByResourceGroup, armdatamigration:SQLMigrationServices.ListBySubscription |
| microsoft.dataprotection | microsoft.dataprotection/backupvaults/backupinstances | 1 | resource-group | item-write | armdataprotection:BackupInstances.List, armdataprotection:BackupInstancesExtensionRouting.List |
| microsoft.dataprotection | microsoft.dataprotection/backupvaults/backupinstances/recoverypoints | 2 | resource-group | arm-envelope | armdataprotection:RecoveryPoints.List |
| microsoft.dataprotection | microsoft.dataprotection/backupvaults/backupjobs | 1 | resource-group | arm-envelope | armdataprotection:Jobs.List |
| microsoft.dataprotection | microsoft.dataprotection/backupvaults/backuppolicies | 1 | resource-group | item-write | armdataprotection:BackupPolicies.List |
| microsoft.dataprotection | microsoft.dataprotection/backupvaults/backupresourceguardproxies | 1 | resource-group | item-write | armdataprotection:DppResourceGuardProxy.List |
| microsoft.dataprotection | microsoft.dataprotection/backupvaults/deletedbackupinstances | 1 | resource-group | arm-envelope | armdataprotection:DeletedBackupInstances.List |
| microsoft.dataprotection | microsoft.dataprotection/resourceguards/deleteprotecteditemrequests | 1 | resource-group | arm-envelope | armdataprotection:ResourceGuards.GetDeleteProtectedItemRequestsObjects |
| microsoft.dataprotection | microsoft.dataprotection/resourceguards/deleteresourceguardproxyrequests | 1 | resource-group | arm-envelope | armdataprotection:ResourceGuards.GetDeleteResourceGuardProxyRequestsObjects |
| microsoft.dataprotection | microsoft.dataprotection/resourceguards/disablesoftdeleterequests | 1 | resource-group | arm-envelope | armdataprotection:ResourceGuards.GetDisableSoftDeleteRequestsObjects |
| microsoft.dataprotection | microsoft.dataprotection/resourceguards/getbackupsecuritypinrequests | 1 | resource-group | arm-envelope | armdataprotection:ResourceGuards.GetBackupSecurityPINRequestsObjects |
| microsoft.dataprotection | microsoft.dataprotection/resourceguards/updateprotecteditemrequests | 1 | resource-group | arm-envelope | armdataprotection:ResourceGuards.GetUpdateProtectedItemRequestsObjects |
| microsoft.dataprotection | microsoft.dataprotection/resourceguards/updateprotectionpolicyrequests | 1 | resource-group | arm-envelope | armdataprotection:ResourceGuards.GetUpdateProtectionPolicyRequestsObjects |
| microsoft.datareplication | microsoft.datareplication/replicationfabrics/fabricagents | 1 | resource-group | item-write | armrecoveryservicesdatareplication:FabricAgent.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/alertsettings | 1 | resource-group | item-write | armrecoveryservicesdatareplication:EmailConfiguration.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/events | 1 | resource-group | arm-envelope | armrecoveryservicesdatareplication:Event.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/jobs | 1 | resource-group | arm-envelope | armrecoveryservicesdatareplication:Job.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/privateendpointconnections | 1 | resource-group | item-write | armrecoveryservicesdatareplication:PrivateEndpointConnections.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/privatelinkresources | 1 | resource-group | arm-envelope | armrecoveryservicesdatareplication:PrivateLinkResources.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/protecteditems | 1 | resource-group | item-write | armrecoveryservicesdatareplication:ProtectedItem.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/protecteditems/recoverypoints | 2 | resource-group | arm-envelope | armrecoveryservicesdatareplication:RecoveryPoint.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/replicationextensions | 1 | resource-group | item-write | armrecoveryservicesdatareplication:ReplicationExtension.List |
| microsoft.datareplication | microsoft.datareplication/replicationvaults/replicationpolicies | 1 | resource-group | item-write | armrecoveryservicesdatareplication:Policy.List |
| microsoft.datashare | microsoft.datashare/accounts/shares | 1 | resource-group | item-write | armdatashare:Shares.ListByAccount |
| microsoft.datashare | microsoft.datashare/accounts/shares/datasets | 2 | resource-group | item-write | armdatashare:DataSets.ListByShare |
| microsoft.datashare | microsoft.datashare/accounts/shares/invitations | 2 | resource-group | item-write | armdatashare:Invitations.ListByShare |
| microsoft.datashare | microsoft.datashare/accounts/shares/providersharesubscriptions | 2 | resource-group | arm-envelope | armdatashare:ProviderShareSubscriptions.ListByShare |
| microsoft.datashare | microsoft.datashare/accounts/shares/synchronizationsettings | 2 | resource-group | item-write | armdatashare:SynchronizationSettings.ListByShare |
| microsoft.datashare | microsoft.datashare/accounts/sharesubscriptions | 1 | resource-group | item-write | armdatashare:ShareSubscriptions.ListByAccount |
| microsoft.datashare | microsoft.datashare/accounts/sharesubscriptions/datasetmappings | 2 | resource-group | item-write | armdatashare:DataSetMappings.ListByShareSubscription |
| microsoft.datashare | microsoft.datashare/accounts/sharesubscriptions/triggers | 2 | resource-group | item-write | armdatashare:Triggers.ListByShareSubscription |
| microsoft.dbformariadb | microsoft.dbformariadb/servers | 0 | subscription | item-write | armmariadb:Replicas.ListByServer, armmariadb:Servers.List, armmariadb:Servers.ListByResourceGroup |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/advisors | 1 | resource-group | arm-envelope | armmariadb:Advisors.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/advisors/recommendedactions | 2 | resource-group | arm-envelope | armmariadb:RecommendedActions.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/configurations | 1 | resource-group | item-write | armmariadb:Configurations.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/databases | 1 | resource-group | item-write | armmariadb:Databases.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/firewallrules | 1 | resource-group | item-write | armmariadb:FirewallRules.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/privateendpointconnections | 1 | resource-group | item-write | armmariadb:PrivateEndpointConnections.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/privatelinkresources | 1 | resource-group | arm-envelope | armmariadb:PrivateLinkResources.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/querytexts | 1 | resource-group | arm-envelope | armmariadb:QueryTexts.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/securityalertpolicies | 1 | resource-group | item-write | armmariadb:ServerSecurityAlertPolicies.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/topquerystatistics | 1 | resource-group | arm-envelope | armmariadb:TopQueryStatistics.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/virtualnetworkrules | 1 | resource-group | item-write | armmariadb:VirtualNetworkRules.ListByServer |
| microsoft.dbformariadb | microsoft.dbformariadb/servers/waitstatistics | 1 | resource-group | arm-envelope | armmariadb:WaitStatistics.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/administrators | 1 | resource-group | item-write | armmysqlflexibleservers:AzureADAdministrators.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/advancedthreatprotectionsettings | 1 | resource-group | item-write | armmysqlflexibleservers:AdvancedThreatProtectionSettings.List |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/backups | 1 | resource-group | item-write | armmysqlflexibleservers:Backups.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/backupsv2 | 1 | resource-group | item-write | armmysqlflexibleservers:LongRunningBackups.List |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/configurations | 1 | resource-group | item-write | armmysqlflexibleservers:Configurations.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/databases | 1 | resource-group | item-write | armmysqlflexibleservers:Databases.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/firewallrules | 1 | resource-group | item-write | armmysqlflexibleservers:FirewallRules.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/maintenances | 1 | resource-group | item-write | armmysqlflexibleservers:Maintenances.List |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/privateendpointconnections | 1 | resource-group | item-write | armmysqlflexibleservers:PrivateEndpointConnections.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/flexibleservers/privatelinkresources | 1 | resource-group | arm-envelope | armmysqlflexibleservers:PrivateLinkResources.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers | 0 | subscription | item-write | armmysql:Replicas.ListByServer, armmysql:Servers.List, armmysql:Servers.ListByResourceGroup |
| microsoft.dbformysql | microsoft.dbformysql/servers/administrators | 1 | resource-group | item-write | armmysql:ServerAdministrators.List |
| microsoft.dbformysql | microsoft.dbformysql/servers/advisors | 1 | resource-group | arm-envelope | armmysql:Advisors.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/advisors/recommendedactions | 2 | resource-group | arm-envelope | armmysql:RecommendedActions.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/configurations | 1 | resource-group | item-write | armmysql:Configurations.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/databases | 1 | resource-group | item-write | armmysql:Databases.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/firewallrules | 1 | resource-group | item-write | armmysql:FirewallRules.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/keys | 1 | resource-group | item-write | armmysql:ServerKeys.List |
| microsoft.dbformysql | microsoft.dbformysql/servers/privateendpointconnections | 1 | resource-group | item-write | armmysql:PrivateEndpointConnections.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/privatelinkresources | 1 | resource-group | arm-envelope | armmysql:PrivateLinkResources.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/querytexts | 1 | resource-group | arm-envelope | armmysql:QueryTexts.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/securityalertpolicies | 1 | resource-group | item-write | armmysql:ServerSecurityAlertPolicies.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/topquerystatistics | 1 | resource-group | arm-envelope | armmysql:TopQueryStatistics.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/virtualnetworkrules | 1 | resource-group | item-write | armmysql:VirtualNetworkRules.ListByServer |
| microsoft.dbformysql | microsoft.dbformysql/servers/waitstatistics | 1 | resource-group | arm-envelope | armmysql:WaitStatistics.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/administrators | 1 | resource-group | item-write | armpostgresqlflexibleservers:AdministratorsMicrosoftEntra.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/advancedthreatprotectionsettings | 1 | resource-group | item-write | armpostgresqlflexibleservers:AdvancedThreatProtectionSettings.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/backups | 1 | resource-group | item-write | armpostgresqlflexibleservers:BackupsAutomaticAndOnDemand.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/configurations | 1 | resource-group | item-write | armpostgresqlflexibleservers:Configurations.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/databases | 1 | resource-group | item-write | armpostgresqlflexibleservers:Databases.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/firewallrules | 1 | resource-group | item-write | armpostgresqlflexibleservers:FirewallRules.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/ltrbackupoperations | 1 | resource-group | arm-envelope | armpostgresqlflexibleservers:BackupsLongTermRetention.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/maintenanceevents | 1 | resource-group | arm-envelope | armpostgresqlflexibleservers:MaintenanceEvents.List |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/majorversionupgradeprecheck | 1 | resource-group | arm-envelope | armpostgresqlflexibleservers:MajorVersionUpgradePrecheck.List |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/migrations | 1 | resource-group | item-write | armpostgresqlflexibleservers:Migrations.ListByTargetServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/privateendpointconnections | 1 | resource-group | item-write | armpostgresqlflexibleservers:PrivateEndpointConnections.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/privatelinkresources | 1 | resource-group | arm-envelope | armpostgresqlflexibleservers:PrivateLinkResources.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/tuningoptions | 1 | resource-group | arm-envelope | armpostgresqlflexibleservers:TuningOptions.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/flexibleservers/virtualendpoints | 1 | resource-group | item-write | armpostgresqlflexibleservers:VirtualEndpoints.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servergroupsv2/configurations | 1 | resource-group | arm-envelope | armcosmosforpostgresql:Configurations.ListByCluster, armpostgresqlhsc:Configurations.ListByCluster |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servergroupsv2/firewallrules | 1 | resource-group | item-write | armcosmosforpostgresql:FirewallRules.ListByCluster, armpostgresqlhsc:FirewallRules.ListByCluster |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servergroupsv2/privateendpointconnections | 1 | resource-group | item-write | armcosmosforpostgresql:PrivateEndpointConnections.ListByCluster, armpostgresqlhsc:PrivateEndpointConnections.ListByCluster |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servergroupsv2/privatelinkresources | 1 | resource-group | arm-envelope | armcosmosforpostgresql:PrivateLinkResources.ListByCluster, armpostgresqlhsc:PrivateLinkResources.ListByCluster |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servergroupsv2/roles | 1 | resource-group | item-write | armcosmosforpostgresql:Roles.ListByCluster, armpostgresqlhsc:Roles.ListByCluster |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servergroupsv2/servers | 1 | resource-group | arm-envelope | armcosmosforpostgresql:Servers.ListByCluster, armpostgresqlhsc:Servers.ListByCluster |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers | 0 | subscription | item-write | armpostgresql:Replicas.ListByServer, armpostgresql:Servers.List, armpostgresql:Servers.ListByResourceGroup |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/administrators | 1 | resource-group | item-write | armpostgresql:ServerAdministrators.List |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/configurations | 1 | resource-group | item-write | armpostgresql:Configurations.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/databases | 1 | resource-group | item-write | armpostgresql:Databases.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/firewallrules | 1 | resource-group | item-write | armpostgresql:FirewallRules.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/keys | 1 | resource-group | item-write | armpostgresql:ServerKeys.List |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/privateendpointconnections | 1 | resource-group | item-write | armpostgresql:PrivateEndpointConnections.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/privatelinkresources | 1 | resource-group | arm-envelope | armpostgresql:PrivateLinkResources.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/securityalertpolicies | 1 | resource-group | item-write | armpostgresql:ServerSecurityAlertPolicies.ListByServer |
| microsoft.dbforpostgresql | microsoft.dbforpostgresql/servers/virtualnetworkrules | 1 | resource-group | item-write | armpostgresql:VirtualNetworkRules.ListByServer |
| microsoft.delegatednetwork | microsoft.delegatednetwork/delegatedsubnets | 0 | subscription | item-write | armdelegatednetwork:DelegatedSubnetService.ListByResourceGroup, armdelegatednetwork:DelegatedSubnetService.ListBySubscription |
| microsoft.delegatednetwork | microsoft.delegatednetwork/orchestrators | 0 | subscription | item-write | armdelegatednetwork:OrchestratorInstanceService.ListByResourceGroup, armdelegatednetwork:OrchestratorInstanceService.ListBySubscription |
| microsoft.dependencymap | microsoft.dependencymap/maps/discoverysources | 1 | resource-group | item-write | armdependencymap:DiscoverySources.ListByMapsResource |
| microsoft.deploymentmanager | microsoft.deploymentmanager/artifactsources | 0 | resource-group | item-write | armdeploymentmanager:ArtifactSources.List |
| microsoft.deploymentmanager | microsoft.deploymentmanager/rollouts | 0 | resource-group | item-write | armdeploymentmanager:Rollouts.List |
| microsoft.deploymentmanager | microsoft.deploymentmanager/servicetopologies | 0 | resource-group | item-write | armdeploymentmanager:ServiceTopologies.List |
| microsoft.deploymentmanager | microsoft.deploymentmanager/servicetopologies/services | 1 | resource-group | item-write | armdeploymentmanager:Services.List |
| microsoft.deploymentmanager | microsoft.deploymentmanager/servicetopologies/services/serviceunits | 2 | resource-group | item-write | armdeploymentmanager:ServiceUnits.List |
| microsoft.deploymentmanager | microsoft.deploymentmanager/steps | 0 | resource-group | item-write | armdeploymentmanager:Steps.List |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/applicationgroups/applications | 1 | resource-group | item-write | armdesktopvirtualization:Applications.List |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/applicationgroups/desktops | 1 | resource-group | item-write | armdesktopvirtualization:Desktops.List |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/hostpools/msixpackages | 1 | resource-group | item-write | armdesktopvirtualization:MSIXPackages.List |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/hostpools/privateendpointconnections | 1 | resource-group | item-write | armdesktopvirtualization:PrivateEndpointConnections.ListByHostPool |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/hostpools/sessionhosts | 1 | resource-group | item-write | armdesktopvirtualization:SessionHosts.List |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/hostpools/sessionhosts/usersessions | 2 | resource-group | item-write | armdesktopvirtualization:UserSessions.List, armdesktopvirtualization:UserSessions.ListByHostPool |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/scalingplans/personalschedules | 1 | resource-group | item-write | armdesktopvirtualization:ScalingPlanPersonalSchedules.List |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/scalingplans/pooledschedules | 1 | resource-group | item-write | armdesktopvirtualization:ScalingPlanPooledSchedules.List |
| microsoft.desktopvirtualization | microsoft.desktopvirtualization/workspaces/privateendpointconnections | 1 | resource-group | item-write | armdesktopvirtualization:PrivateEndpointConnections.ListByWorkspace |
| microsoft.devcenter | microsoft.devcenter/devcenters/attachednetworks | 1 | resource-group | item-write | armdevcenter:AttachedNetworks.ListByDevCenter |
| microsoft.devcenter | microsoft.devcenter/devcenters/catalogs | 1 | resource-group | item-write | armdevcenter:Catalogs.ListByDevCenter |
| microsoft.devcenter | microsoft.devcenter/devcenters/catalogs/environmentdefinitions | 2 | resource-group | arm-envelope | armdevcenter:EnvironmentDefinitions.ListByCatalog |
| microsoft.devcenter | microsoft.devcenter/devcenters/catalogs/imagedefinitions | 2 | resource-group | arm-envelope | armdevcenter:CatalogImageDefinitions.ListByDevCenterCatalog |
| microsoft.devcenter | microsoft.devcenter/devcenters/catalogs/imagedefinitions/builds | 3 | resource-group | arm-envelope | armdevcenter:CatalogImageDefinitionBuilds.ListByImageDefinition |
| microsoft.devcenter | microsoft.devcenter/devcenters/catalogs/tasks | 2 | resource-group | arm-envelope | armdevcenter:CustomizationTasks.ListByCatalog |
| microsoft.devcenter | microsoft.devcenter/devcenters/devboxdefinitions | 1 | resource-group | item-write | armdevcenter:DevBoxDefinitions.ListByDevCenter |
| microsoft.devcenter | microsoft.devcenter/devcenters/encryptionsets | 1 | resource-group | item-write | armdevcenter:EncryptionSets.List |
| microsoft.devcenter | microsoft.devcenter/devcenters/environmenttypes | 1 | resource-group | item-write | armdevcenter:EnvironmentTypes.ListByDevCenter |
| microsoft.devcenter | microsoft.devcenter/devcenters/galleries | 1 | resource-group | item-write | armdevcenter:Galleries.ListByDevCenter |
| microsoft.devcenter | microsoft.devcenter/devcenters/galleries/images | 2 | resource-group | arm-envelope | armdevcenter:Images.ListByGallery |
| microsoft.devcenter | microsoft.devcenter/devcenters/galleries/images/versions | 3 | resource-group | arm-envelope | armdevcenter:ImageVersions.ListByImage |
| microsoft.devcenter | microsoft.devcenter/devcenters/projectpolicies | 1 | resource-group | item-write | armdevcenter:ProjectPolicies.ListByDevCenter |
| microsoft.devcenter | microsoft.devcenter/networkconnections/healthchecks | 1 | resource-group | arm-envelope | armdevcenter:NetworkConnections.ListHealthDetails |
| microsoft.devcenter | microsoft.devcenter/projects/allowedenvironmenttypes | 1 | resource-group | arm-envelope | armdevcenter:ProjectAllowedEnvironmentTypes.List |
| microsoft.devcenter | microsoft.devcenter/projects/attachednetworks | 1 | resource-group | arm-envelope | armdevcenter:AttachedNetworks.ListByProject |
| microsoft.devcenter | microsoft.devcenter/projects/catalogs | 1 | resource-group | item-write | armdevcenter:ProjectCatalogs.List |
| microsoft.devcenter | microsoft.devcenter/projects/catalogs/environmentdefinitions | 2 | resource-group | arm-envelope | armdevcenter:EnvironmentDefinitions.ListByProjectCatalog |
| microsoft.devcenter | microsoft.devcenter/projects/catalogs/imagedefinitions | 2 | resource-group | arm-envelope | armdevcenter:ProjectCatalogImageDefinitions.ListByProjectCatalog |
| microsoft.devcenter | microsoft.devcenter/projects/catalogs/imagedefinitions/builds | 3 | resource-group | arm-envelope | armdevcenter:ProjectCatalogImageDefinitionBuilds.ListByImageDefinition |
| microsoft.devcenter | microsoft.devcenter/projects/devboxdefinitions | 1 | resource-group | arm-envelope | armdevcenter:DevBoxDefinitions.ListByProject |
| microsoft.devcenter | microsoft.devcenter/projects/environmenttypes | 1 | resource-group | item-write | armdevcenter:ProjectEnvironmentTypes.List |
| microsoft.devcenter | microsoft.devcenter/projects/images | 1 | resource-group | arm-envelope | armdevcenter:Images.ListByProject |
| microsoft.devcenter | microsoft.devcenter/projects/images/versions | 2 | resource-group | arm-envelope | armdevcenter:ImageVersions.ListByProject |
| microsoft.devcenter | microsoft.devcenter/projects/pools | 1 | resource-group | item-write | armdevcenter:Pools.ListByProject |
| microsoft.devcenter | microsoft.devcenter/projects/pools/schedules | 2 | resource-group | item-write | armdevcenter:Schedules.ListByPool |
| microsoft.devhub | microsoft.devhub/iacprofiles | 0 | subscription | item-write | armdevhub:IacProfiles.List, armdevhub:IacProfiles.ListByResourceGroup |
| microsoft.devhub | microsoft.devhub/templates | 0 | subscription | arm-envelope | armdevhub:Template.List |
| microsoft.devhub | microsoft.devhub/templates/versions | 1 | subscription | arm-envelope | armdevhub:VersionedTemplate.List |
| microsoft.deviceregistry | microsoft.deviceregistry/namespaces | 0 | subscription | item-write | armdeviceregistry:Namespaces.ListByResourceGroup, armdeviceregistry:Namespaces.ListBySubscription |
| microsoft.deviceregistry | microsoft.deviceregistry/namespaces/assets | 1 | resource-group | item-write | armdeviceregistry:NamespaceAssets.ListByResourceGroup |
| microsoft.deviceregistry | microsoft.deviceregistry/namespaces/credentials | 1 | resource-group | item-write | armdeviceregistry:Credentials.ListByResourceGroup |
| microsoft.deviceregistry | microsoft.deviceregistry/namespaces/credentials/policies | 2 | resource-group | item-write | armdeviceregistry:Policies.ListByResourceGroup |
| microsoft.deviceregistry | microsoft.deviceregistry/namespaces/devices | 1 | resource-group | item-write | armdeviceregistry:NamespaceDevices.ListByResourceGroup |
| microsoft.deviceregistry | microsoft.deviceregistry/namespaces/discoveredassets | 1 | resource-group | item-write | armdeviceregistry:NamespaceDiscoveredAssets.ListByResourceGroup |
| microsoft.deviceregistry | microsoft.deviceregistry/namespaces/discovereddevices | 1 | resource-group | item-write | armdeviceregistry:NamespaceDiscoveredDevices.ListByResourceGroup |
| microsoft.deviceregistry | microsoft.deviceregistry/schemaregistries | 0 | subscription | item-write | armdeviceregistry:SchemaRegistries.ListByResourceGroup, armdeviceregistry:SchemaRegistries.ListBySubscription |
| microsoft.deviceregistry | microsoft.deviceregistry/schemaregistries/schemas | 1 | resource-group | item-write | armdeviceregistry:Schemas.ListBySchemaRegistry |
| microsoft.deviceregistry | microsoft.deviceregistry/schemaregistries/schemas/schemaversions | 2 | resource-group | item-write | armdeviceregistry:SchemaVersions.ListBySchema |
| microsoft.devices | microsoft.devices/iothubs/certificates | 1 | resource-group | item-write | armiothub:Certificates.ListByIotHub |
| microsoft.devices | microsoft.devices/iothubs/eventhubendpoints/consumergroups | 2 | resource-group | item-write | armiothub:Resource.ListEventHubConsumerGroups |
| microsoft.devices | microsoft.devices/iothubs/privateendpointconnections | 1 | resource-group | item-write | armiothub:PrivateEndpointConnections.List |
| microsoft.devices | microsoft.devices/iothubs/privatelinkresources | 1 | resource-group | arm-envelope | armiothub:PrivateLinkResources.List |
| microsoft.devices | microsoft.devices/provisioningservices/certificates | 1 | resource-group | item-write | armdeviceprovisioningservices:DpsCertificate.List |
| microsoft.devices | microsoft.devices/provisioningservices/privateendpointconnections | 1 | resource-group | item-write | armdeviceprovisioningservices:IotDpsResource.ListPrivateEndpointConnections |
| microsoft.devices | microsoft.devices/provisioningservices/privatelinkresources | 1 | resource-group | arm-envelope | armdeviceprovisioningservices:IotDpsResource.ListPrivateLinkResources |
| microsoft.deviceupdate | microsoft.deviceupdate/accounts/instances | 1 | resource-group | item-write | armdeviceupdate:Instances.ListByAccount |
| microsoft.deviceupdate | microsoft.deviceupdate/accounts/privateendpointconnectionproxies | 1 | resource-group | item-write | armdeviceupdate:PrivateEndpointConnectionProxies.ListByAccount |
| microsoft.deviceupdate | microsoft.deviceupdate/accounts/privateendpointconnections | 1 | resource-group | item-write | armdeviceupdate:PrivateEndpointConnections.ListByAccount |
| microsoft.deviceupdate | microsoft.deviceupdate/accounts/privatelinkresources | 1 | resource-group | arm-envelope | armdeviceupdate:PrivateLinkResources.ListByAccount |
| microsoft.devops | microsoft.devops/pipelines | 0 | subscription | item-write | armdevops:Pipelines.ListByResourceGroup, armdevops:Pipelines.ListBySubscription |
| microsoft.devtestlab | microsoft.devtestlab/labs/artifactsources | 1 | resource-group | item-write | armdevtestlabs:ArtifactSources.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/artifactsources/armtemplates | 2 | resource-group | arm-envelope | armdevtestlabs:ArmTemplates.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/artifactsources/artifacts | 2 | resource-group | arm-envelope | armdevtestlabs:Artifacts.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/customimages | 1 | resource-group | item-write | armdevtestlabs:CustomImages.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/formulas | 1 | resource-group | item-write | armdevtestlabs:Formulas.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/notificationchannels | 1 | resource-group | item-write | armdevtestlabs:NotificationChannels.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/policysets/policies | 2 | resource-group | item-write | armdevtestlabs:Policies.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/schedules | 1 | resource-group | item-write | armdevtestlabs:Schedules.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/users | 1 | resource-group | item-write | armdevtestlabs:Users.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/users/disks | 2 | resource-group | item-write | armdevtestlabs:Disks.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/users/environments | 2 | resource-group | item-write | armdevtestlabs:Environments.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/users/secrets | 2 | resource-group | item-write | armdevtestlabs:Secrets.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/users/servicefabrics | 2 | resource-group | item-write | armdevtestlabs:ServiceFabrics.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/users/servicefabrics/schedules | 3 | resource-group | item-write | armdevtestlabs:ServiceFabricSchedules.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/virtualmachines | 1 | resource-group | item-write | armdevtestlabs:VirtualMachines.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/virtualmachines/schedules | 2 | resource-group | item-write | armdevtestlabs:VirtualMachineSchedules.List |
| microsoft.devtestlab | microsoft.devtestlab/labs/virtualnetworks | 1 | resource-group | item-write | armdevtestlabs:VirtualNetworks.List |
| microsoft.digitaltwins | microsoft.digitaltwins/digitaltwinsinstances/endpoints | 1 | resource-group | item-write | armdigitaltwins:Endpoint.List |
| microsoft.digitaltwins | microsoft.digitaltwins/digitaltwinsinstances/privateendpointconnections | 1 | resource-group | item-write | armdigitaltwins:PrivateEndpointConnections.List |
| microsoft.digitaltwins | microsoft.digitaltwins/digitaltwinsinstances/privatelinkresources | 1 | resource-group | arm-envelope | armdigitaltwins:PrivateLinkResources.List |
| microsoft.digitaltwins | microsoft.digitaltwins/digitaltwinsinstances/timeseriesdatabaseconnections | 1 | resource-group | item-write | armdigitaltwins:TimeSeriesDatabaseConnections.List |
| microsoft.discovery | microsoft.discovery/bookshelves | 0 | subscription | item-write | armdiscovery:Bookshelves.ListByResourceGroup, armdiscovery:Bookshelves.ListBySubscription |
| microsoft.discovery | microsoft.discovery/bookshelves/privateendpointconnections | 1 | resource-group | item-write | armdiscovery:BookshelfPrivateEndpointConnections.ListByBookshelf |
| microsoft.discovery | microsoft.discovery/bookshelves/privatelinkresources | 1 | resource-group | arm-envelope | armdiscovery:BookshelfPrivateLinkResources.ListByBookshelf |
| microsoft.discovery | microsoft.discovery/storagecontainers | 0 | subscription | item-write | armdiscovery:StorageContainers.ListByResourceGroup, armdiscovery:StorageContainers.ListBySubscription |
| microsoft.discovery | microsoft.discovery/storagecontainers/storageassets | 1 | resource-group | item-write | armdiscovery:StorageAssets.ListByStorageContainer |
| microsoft.discovery | microsoft.discovery/supercomputers | 0 | subscription | item-write | armdiscovery:Supercomputers.ListByResourceGroup, armdiscovery:Supercomputers.ListBySubscription |
| microsoft.discovery | microsoft.discovery/supercomputers/nodepools | 1 | resource-group | item-write | armdiscovery:NodePools.ListBySupercomputer |
| microsoft.discovery | microsoft.discovery/tools | 0 | subscription | item-write | armdiscovery:Tools.ListByResourceGroup, armdiscovery:Tools.ListBySubscription |
| microsoft.discovery | microsoft.discovery/workspaces | 0 | subscription | item-write | armdiscovery:Workspaces.ListByResourceGroup, armdiscovery:Workspaces.ListBySubscription |
| microsoft.discovery | microsoft.discovery/workspaces/chatmodeldeployments | 1 | resource-group | item-write | armdiscovery:ChatModelDeployments.ListByWorkspace |
| microsoft.discovery | microsoft.discovery/workspaces/privateendpointconnections | 1 | resource-group | item-write | armdiscovery:WorkspacePrivateEndpointConnections.ListByWorkspace |
| microsoft.discovery | microsoft.discovery/workspaces/privatelinkresources | 1 | resource-group | arm-envelope | armdiscovery:WorkspacePrivateLinkResources.ListByWorkspace |
| microsoft.discovery | microsoft.discovery/workspaces/projects | 1 | resource-group | item-write | armdiscovery:Projects.ListByWorkspace |
| microsoft.documentdb | microsoft.documentdb/cassandraclusters/datacenters | 1 | resource-group | item-write | armcosmos:CassandraDataCenters.List |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/cassandrakeyspaces | 1 | resource-group | item-write | armcosmos:CassandraResources.ListCassandraKeyspaces |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/cassandrakeyspaces/tables | 2 | resource-group | item-write | armcosmos:CassandraResources.ListCassandraTables |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/cassandraroleassignments | 1 | resource-group | item-write | armcosmos:CassandraResources.ListCassandraRoleAssignments |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/cassandraroledefinitions | 1 | resource-group | item-write | armcosmos:CassandraResources.ListCassandraRoleDefinitions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/gremlindatabases | 1 | resource-group | item-write | armcosmos:GremlinResources.ListGremlinDatabases |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/gremlindatabases/graphs | 2 | resource-group | item-write | armcosmos:GremlinResources.ListGremlinGraphs |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/gremlinroleassignments | 1 | resource-group | item-write | armcosmos:GremlinResources.ListGremlinRoleAssignments |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/gremlinroledefinitions | 1 | resource-group | item-write | armcosmos:GremlinResources.ListGremlinRoleDefinitions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/mongodbdatabases | 1 | resource-group | item-write | armcosmos:MongoDBResources.ListMongoDBDatabases |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/mongodbdatabases/collections | 2 | resource-group | item-write | armcosmos:MongoDBResources.ListMongoDBCollections |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/mongodbroledefinitions | 1 | resource-group | item-write | armcosmos:MongoDBResources.ListMongoRoleDefinitions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/mongodbuserdefinitions | 1 | resource-group | item-write | armcosmos:MongoDBResources.ListMongoUserDefinitions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/mongomiroleassignments | 1 | resource-group | item-write | armcosmos:MongoMIResources.ListMongoMIRoleAssignments |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/mongomiroledefinitions | 1 | resource-group | item-write | armcosmos:MongoMIResources.ListMongoMIRoleDefinitions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/notebookworkspaces | 1 | resource-group | item-write | armcosmos:NotebookWorkspaces.ListByDatabaseAccount |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/privateendpointconnections | 1 | resource-group | item-write | armcosmos:PrivateEndpointConnections.ListByDatabaseAccount |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/privatelinkresources | 1 | resource-group | arm-envelope | armcosmos:PrivateLinkResources.ListByDatabaseAccount |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/services | 1 | resource-group | item-write | armcosmos:Service.List |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqldatabases | 1 | resource-group | item-write | armcosmos:SQLResources.ListSQLDatabases |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqldatabases/clientencryptionkeys | 2 | resource-group | item-write | armcosmos:SQLResources.ListClientEncryptionKeys |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqldatabases/containers | 2 | resource-group | item-write | armcosmos:SQLResources.ListSQLContainers |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqldatabases/containers/storedprocedures | 3 | resource-group | item-write | armcosmos:SQLResources.ListSQLStoredProcedures |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqldatabases/containers/triggers | 3 | resource-group | item-write | armcosmos:SQLResources.ListSQLTriggers |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqldatabases/containers/userdefinedfunctions | 3 | resource-group | item-write | armcosmos:SQLResources.ListSQLUserDefinedFunctions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqlroleassignments | 1 | resource-group | item-write | armcosmos:SQLResources.ListSQLRoleAssignments |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/sqlroledefinitions | 1 | resource-group | item-write | armcosmos:SQLResources.ListSQLRoleDefinitions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/tableroleassignments | 1 | resource-group | item-write | armcosmos:TableResources.ListTableRoleAssignments |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/tableroledefinitions | 1 | resource-group | item-write | armcosmos:TableResources.ListTableRoleDefinitions |
| microsoft.documentdb | microsoft.documentdb/databaseaccounts/tables | 1 | resource-group | item-write | armcosmos:TableResources.ListTables |
| microsoft.documentdb | microsoft.documentdb/fleets | 0 | subscription | item-write | armcosmos:Fleet.List, armcosmos:Fleet.ListByResourceGroup |
| microsoft.documentdb | microsoft.documentdb/fleets/fleetspaces | 1 | resource-group | item-write | armcosmos:Fleetspace.List |
| microsoft.documentdb | microsoft.documentdb/fleets/fleetspaces/fleetspaceaccounts | 2 | resource-group | item-write | armcosmos:FleetspaceAccount.List |
| microsoft.documentdb | microsoft.documentdb/locations | 0 | subscription | arm-envelope | armcosmos:Locations.List |
| microsoft.documentdb | microsoft.documentdb/mongoclusters/firewallrules | 1 | resource-group | item-write | armmongocluster:FirewallRules.ListByMongoCluster |
| microsoft.documentdb | microsoft.documentdb/mongoclusters/privateendpointconnections | 1 | resource-group | item-write | armmongocluster:PrivateEndpointConnections.ListByMongoCluster |
| microsoft.documentdb | microsoft.documentdb/mongoclusters/users | 1 | resource-group | item-write | armmongocluster:Users.ListByMongoCluster |
| microsoft.domainregistration | microsoft.domainregistration/domains/domainownershipidentifiers | 1 | resource-group | item-write | armdomainregistration:Domains.ListOwnershipIdentifiers |
| microsoft.domainregistration | microsoft.domainregistration/topleveldomains | 0 | subscription | arm-envelope | armdomainregistration:TopLevelDomains.List |
| microsoft.durabletask | microsoft.durabletask/schedulers/privateendpointconnections | 1 | resource-group | item-write | armdurabletask:Schedulers.ListPrivateEndpointConnections |
| microsoft.durabletask | microsoft.durabletask/schedulers/privatelinkresources | 1 | resource-group | arm-envelope | armdurabletask:Schedulers.ListPrivateLinks |
| microsoft.durabletask | microsoft.durabletask/schedulers/retentionpolicies | 1 | resource-group | item-write | armdurabletask:RetentionPolicies.ListByScheduler |
| microsoft.durabletask | microsoft.durabletask/schedulers/taskhubs | 1 | resource-group | item-write | armdurabletask:TaskHubs.ListByScheduler |
| microsoft.edge | microsoft.edge/configtemplates | 0 | subscription | item-write | armworkloadorchestration:ConfigTemplates.ListByResourceGroup, armworkloadorchestration:ConfigTemplates.ListBySubscription |
| microsoft.edge | microsoft.edge/configtemplates/versions | 1 | resource-group | arm-envelope | armworkloadorchestration:ConfigTemplateVersions.ListByConfigTemplate |
| microsoft.edge | microsoft.edge/contexts | 0 | subscription | item-write | armworkloadorchestration:Contexts.ListByResourceGroup, armworkloadorchestration:Contexts.ListBySubscription |
| microsoft.edge | microsoft.edge/contexts/sitereferences | 1 | resource-group | item-write | armworkloadorchestration:SiteReferences.ListByContext |
| microsoft.edge | microsoft.edge/contexts/workflows | 1 | resource-group | item-write | armworkloadorchestration:Workflows.ListByContext |
| microsoft.edge | microsoft.edge/contexts/workflows/versions | 2 | resource-group | item-write | armworkloadorchestration:WorkflowVersions.ListByWorkflow |
| microsoft.edge | microsoft.edge/contexts/workflows/versions/executions | 3 | resource-group | item-write | armworkloadorchestration:Executions.ListByWorkflowVersion |
| microsoft.edge | microsoft.edge/diagnostics | 0 | subscription | item-write | armworkloadorchestration:Diagnostics.ListByResourceGroup, armworkloadorchestration:Diagnostics.ListBySubscription |
| microsoft.edge | microsoft.edge/disconnectedoperations | 0 | subscription | item-write | armdisconnectedoperations:Client.ListByResourceGroup, armdisconnectedoperations:Client.ListBySubscription |
| microsoft.edge | microsoft.edge/disconnectedoperations/hardwaresettings | 1 | resource-group | item-write | armdisconnectedoperations:HardwareSettings.ListByParent |
| microsoft.edge | microsoft.edge/disconnectedoperations/images | 1 | resource-group | arm-envelope | armdisconnectedoperations:Images.ListByDisconnectedOperation |
| microsoft.edge | microsoft.edge/disconnectedoperations/images/artifacts | 2 | resource-group | arm-envelope | armdisconnectedoperations:Artifacts.ListByParent |
| microsoft.edge | microsoft.edge/schemareferences | 0 | extension | item-write | armworkloadorchestration:SchemaReferences.ListByResourceGroup |
| microsoft.edge | microsoft.edge/schemas | 0 | subscription | item-write | armworkloadorchestration:Schemas.ListByResourceGroup, armworkloadorchestration:Schemas.ListBySubscription |
| microsoft.edge | microsoft.edge/schemas/dynamicschemas | 1 | resource-group | item-write | armworkloadorchestration:DynamicSchemas.ListBySchema |
| microsoft.edge | microsoft.edge/schemas/dynamicschemas/versions | 2 | resource-group | item-write | armworkloadorchestration:DynamicSchemaVersions.ListByDynamicSchema |
| microsoft.edge | microsoft.edge/schemas/versions | 1 | resource-group | item-write | armworkloadorchestration:SchemaVersions.ListBySchema |
| microsoft.edge | microsoft.edge/sites | 0 | tenant | item-write | armsitemanager:Sites.ListByResourceGroup, armsitemanager:SitesByServiceGroup.ListByServiceGroup, armsitemanager:SitesBySubscription.List |
| microsoft.edge | microsoft.edge/solutiontemplates | 0 | subscription | item-write | armworkloadorchestration:SolutionTemplates.ListByResourceGroup, armworkloadorchestration:SolutionTemplates.ListBySubscription |
| microsoft.edge | microsoft.edge/solutiontemplates/versions | 1 | resource-group | arm-envelope | armworkloadorchestration:SolutionTemplateVersions.ListBySolutionTemplate |
| microsoft.edge | microsoft.edge/targets | 0 | subscription | item-write | armworkloadorchestration:Targets.ListByResourceGroup, armworkloadorchestration:Targets.ListBySubscription |
| microsoft.edge | microsoft.edge/targets/solutions | 1 | resource-group | item-write | armworkloadorchestration:Solutions.ListByTarget |
| microsoft.edge | microsoft.edge/targets/solutions/instances | 2 | resource-group | item-write | armworkloadorchestration:Instances.ListBySolution |
| microsoft.edge | microsoft.edge/targets/solutions/instances/histories | 3 | resource-group | arm-envelope | armworkloadorchestration:InstanceHistories.ListByInstance |
| microsoft.edge | microsoft.edge/targets/solutions/versions | 2 | resource-group | item-write | armworkloadorchestration:SolutionVersions.ListBySolution |
| microsoft.education | microsoft.education/labs | 0 | tenant | item-write | armeducation:Labs.List, armeducation:Labs.ListAll |
| microsoft.education | microsoft.education/labs/joinrequests | 1 | tenant | arm-envelope | armeducation:JoinRequests.List |
| microsoft.education | microsoft.education/labs/students | 1 | tenant | item-write | armeducation:Students.List |
| microsoft.elastic | microsoft.elastic/monitors/monitoredsubscriptions | 1 | resource-group | item-write | armelastic:MonitoredSubscriptions.List |
| microsoft.elastic | microsoft.elastic/monitors/openaiintegrations | 1 | resource-group | item-write | armelastic:OpenAI.List |
| microsoft.elastic | microsoft.elastic/monitors/tagrules | 1 | resource-group | item-write | armelastic:TagRules.List |
| microsoft.elasticsan | microsoft.elasticsan/elasticsans/privateendpointconnections | 1 | resource-group | item-write | armelasticsan:PrivateEndpointConnections.List |
| microsoft.elasticsan | microsoft.elasticsan/elasticsans/volumegroups | 1 | resource-group | item-write | armelasticsan:VolumeGroups.ListByElasticSan |
| microsoft.elasticsan | microsoft.elasticsan/elasticsans/volumegroups/snapshots | 2 | resource-group | item-write | armelasticsan:VolumeSnapshots.ListByVolumeGroup |
| microsoft.elasticsan | microsoft.elasticsan/elasticsans/volumegroups/volumes | 2 | resource-group | item-write | armelasticsan:Volumes.ListByVolumeGroup |
| microsoft.engagementfabric | microsoft.engagementfabric/accounts | 0 | subscription | item-write | armengagementfabric:Accounts.List, armengagementfabric:Accounts.ListByResourceGroup |
| microsoft.engagementfabric | microsoft.engagementfabric/accounts/channels | 1 | resource-group | item-write | armengagementfabric:Channels.ListByAccount |
| microsoft.eventgrid | microsoft.eventgrid/domains/eventsubscriptions | 1 | resource-group | item-write | armeventgrid:DomainEventSubscriptions.List, armeventgrid:EventSubscriptions.ListGlobalByResourceGroupForTopicType, armeventgrid:EventSubscriptions.ListGlobalBySubscriptionForTopicType, armeventgrid:EventSubscriptions.ListRegionalByResourceGroupForTopicType, armeventgrid:EventSubscriptions.ListRegionalBySubscriptionForTopicType |
| microsoft.eventgrid | microsoft.eventgrid/domains/topics | 1 | resource-group | item-write | armeventgrid:DomainTopics.ListByDomain |
| microsoft.eventgrid | microsoft.eventgrid/domains/topics/eventsubscriptions | 2 | resource-group | item-write | armeventgrid:DomainTopicEventSubscriptions.List |
| microsoft.eventgrid | microsoft.eventgrid/namespaces/cacertificates | 1 | resource-group | item-write | armeventgrid:CaCertificates.ListByNamespace |
| microsoft.eventgrid | microsoft.eventgrid/namespaces/clientgroups | 1 | resource-group | item-write | armeventgrid:ClientGroups.ListByNamespace |
| microsoft.eventgrid | microsoft.eventgrid/namespaces/clients | 1 | resource-group | item-write | armeventgrid:Clients.ListByNamespace |
| microsoft.eventgrid | microsoft.eventgrid/namespaces/permissionbindings | 1 | resource-group | item-write | armeventgrid:PermissionBindings.ListByNamespace |
| microsoft.eventgrid | microsoft.eventgrid/namespaces/topics | 1 | resource-group | item-write | armeventgrid:NamespaceTopics.ListByNamespace |
| microsoft.eventgrid | microsoft.eventgrid/namespaces/topics/eventsubscriptions | 2 | resource-group | item-write | armeventgrid:NamespaceTopicEventSubscriptions.ListByNamespaceTopic |
| microsoft.eventgrid | microsoft.eventgrid/namespaces/topicspaces | 1 | resource-group | item-write | armeventgrid:TopicSpaces.ListByNamespace |
| microsoft.eventgrid | microsoft.eventgrid/networksecurityperimeterconfigurations | 0 | resource-group | arm-envelope | armeventgrid:NetworkSecurityPerimeterConfigurations.List |
| microsoft.eventgrid | microsoft.eventgrid/partnerdestinations | 0 | subscription | item-write | armeventgrid:PartnerDestinations.ListByResourceGroup, armeventgrid:PartnerDestinations.ListBySubscription |
| microsoft.eventgrid | microsoft.eventgrid/partnernamespaces/channels | 1 | resource-group | item-write | armeventgrid:Channels.ListByPartnerNamespace |
| microsoft.eventgrid | microsoft.eventgrid/partnertopics/eventsubscriptions | 1 | resource-group | item-write | armeventgrid:PartnerTopicEventSubscriptions.ListByPartnerTopic |
| microsoft.eventgrid | microsoft.eventgrid/privateendpointconnections | 0 | resource-group | item-write | armeventgrid:PrivateEndpointConnections.ListByResource |
| microsoft.eventgrid | microsoft.eventgrid/privatelinkresources | 0 | resource-group | arm-envelope | armeventgrid:PrivateLinkResources.ListByResource |
| microsoft.eventgrid | microsoft.eventgrid/systemtopics/eventsubscriptions | 1 | resource-group | item-write | armeventgrid:SystemTopicEventSubscriptions.ListBySystemTopic |
| microsoft.eventgrid | microsoft.eventgrid/topics/eventsubscriptions | 1 | resource-group | item-write | armeventgrid:TopicEventSubscriptions.List |
| microsoft.eventhub | microsoft.eventhub/namespaces/applicationgroups | 1 | resource-group | item-write | armeventhub:ApplicationGroup.ListByNamespace |
| microsoft.eventhub | microsoft.eventhub/namespaces/authorizationrules | 1 | resource-group | item-write | armeventhub:Namespaces.ListAuthorizationRules |
| microsoft.eventhub | microsoft.eventhub/namespaces/disasterrecoveryconfigs | 1 | resource-group | item-write | armeventhub:DisasterRecoveryConfigs.List |
| microsoft.eventhub | microsoft.eventhub/namespaces/disasterrecoveryconfigs/authorizationrules | 2 | resource-group | arm-envelope | armeventhub:DisasterRecoveryConfigs.ListAuthorizationRules |
| microsoft.eventhub | microsoft.eventhub/namespaces/eventhubs | 1 | resource-group | item-write | armeventhub:EventHubs.ListByNamespace |
| microsoft.eventhub | microsoft.eventhub/namespaces/eventhubs/authorizationrules | 2 | resource-group | item-write | armeventhub:EventHubs.ListAuthorizationRules |
| microsoft.eventhub | microsoft.eventhub/namespaces/eventhubs/consumergroups | 2 | resource-group | item-write | armeventhub:ConsumerGroups.ListByEventHub |
| microsoft.eventhub | microsoft.eventhub/namespaces/networkrulesets | 1 | resource-group | item-write | armeventhub:Namespaces.ListNetworkRuleSet |
| microsoft.eventhub | microsoft.eventhub/namespaces/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armeventhub:NetworkSecurityPerimeterConfiguration.List |
| microsoft.eventhub | microsoft.eventhub/namespaces/privateendpointconnections | 1 | resource-group | item-write | armeventhub:PrivateEndpointConnections.List |
| microsoft.eventhub | microsoft.eventhub/namespaces/schemagroups | 1 | resource-group | item-write | armeventhub:SchemaRegistry.ListByNamespace |
| microsoft.extendedlocation | microsoft.extendedlocation/customlocations/resourcesyncrules | 1 | resource-group | item-write | armextendedlocation:ResourceSyncRules.ListByCustomLocationID |
| microsoft.features | microsoft.features/featureproviders/subscriptionfeatureregistrations | 1 | subscription | item-write | armfeatures:SubscriptionFeatureRegistrations.ListAllBySubscription, armfeatures:SubscriptionFeatureRegistrations.ListBySubscription |
| microsoft.features | microsoft.features/features | 0 | subscription | arm-envelope | armfeatures:Client.List, armfeatures:Client.ListAll |
| microsoft.fileshares | microsoft.fileshares/fileshares/filesharesnapshots | 1 | resource-group | item-write | armfileshares:FileShareSnapshots.ListByFileShare |
| microsoft.fileshares | microsoft.fileshares/fileshares/privateendpointconnections | 1 | resource-group | item-write | armfileshares:PrivateEndpointConnections.ListByFileShare |
| microsoft.fileshares | microsoft.fileshares/fileshares/privatelinkresources | 1 | resource-group | arm-envelope | armfileshares:PrivateLinkResources.List |
| microsoft.fluidrelay | microsoft.fluidrelay/fluidrelayservers/fluidrelaycontainers | 1 | resource-group | item-write | armfluidrelay:Containers.ListByFluidRelayServers |
| microsoft.guestconfiguration | microsoft.guestconfiguration/guestconfigurationassignments | 0 | subscription | item-write | armguestconfiguration:Assignments.List, armguestconfiguration:Assignments.RGList, armguestconfiguration:Assignments.SubscriptionList, armguestconfiguration:AssignmentsVMSS.List, armguestconfiguration:ConnectedVMwarevSphereAssignments.List, armguestconfiguration:HCRPAssignments.List |
| microsoft.hanaonazure | microsoft.hanaonazure/sapmonitors | 0 | subscription | item-write | armhanaonazure:SapMonitors.List |
| microsoft.hanaonazure | microsoft.hanaonazure/sapmonitors/providerinstances | 1 | resource-group | item-write | armhanaonazure:ProviderInstances.List |
| microsoft.hardwaresecuritymodules | microsoft.hardwaresecuritymodules/cloudhsmclusters | 0 | subscription | item-write | armhardwaresecuritymodules:CloudHsmClusters.ListByResourceGroup, armhardwaresecuritymodules:CloudHsmClusters.ListBySubscription |
| microsoft.hardwaresecuritymodules | microsoft.hardwaresecuritymodules/cloudhsmclusters/privateendpointconnections | 1 | resource-group | item-write | armhardwaresecuritymodules:PrivateEndpointConnections.ListByCloudHsmCluster |
| microsoft.hardwaresecuritymodules | microsoft.hardwaresecuritymodules/paymenthsmclusters | 0 | subscription | item-write | armhardwaresecuritymodules:PaymentHsmClusters.ListByResourceGroup, armhardwaresecuritymodules:PaymentHsmClusters.ListBySubscription |
| microsoft.hardwaresecuritymodules | microsoft.hardwaresecuritymodules/paymenthsmclusters/privateendpointconnections | 1 | resource-group | item-write | armhardwaresecuritymodules:PaymentHsmClusterPrivateEndpointConnections.ListByPaymentHsmCluster |
| microsoft.hdinsight | microsoft.hdinsight/clusters/applications | 1 | resource-group | item-write | armhdinsight:Applications.ListByCluster |
| microsoft.hdinsight | microsoft.hdinsight/clusters/privateendpointconnections | 1 | resource-group | item-write | armhdinsight:PrivateEndpointConnections.ListByCluster |
| microsoft.hdinsight | microsoft.hdinsight/clusters/privatelinkresources | 1 | resource-group | arm-envelope | armhdinsight:PrivateLinkResources.ListByCluster |
| microsoft.hdinsight | microsoft.hdinsight/clusters/scriptactions | 1 | resource-group | item-write | armhdinsight:ScriptActions.ListByCluster |
| microsoft.healthcareapis | microsoft.healthcareapis/services/privateendpointconnections | 1 | resource-group | item-write | armhealthcareapis:PrivateEndpointConnections.ListByService |
| microsoft.healthcareapis | microsoft.healthcareapis/services/privatelinkresources | 1 | resource-group | arm-envelope | armhealthcareapis:PrivateLinkResources.ListByService |
| microsoft.healthcareapis | microsoft.healthcareapis/workspaces/dicomservices | 1 | resource-group | item-write | armhealthcareapis:DicomServices.ListByWorkspace |
| microsoft.healthcareapis | microsoft.healthcareapis/workspaces/fhirservices | 1 | resource-group | item-write | armhealthcareapis:FhirServices.ListByWorkspace |
| microsoft.healthcareapis | microsoft.healthcareapis/workspaces/iotconnectors | 1 | resource-group | item-write | armhealthcareapis:IotConnectors.ListByWorkspace |
| microsoft.healthcareapis | microsoft.healthcareapis/workspaces/iotconnectors/fhirdestinations | 2 | resource-group | item-write | armhealthcareapis:FhirDestinations.ListByIotConnector |
| microsoft.healthcareapis | microsoft.healthcareapis/workspaces/privateendpointconnections | 1 | resource-group | item-write | armhealthcareapis:WorkspacePrivateEndpointConnections.ListByWorkspace |
| microsoft.healthcareapis | microsoft.healthcareapis/workspaces/privatelinkresources | 1 | resource-group | arm-envelope | armhealthcareapis:WorkspacePrivateLinkResources.ListByWorkspace |
| microsoft.healthdataaiservices | microsoft.healthdataaiservices/deidservices/privateendpointconnections | 1 | resource-group | item-write | armhealthdataaiservices:PrivateEndpointConnections.ListByDeidService |
| microsoft.horizondb | microsoft.horizondb/clusters/administrators | 1 | resource-group | item-write | armhorizondb:Administrators.List |
| microsoft.horizondb | microsoft.horizondb/clusters/pools | 1 | resource-group | arm-envelope | armhorizondb:Pools.List |
| microsoft.horizondb | microsoft.horizondb/clusters/pools/firewallrules | 2 | resource-group | item-write | armhorizondb:FirewallRules.List |
| microsoft.horizondb | microsoft.horizondb/clusters/pools/replicas | 2 | resource-group | item-write | armhorizondb:Replicas.List |
| microsoft.horizondb | microsoft.horizondb/clusters/privateendpointconnections | 1 | resource-group | item-write | armhorizondb:PrivateEndpointConnections.List |
| microsoft.horizondb | microsoft.horizondb/clusters/privatelinkresources | 1 | resource-group | arm-envelope | armhorizondb:PrivateLinkResources.List |
| microsoft.hybridcompute | microsoft.hybridcompute/gateways | 0 | subscription | item-write | armhybridcompute:Gateways.ListByResourceGroup, armhybridcompute:Gateways.ListBySubscription |
| microsoft.hybridcompute | microsoft.hybridcompute/licenses | 0 | subscription | item-write | armhybridcompute:Licenses.ListByResourceGroup, armhybridcompute:Licenses.ListBySubscription |
| microsoft.hybridcompute | microsoft.hybridcompute/machines/extensions | 1 | resource-group | item-write | armhybridcompute:MachineExtensions.List |
| microsoft.hybridcompute | microsoft.hybridcompute/machines/licenseprofiles | 1 | resource-group | item-write | armhybridcompute:LicenseProfiles.List |
| microsoft.hybridcompute | microsoft.hybridcompute/machines/runcommands | 1 | resource-group | item-write | armhybridcompute:MachineRunCommands.List |
| microsoft.hybridcompute | microsoft.hybridcompute/privatelinkscopes/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armhybridcompute:NetworkSecurityPerimeterConfigurations.ListByPrivateLinkScope |
| microsoft.hybridcompute | microsoft.hybridcompute/privatelinkscopes/privateendpointconnections | 1 | resource-group | item-write | armhybridcompute:PrivateEndpointConnections.ListByPrivateLinkScope |
| microsoft.hybridcompute | microsoft.hybridcompute/privatelinkscopes/privatelinkresources | 1 | resource-group | arm-envelope | armhybridcompute:PrivateLinkResources.ListByPrivateLinkScope |
| microsoft.hybridconnectivity | microsoft.hybridconnectivity/endpoints | 0 | extension | item-write | armhybridconnectivity:Endpoints.List |
| microsoft.hybridconnectivity | microsoft.hybridconnectivity/endpoints/serviceconfigurations | 1 | extension | item-write | armhybridconnectivity:ServiceConfigurations.ListByEndpointResource |
| microsoft.hybridconnectivity | microsoft.hybridconnectivity/solutionconfigurations | 0 | extension | item-write | armhybridconnectivity:SolutionConfigurations.List |
| microsoft.hybridconnectivity | microsoft.hybridconnectivity/solutionconfigurations/inventory | 1 | extension | arm-envelope | armhybridconnectivity:Inventory.ListBySolutionConfiguration |
| microsoft.hybridconnectivity | microsoft.hybridconnectivity/solutiontypes | 0 | subscription | arm-envelope | armhybridconnectivity:SolutionTypes.ListByResourceGroup, armhybridconnectivity:SolutionTypes.ListBySubscription |
| microsoft.hybridcontainerservice | microsoft.hybridcontainerservice/kubernetesversions | 0 | extension | item-write | armhybridcontainerservice:KubernetesVersions.List |
| microsoft.hybridcontainerservice | microsoft.hybridcontainerservice/provisionedclusterinstances | 0 | extension | item-write | armhybridcontainerservice:ProvisionedClusterInstances.List |
| microsoft.hybridcontainerservice | microsoft.hybridcontainerservice/provisionedclusterinstances/agentpools | 1 | extension | item-write | armhybridcontainerservice:AgentPool.ListByProvisionedCluster |
| microsoft.hybridcontainerservice | microsoft.hybridcontainerservice/provisionedclusterinstances/hybrididentitymetadata | 1 | extension | item-write | armhybridcontainerservice:HybridIdentityMetadata.ListByCluster |
| microsoft.hybridcontainerservice | microsoft.hybridcontainerservice/skus | 0 | extension | item-write | armhybridcontainerservice:VMSKUs.List |
| microsoft.hybriddata | microsoft.hybriddata/datamanagers | 0 | subscription | item-write | armhybriddatamanager:DataManagers.List, armhybriddatamanager:DataManagers.ListByResourceGroup |
| microsoft.hybriddata | microsoft.hybriddata/datamanagers/dataservices | 1 | resource-group | arm-envelope | armhybriddatamanager:DataServices.ListByDataManager |
| microsoft.hybriddata | microsoft.hybriddata/datamanagers/dataservices/jobdefinitions | 2 | resource-group | item-write | armhybriddatamanager:JobDefinitions.ListByDataManager, armhybriddatamanager:JobDefinitions.ListByDataService |
| microsoft.hybriddata | microsoft.hybriddata/datamanagers/dataservices/jobdefinitions/jobs | 3 | resource-group | arm-envelope | armhybriddatamanager:Jobs.ListByJobDefinition |
| microsoft.hybriddata | microsoft.hybriddata/datamanagers/datastores | 1 | resource-group | item-write | armhybriddatamanager:DataStores.ListByDataManager |
| microsoft.hybriddata | microsoft.hybriddata/datamanagers/datastoretypes | 1 | resource-group | arm-envelope | armhybriddatamanager:DataStoreTypes.ListByDataManager |
| microsoft.hybriddata | microsoft.hybriddata/datamanagers/publickeys | 1 | resource-group | arm-envelope | armhybriddatamanager:PublicKeys.ListByDataManager |
| microsoft.hybridnetwork | microsoft.hybridnetwork/configurationgroupvalues | 0 | subscription | item-write | armhybridnetwork:ConfigurationGroupValues.ListByResourceGroup, armhybridnetwork:ConfigurationGroupValues.ListBySubscription |
| microsoft.hybridnetwork | microsoft.hybridnetwork/networkfunctions/components | 1 | resource-group | arm-envelope | armhybridnetwork:Components.ListByNetworkFunction |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers | 0 | subscription | item-write | armhybridnetwork:Publishers.ListByResourceGroup, armhybridnetwork:Publishers.ListBySubscription |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/artifactstores | 1 | resource-group | item-write | armhybridnetwork:ArtifactStores.ListByPublisher |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/artifactstores/artifactmanifests | 2 | resource-group | item-write | armhybridnetwork:ArtifactManifests.ListByArtifactStore |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/artifactstores/artifactversions | 2 | resource-group | item-write | armhybridnetwork:ProxyArtifact.Get |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/configurationgroupschemas | 1 | resource-group | item-write | armhybridnetwork:ConfigurationGroupSchemas.ListByPublisher |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/networkfunctiondefinitiongroups | 1 | resource-group | item-write | armhybridnetwork:NetworkFunctionDefinitionGroups.ListByPublisher |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/networkfunctiondefinitiongroups/networkfunctiondefinitionversions | 2 | resource-group | item-write | armhybridnetwork:NetworkFunctionDefinitionVersions.ListByNetworkFunctionDefinitionGroup |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/networkservicedesigngroups | 1 | resource-group | item-write | armhybridnetwork:NetworkServiceDesignGroups.ListByPublisher |
| microsoft.hybridnetwork | microsoft.hybridnetwork/publishers/networkservicedesigngroups/networkservicedesignversions | 2 | resource-group | item-write | armhybridnetwork:NetworkServiceDesignVersions.ListByNetworkServiceDesignGroup |
| microsoft.hybridnetwork | microsoft.hybridnetwork/sitenetworkservices | 0 | subscription | item-write | armhybridnetwork:SiteNetworkServices.ListByResourceGroup, armhybridnetwork:SiteNetworkServices.ListBySubscription |
| microsoft.hybridnetwork | microsoft.hybridnetwork/sites | 0 | subscription | item-write | armhybridnetwork:Sites.ListByResourceGroup, armhybridnetwork:Sites.ListBySubscription |
| microsoft.impact | microsoft.impact/connectors | 0 | subscription | item-write | armimpactreporting:Connectors.ListBySubscription |
| microsoft.impact | microsoft.impact/impactcategories | 0 | subscription | arm-envelope | armimpactreporting:ImpactCategories.ListBySubscription |
| microsoft.impact | microsoft.impact/workloadimpacts | 0 | subscription | item-write | armimpactreporting:WorkloadImpacts.ListBySubscription |
| microsoft.impact | microsoft.impact/workloadimpacts/insights | 1 | subscription | item-write | armimpactreporting:Insights.ListBySubscription |
| microsoft.importexport | microsoft.importexport/jobs | 0 | subscription | item-write | armstorageimportexport:Jobs.ListByResourceGroup, armstorageimportexport:Jobs.ListBySubscription |
| microsoft.insights | microsoft.insights/actiongroups | 0 | subscription | item-write | armmonitor:ActionGroups.ListByResourceGroup, armmonitor:ActionGroups.ListBySubscriptionID |
| microsoft.insights | microsoft.insights/actiongroups/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armmonitor:ActionGroups.ListNSP |
| microsoft.insights | microsoft.insights/activitylogalerts | 0 | subscription | item-write | armmonitor:ActivityLogAlerts.ListByResourceGroup, armmonitor:ActivityLogAlerts.ListBySubscriptionID |
| microsoft.insights | microsoft.insights/autoscalesettings | 0 | subscription | item-write | armmonitor:AutoscaleSettings.ListByResourceGroup, armmonitor:AutoscaleSettings.ListBySubscription |
| microsoft.insights | microsoft.insights/components | 0 | subscription | item-write | armapplicationinsights:Components.List, armapplicationinsights:Components.ListByResourceGroup |
| microsoft.insights | microsoft.insights/components/annotations | 1 | resource-group | item-write | armapplicationinsights:Annotations.List |
| microsoft.insights | microsoft.insights/components/apikeys | 1 | resource-group | item-write | armapplicationinsights:APIKeys.List |
| microsoft.insights | microsoft.insights/components/exportconfiguration | 1 | resource-group | item-write | armapplicationinsights:ExportConfigurations.List |
| microsoft.insights | microsoft.insights/components/favorites | 1 | resource-group | item-write | armapplicationinsights:Favorites.List |
| microsoft.insights | microsoft.insights/components/proactivedetectionconfigs | 1 | resource-group | item-write | armapplicationinsights:ProactiveDetectionConfigurations.List |
| microsoft.insights | microsoft.insights/components/workitemconfigs | 1 | resource-group | item-write | armapplicationinsights:WorkItemConfigurations.List |
| microsoft.insights | microsoft.insights/datacollectionendpoints | 0 | subscription | item-write | armmonitor:DataCollectionEndpoints.ListByResourceGroup, armmonitor:DataCollectionEndpoints.ListBySubscription |
| microsoft.insights | microsoft.insights/datacollectionendpoints/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armmonitor:DataCollectionEndpoints.ListNSP |
| microsoft.insights | microsoft.insights/datacollectionruleassociations | 0 | extension | item-write | armmonitor:DataCollectionRuleAssociations.ListByDataCollectionEndpoint, armmonitor:DataCollectionRuleAssociations.ListByResource, armmonitor:DataCollectionRuleAssociations.ListByRule |
| microsoft.insights | microsoft.insights/datacollectionrules | 0 | subscription | item-write | armmonitor:DataCollectionRules.ListByResourceGroup, armmonitor:DataCollectionRules.ListBySubscription |
| microsoft.insights | microsoft.insights/logprofiles | 0 | subscription | item-write | armmonitor:LogProfiles.List |
| microsoft.insights | microsoft.insights/metricalerts | 0 | subscription | item-write | armmonitor:MetricAlerts.ListByResourceGroup, armmonitor:MetricAlerts.ListBySubscription |
| microsoft.insights | microsoft.insights/metricalerts/status | 1 | resource-group | arm-envelope | armmonitor:MetricAlertsStatus.List |
| microsoft.insights | microsoft.insights/privatelinkscopes | 0 | subscription | item-write | armmonitor:PrivateLinkScopes.List, armmonitor:PrivateLinkScopes.ListByResourceGroup |
| microsoft.insights | microsoft.insights/privatelinkscopes/privateendpointconnections | 1 | resource-group | item-write | armmonitor:PrivateEndpointConnections.ListByPrivateLinkScope |
| microsoft.insights | microsoft.insights/privatelinkscopes/privatelinkresources | 1 | resource-group | arm-envelope | armmonitor:PrivateLinkResources.ListByPrivateLinkScope |
| microsoft.insights | microsoft.insights/privatelinkscopes/scopedresources | 1 | resource-group | item-write | armmonitor:PrivateLinkScopedResources.ListByPrivateLinkScope |
| microsoft.insights | microsoft.insights/scheduledqueryrules | 0 | subscription | item-write | armmonitor:ScheduledQueryRules.ListByResourceGroup, armmonitor:ScheduledQueryRules.ListBySubscription |
| microsoft.insights | microsoft.insights/scheduledqueryrules/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armmonitor:ScheduledQueryRule.ListNSP |
| microsoft.insights | microsoft.insights/tenantactiongroups | 0 | management-group | item-write | armmonitor:TenantActionGroups.ListByManagementGroupID |
| microsoft.insights | microsoft.insights/webtests | 0 | subscription | item-write | armapplicationinsights:WebTests.List, armapplicationinsights:WebTests.ListByComponent, armapplicationinsights:WebTests.ListByResourceGroup |
| microsoft.insights | microsoft.insights/workbooks | 0 | subscription | item-write | armapplicationinsights:Workbooks.ListByResourceGroup, armapplicationinsights:Workbooks.ListBySubscription |
| microsoft.insights | microsoft.insights/workbooks/revisions | 1 | resource-group | arm-envelope | armapplicationinsights:Workbooks.RevisionsList |
| microsoft.insights | microsoft.insights/workbooktemplates | 0 | resource-group | item-write | armapplicationinsights:WorkbookTemplates.ListByResourceGroup |
| microsoft.integrationspaces | microsoft.integrationspaces/spaces/applications | 1 | resource-group | item-write | armintegrationspaces:Applications.ListBySpace |
| microsoft.integrationspaces | microsoft.integrationspaces/spaces/applications/businessprocesses | 2 | resource-group | item-write | armintegrationspaces:BusinessProcesses.ListByApplication |
| microsoft.integrationspaces | microsoft.integrationspaces/spaces/applications/businessprocesses/versions | 3 | resource-group | arm-envelope | armintegrationspaces:BusinessProcessVersions.ListByBusinessProcess |
| microsoft.integrationspaces | microsoft.integrationspaces/spaces/applications/resources | 2 | resource-group | item-write | armintegrationspaces:ApplicationResources.ListByApplication |
| microsoft.integrationspaces | microsoft.integrationspaces/spaces/infrastructureresources | 1 | resource-group | item-write | armintegrationspaces:InfrastructureResources.ListBySpace |
| microsoft.iotfirmwaredefense | microsoft.iotfirmwaredefense/workspaces/firmwares | 1 | resource-group | item-write | armiotfirmwaredefense:Firmwares.ListByWorkspace |
| microsoft.iotfirmwaredefense | microsoft.iotfirmwaredefense/workspaces/firmwares/summaries | 2 | resource-group | arm-envelope | armiotfirmwaredefense:Summaries.ListByFirmware |
| microsoft.iotfirmwaredefense | microsoft.iotfirmwaredefense/workspaces/usagemetrics | 1 | resource-group | arm-envelope | armiotfirmwaredefense:UsageMetrics.ListByWorkspace |
| microsoft.iotoperations | microsoft.iotoperations/instances/akriconnectortemplates | 1 | resource-group | item-write | armiotoperations:AkriConnectorTemplate.ListByInstanceResource |
| microsoft.iotoperations | microsoft.iotoperations/instances/akriconnectortemplates/connectors | 2 | resource-group | item-write | armiotoperations:AkriConnector.ListByTemplate |
| microsoft.iotoperations | microsoft.iotoperations/instances/akriservices | 1 | resource-group | item-write | armiotoperations:AkriService.ListByInstanceResource |
| microsoft.iotoperations | microsoft.iotoperations/instances/brokers | 1 | resource-group | item-write | armiotoperations:Broker.ListByResourceGroup |
| microsoft.iotoperations | microsoft.iotoperations/instances/brokers/authentications | 2 | resource-group | item-write | armiotoperations:BrokerAuthentication.ListByResourceGroup |
| microsoft.iotoperations | microsoft.iotoperations/instances/brokers/authorizations | 2 | resource-group | item-write | armiotoperations:BrokerAuthorization.ListByResourceGroup |
| microsoft.iotoperations | microsoft.iotoperations/instances/brokers/listeners | 2 | resource-group | item-write | armiotoperations:BrokerListener.ListByResourceGroup |
| microsoft.iotoperations | microsoft.iotoperations/instances/dataflowendpoints | 1 | resource-group | item-write | armiotoperations:DataflowEndpoint.ListByResourceGroup |
| microsoft.iotoperations | microsoft.iotoperations/instances/dataflowprofiles | 1 | resource-group | item-write | armiotoperations:DataflowProfile.ListByResourceGroup |
| microsoft.iotoperations | microsoft.iotoperations/instances/dataflowprofiles/dataflowgraphs | 2 | resource-group | item-write | armiotoperations:DataflowGraph.ListByDataflowProfile |
| microsoft.iotoperations | microsoft.iotoperations/instances/dataflowprofiles/dataflows | 2 | resource-group | item-write | armiotoperations:Dataflow.ListByResourceGroup |
| microsoft.iotoperations | microsoft.iotoperations/instances/registryendpoints | 1 | resource-group | item-write | armiotoperations:RegistryEndpoint.ListByInstanceResource |
| microsoft.iotsecurity | microsoft.iotsecurity/defendersettings | 0 | subscription | item-write | armiotsecurity:DefenderSettings.List |
| microsoft.iotsecurity | microsoft.iotsecurity/devicegroups | 0 | subscription | item-write | armiotsecurity:DeviceGroups.List |
| microsoft.iotsecurity | microsoft.iotsecurity/locations | 0 | subscription | arm-envelope | armiotsecurity:Locations.List |
| microsoft.iotsecurity | microsoft.iotsecurity/onpremisesensors | 0 | subscription | item-write | armiotsecurity:OnPremiseSensors.List |
| microsoft.iotsecurity | microsoft.iotsecurity/sensors | 0 | extension | item-write | armiotsecurity:Sensors.List |
| microsoft.iotsecurity | microsoft.iotsecurity/sites | 0 | extension | item-write | armiotsecurity:Sites.List |
| microsoft.keyvault | microsoft.keyvault/deletedmanagedhsms | 0 | subscription | arm-envelope | armkeyvault:ManagedHsms.ListDeleted |
| microsoft.keyvault | microsoft.keyvault/deletedvaults | 0 | subscription | arm-envelope | armkeyvault:Vaults.ListDeleted |
| microsoft.keyvault | microsoft.keyvault/managedhsms/keys | 1 | resource-group | item-write | armkeyvault:ManagedHsmKeys.List |
| microsoft.keyvault | microsoft.keyvault/managedhsms/keys/versions | 2 | resource-group | arm-envelope | armkeyvault:ManagedHsmKeys.ListVersions |
| microsoft.keyvault | microsoft.keyvault/managedhsms/privateendpointconnections | 1 | resource-group | item-write | armkeyvault:MHSMPrivateEndpointConnections.ListByResource |
| microsoft.keyvault | microsoft.keyvault/vaults/keys | 1 | resource-group | item-write | armkeyvault:Keys.List |
| microsoft.keyvault | microsoft.keyvault/vaults/keys/versions | 2 | resource-group | arm-envelope | armkeyvault:Keys.ListVersions |
| microsoft.keyvault | microsoft.keyvault/vaults/privateendpointconnections | 1 | resource-group | item-write | armkeyvault:PrivateEndpointConnections.ListByResource |
| microsoft.keyvault | microsoft.keyvault/vaults/secrets | 1 | resource-group | item-write | armkeyvault:Secrets.List |
| microsoft.kubernetesconfiguration | microsoft.kubernetesconfiguration/extensions | 0 | extension | item-write | armextensions:Client.List, armkubernetesconfiguration:Extensions.List |
| microsoft.kubernetesconfiguration | microsoft.kubernetesconfiguration/extensiontypes/versions | 1 | extension | arm-envelope | armextensiontypes:Client.ClusterListVersions, armextensiontypes:Client.ListVersions |
| microsoft.kubernetesconfiguration | microsoft.kubernetesconfiguration/fluxconfigurations | 0 | extension | item-write | armfluxconfigurations:Client.List, armkubernetesconfiguration:FluxConfigurations.List |
| microsoft.kubernetesconfiguration | microsoft.kubernetesconfiguration/privatelinkscopes | 0 | subscription | item-write | armprivatelinkscopes:Client.List, armprivatelinkscopes:Client.ListByResourceGroup |
| microsoft.kubernetesconfiguration | microsoft.kubernetesconfiguration/privatelinkscopes/privateendpointconnections | 1 | resource-group | item-write | armprivatelinkscopes:PrivateEndpointConnections.ListByPrivateLinkScope |
| microsoft.kubernetesconfiguration | microsoft.kubernetesconfiguration/privatelinkscopes/privatelinkresources | 1 | resource-group | arm-envelope | armprivatelinkscopes:PrivateLinkResources.ListByPrivateLinkScope |
| microsoft.kubernetesconfiguration | microsoft.kubernetesconfiguration/sourcecontrolconfigurations | 0 | extension | item-write | armkubernetesconfiguration:SourceControlConfigurations.List |
| microsoft.kubernetesruntime | microsoft.kubernetesruntime/bgppeers | 0 | extension | item-write | armcontainerorchestratorruntime:BgpPeers.List |
| microsoft.kubernetesruntime | microsoft.kubernetesruntime/loadbalancers | 0 | extension | item-write | armcontainerorchestratorruntime:LoadBalancers.List |
| microsoft.kubernetesruntime | microsoft.kubernetesruntime/services | 0 | extension | item-write | armcontainerorchestratorruntime:Services.List |
| microsoft.kubernetesruntime | microsoft.kubernetesruntime/storageclasses | 0 | extension | item-write | armcontainerorchestratorruntime:StorageClass.List |
| microsoft.kusto | microsoft.kusto/clusters/attacheddatabaseconfigurations | 1 | resource-group | item-write | armkusto:AttachedDatabaseConfigurations.ListByCluster |
| microsoft.kusto | microsoft.kusto/clusters/databases | 1 | resource-group | item-write | armkusto:Databases.ListByCluster |
| microsoft.kusto | microsoft.kusto/clusters/databases/dataconnections | 2 | resource-group | item-write | armkusto:DataConnections.ListByDatabase |
| microsoft.kusto | microsoft.kusto/clusters/databases/principalassignments | 2 | resource-group | item-write | armkusto:DatabasePrincipalAssignments.List |
| microsoft.kusto | microsoft.kusto/clusters/databases/scripts | 2 | resource-group | item-write | armkusto:Scripts.ListByDatabase |
| microsoft.kusto | microsoft.kusto/clusters/managedprivateendpoints | 1 | resource-group | item-write | armkusto:ManagedPrivateEndpoints.List |
| microsoft.kusto | microsoft.kusto/clusters/principalassignments | 1 | resource-group | item-write | armkusto:ClusterPrincipalAssignments.List |
| microsoft.kusto | microsoft.kusto/clusters/privateendpointconnections | 1 | resource-group | item-write | armkusto:PrivateEndpointConnections.List |
| microsoft.kusto | microsoft.kusto/clusters/privatelinkresources | 1 | resource-group | arm-envelope | armkusto:PrivateLinkResources.List |
| microsoft.kusto | microsoft.kusto/clusters/sandboxcustomimages | 1 | resource-group | item-write | armkusto:SandboxCustomImages.ListByCluster |
| microsoft.labservices | microsoft.labservices/labplans/images | 1 | resource-group | item-write | armlabservices:Images.ListByLabPlan |
| microsoft.labservices | microsoft.labservices/labs/schedules | 1 | resource-group | item-write | armlabservices:Schedules.ListByLab |
| microsoft.labservices | microsoft.labservices/labs/users | 1 | resource-group | item-write | armlabservices:Users.ListByLab |
| microsoft.labservices | microsoft.labservices/labs/virtualmachines | 1 | resource-group | arm-envelope | armlabservices:VirtualMachines.ListByLab |
| microsoft.loadtestservice | microsoft.loadtestservice/playwrightworkspaces | 0 | subscription | item-write | armplaywright:Workspaces.ListByResourceGroup, armplaywright:Workspaces.ListBySubscription |
| microsoft.loadtestservice | microsoft.loadtestservice/playwrightworkspaces/quotas | 1 | resource-group | arm-envelope | armplaywright:WorkspaceQuotas.ListByPlaywrightWorkspace |
| microsoft.logic | microsoft.logic/integrationaccounts/agreements | 1 | resource-group | item-write | armlogic:IntegrationAccountAgreements.List |
| microsoft.logic | microsoft.logic/integrationaccounts/assemblies | 1 | resource-group | item-write | armlogic:IntegrationAccountAssemblies.List |
| microsoft.logic | microsoft.logic/integrationaccounts/batchconfigurations | 1 | resource-group | item-write | armlogic:IntegrationAccountBatchConfigurations.List |
| microsoft.logic | microsoft.logic/integrationaccounts/certificates | 1 | resource-group | item-write | armlogic:IntegrationAccountCertificates.List |
| microsoft.logic | microsoft.logic/integrationaccounts/maps | 1 | resource-group | item-write | armlogic:IntegrationAccountMaps.List |
| microsoft.logic | microsoft.logic/integrationaccounts/partners | 1 | resource-group | item-write | armlogic:IntegrationAccountPartners.List |
| microsoft.logic | microsoft.logic/integrationaccounts/schemas | 1 | resource-group | item-write | armlogic:IntegrationAccountSchemas.List |
| microsoft.logic | microsoft.logic/integrationaccounts/sessions | 1 | resource-group | item-write | armlogic:IntegrationAccountSessions.List |
| microsoft.logic | microsoft.logic/integrationserviceenvironments/managedapis | 1 | resource-group | item-write | armlogic:IntegrationServiceEnvironmentManagedApis.List |
| microsoft.logic | microsoft.logic/workflows/runs | 1 | resource-group | arm-envelope | armlogic:WorkflowRuns.List |
| microsoft.logic | microsoft.logic/workflows/runs/actions | 2 | resource-group | arm-envelope | armlogic:WorkflowRunActions.List |
| microsoft.logic | microsoft.logic/workflows/runs/actions/repetitions | 3 | resource-group | arm-envelope | armlogic:WorkflowRunActionRepetitions.List |
| microsoft.logic | microsoft.logic/workflows/runs/actions/repetitions/requesthistories | 4 | resource-group | arm-envelope | armlogic:WorkflowRunActionRepetitionsRequestHistories.List |
| microsoft.logic | microsoft.logic/workflows/runs/actions/requesthistories | 3 | resource-group | arm-envelope | armlogic:WorkflowRunActionRequestHistories.List |
| microsoft.logic | microsoft.logic/workflows/runs/actions/scoperepetitions | 3 | resource-group | arm-envelope | armlogic:WorkflowRunActionScopeRepetitions.List |
| microsoft.logic | microsoft.logic/workflows/triggers | 1 | resource-group | arm-envelope | armlogic:WorkflowTriggers.List |
| microsoft.logic | microsoft.logic/workflows/triggers/histories | 2 | resource-group | arm-envelope | armlogic:WorkflowTriggerHistories.List |
| microsoft.logic | microsoft.logic/workflows/versions | 1 | resource-group | arm-envelope | armlogic:WorkflowVersions.List |
| microsoft.logz | microsoft.logz/monitors | 0 | subscription | item-write | armlogz:Monitors.ListByResourceGroup, armlogz:Monitors.ListBySubscription |
| microsoft.logz | microsoft.logz/monitors/accounts | 1 | resource-group | item-write | armlogz:SubAccount.List |
| microsoft.logz | microsoft.logz/monitors/accounts/tagrules | 2 | resource-group | item-write | armlogz:SubAccountTagRules.List |
| microsoft.logz | microsoft.logz/monitors/singlesignonconfigurations | 1 | resource-group | item-write | armlogz:SingleSignOn.List |
| microsoft.logz | microsoft.logz/monitors/tagrules | 1 | resource-group | item-write | armlogz:TagRules.List |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforedmupload | 0 | subscription | item-write | armm365securityandcompliance:PrivateLinkServicesForEDMUpload.List, armm365securityandcompliance:PrivateLinkServicesForEDMUpload.ListByResourceGroup |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforedmupload/privateendpointconnections | 1 | resource-group | item-write | armm365securityandcompliance:PrivateEndpointConnectionsForEDM.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforedmupload/privatelinkresources | 1 | resource-group | arm-envelope | armm365securityandcompliance:PrivateLinkResources.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesform365compliancecenter | 0 | subscription | item-write | armm365securityandcompliance:PrivateLinkServicesForM365ComplianceCenter.List, armm365securityandcompliance:PrivateLinkServicesForM365ComplianceCenter.ListByResourceGroup |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesform365compliancecenter/privateendpointconnections | 1 | resource-group | item-write | armm365securityandcompliance:PrivateEndpointConnectionsComp.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesform365compliancecenter/privatelinkresources | 1 | resource-group | arm-envelope | armm365securityandcompliance:PrivateLinkResourcesComp.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesform365securitycenter | 0 | subscription | item-write | armm365securityandcompliance:PrivateLinkServicesForM365SecurityCenter.List, armm365securityandcompliance:PrivateLinkServicesForM365SecurityCenter.ListByResourceGroup |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesform365securitycenter/privateendpointconnections | 1 | resource-group | item-write | armm365securityandcompliance:PrivateEndpointConnectionsSec.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesform365securitycenter/privatelinkresources | 1 | resource-group | arm-envelope | armm365securityandcompliance:PrivateLinkResourcesSec.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesformippolicysync | 0 | subscription | item-write | armm365securityandcompliance:PrivateLinkServicesForMIPPolicySync.List, armm365securityandcompliance:PrivateLinkServicesForMIPPolicySync.ListByResourceGroup |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesformippolicysync/privateendpointconnections | 1 | resource-group | item-write | armm365securityandcompliance:PrivateEndpointConnectionsForMIPPolicySync.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesformippolicysync/privatelinkresources | 1 | resource-group | arm-envelope | armm365securityandcompliance:PrivateLinkResourcesForMIPPolicySync.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforo365managementactivityapi | 0 | subscription | item-write | armm365securityandcompliance:PrivateLinkServicesForO365ManagementActivityAPI.List, armm365securityandcompliance:PrivateLinkServicesForO365ManagementActivityAPI.ListByResourceGroup |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforo365managementactivityapi/privateendpointconnections | 1 | resource-group | item-write | armm365securityandcompliance:PrivateEndpointConnectionsAdtAPI.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforo365managementactivityapi/privatelinkresources | 1 | resource-group | arm-envelope | armm365securityandcompliance:PrivateLinkResourcesAdtAPI.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforsccpowershell | 0 | subscription | item-write | armm365securityandcompliance:PrivateLinkServicesForSCCPowershell.List, armm365securityandcompliance:PrivateLinkServicesForSCCPowershell.ListByResourceGroup |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforsccpowershell/privateendpointconnections | 1 | resource-group | item-write | armm365securityandcompliance:PrivateEndpointConnectionsForSCCPowershell.ListByService |
| microsoft.m365securityandcompliance | microsoft.m365securityandcompliance/privatelinkservicesforsccpowershell/privatelinkresources | 1 | resource-group | arm-envelope | armm365securityandcompliance:PrivateLinkResourcesForSCCPowershell.ListByService |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/codes | 1 | resource-group | item-write | armmachinelearning:RegistryCodeContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/codes/versions | 2 | resource-group | item-write | armmachinelearning:RegistryCodeVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/components | 1 | resource-group | item-write | armmachinelearning:RegistryComponentContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/components/versions | 2 | resource-group | item-write | armmachinelearning:RegistryComponentVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/data | 1 | resource-group | item-write | armmachinelearning:RegistryDataContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/data/versions | 2 | resource-group | item-write | armmachinelearning:RegistryDataVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/environments | 1 | resource-group | item-write | armmachinelearning:RegistryEnvironmentContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/environments/versions | 2 | resource-group | item-write | armmachinelearning:RegistryEnvironmentVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/models | 1 | resource-group | item-write | armmachinelearning:RegistryModelContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/registries/models/versions | 2 | resource-group | item-write | armmachinelearning:RegistryModelVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/batchendpoints | 1 | resource-group | item-write | armmachinelearning:BatchEndpoints.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/batchendpoints/deployments | 2 | resource-group | item-write | armmachinelearning:BatchDeployments.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/codes | 1 | resource-group | item-write | armmachinelearning:CodeContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/codes/versions | 2 | resource-group | item-write | armmachinelearning:CodeVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/components | 1 | resource-group | item-write | armmachinelearning:ComponentContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/components/versions | 2 | resource-group | item-write | armmachinelearning:ComponentVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/computes | 1 | resource-group | item-write | armmachinelearning:Compute.List, armmachinelearningservices:Compute.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/connections | 1 | resource-group | item-write | armmachinelearning:WorkspaceConnections.List, armmachinelearningservices:WorkspaceConnections.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/connections/deployments | 2 | resource-group | item-write | armmachinelearning:Connection.ListDeployments, armmachinelearning:EndpointDeployment.GetInWorkspace |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/connections/raiblocklists | 2 | resource-group | item-write | armmachinelearning:ConnectionRaiBlocklists.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/connections/raiblocklists/raiblocklistitems | 3 | resource-group | item-write | armmachinelearning:ConnectionRaiBlocklistItems.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/connections/raipolicies | 2 | resource-group | item-write | armmachinelearning:ConnectionRaiPolicies.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/data | 1 | resource-group | item-write | armmachinelearning:DataContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/data/versions | 2 | resource-group | item-write | armmachinelearning:DataVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/datastores | 1 | resource-group | item-write | armmachinelearning:Datastores.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/endpoints | 1 | resource-group | item-write | armmachinelearning:Endpoint.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/endpoints/deployments | 2 | resource-group | item-write | armmachinelearning:EndpointDeployment.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/endpoints/raipolicies | 2 | resource-group | item-write | armmachinelearning:RaiPolicies.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/environments | 1 | resource-group | item-write | armmachinelearning:EnvironmentContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/environments/versions | 2 | resource-group | item-write | armmachinelearning:EnvironmentVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/featuresets | 1 | resource-group | item-write | armmachinelearning:FeaturesetContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/featuresets/versions | 2 | resource-group | item-write | armmachinelearning:FeaturesetVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/featuresets/versions/features | 3 | resource-group | arm-envelope | armmachinelearning:Features.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/featurestoreentities | 1 | resource-group | item-write | armmachinelearning:FeaturestoreEntityContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/featurestoreentities/versions | 2 | resource-group | item-write | armmachinelearning:FeaturestoreEntityVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/inferencepools | 1 | resource-group | item-write | armmachinelearning:InferencePools.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/inferencepools/endpoints | 2 | resource-group | item-write | armmachinelearning:InferenceEndpoints.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/inferencepools/groups | 2 | resource-group | item-write | armmachinelearning:InferenceGroups.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/jobs | 1 | resource-group | item-write | armmachinelearning:Jobs.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/managednetworks | 1 | resource-group | item-write | armmachinelearning:ManagedNetworkSettings.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/managednetworks/outboundrules | 2 | resource-group | item-write | armmachinelearning:OutboundRule.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/marketplacesubscriptions | 1 | resource-group | item-write | armmachinelearning:MarketplaceSubscriptions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/models | 1 | resource-group | item-write | armmachinelearning:ModelContainers.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/models/versions | 2 | resource-group | item-write | armmachinelearning:ModelVersions.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/onlineendpoints | 1 | resource-group | item-write | armmachinelearning:OnlineEndpoints.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/onlineendpoints/deployments | 2 | resource-group | item-write | armmachinelearning:OnlineDeployments.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/outboundrules | 1 | resource-group | item-write | armmachinelearning:ManagedNetworkSettingsRule.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/privateendpointconnections | 1 | resource-group | item-write | armmachinelearning:PrivateEndpointConnections.List, armmachinelearningservices:PrivateEndpointConnections.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/schedules | 1 | resource-group | item-write | armmachinelearning:Schedules.List |
| microsoft.machinelearningservices | microsoft.machinelearningservices/workspaces/serverlessendpoints | 1 | resource-group | item-write | armmachinelearning:ServerlessEndpoints.List |
| microsoft.maintenance | microsoft.maintenance/applyupdates | 0 | subscription | item-write | armmaintenance:ApplyUpdateForResourceGroup.List, armmaintenance:ApplyUpdates.List |
| microsoft.managedidentity | microsoft.managedidentity/userassignedidentities/federatedidentitycredentials | 1 | resource-group | item-write | armmsi:FederatedIdentityCredentials.List |
| microsoft.managednetwork | microsoft.managednetwork/managednetworks | 0 | subscription | item-write | armmanagednetwork:ManagedNetworks.ListByResourceGroup, armmanagednetwork:ManagedNetworks.ListBySubscription |
| microsoft.managednetwork | microsoft.managednetwork/managednetworks/managednetworkgroups | 1 | resource-group | item-write | armmanagednetwork:Groups.ListByManagedNetwork |
| microsoft.managednetwork | microsoft.managednetwork/managednetworks/managednetworkpeeringpolicies | 1 | resource-group | item-write | armmanagednetwork:PeeringPolicies.ListByManagedNetwork |
| microsoft.managednetwork | microsoft.managednetwork/scopeassignments | 0 | extension | item-write | armmanagednetwork:ScopeAssignments.List |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/l3isolationdomains/externalnetworks | 1 | resource-group | item-write | armmanagednetworkfabric:ExternalNetworks.ListByL3IsolationDomain |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/l3isolationdomains/internalnetworks | 1 | resource-group | item-write | armmanagednetworkfabric:InternalNetworks.ListByL3IsolationDomain |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/networkbootstrapdevices | 0 | subscription | item-write | armmanagednetworkfabric:NetworkBootstrapDevices.ListByResourceGroup, armmanagednetworkfabric:NetworkBootstrapDevices.ListBySubscription |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/networkbootstrapdevices/networkbootstrapinterfaces | 1 | resource-group | item-write | armmanagednetworkfabric:NetworkBootstrapInterfaces.ListByNetworkBootstrapDevice |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/networkdevices/networkinterfaces | 1 | resource-group | item-write | armmanagednetworkfabric:NetworkInterfaces.ListByNetworkDevice |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/networkdeviceskus | 0 | subscription | arm-envelope | armmanagednetworkfabric:NetworkDeviceSKUs.ListBySubscription |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/networkfabrics/networktonetworkinterconnects | 1 | resource-group | item-write | armmanagednetworkfabric:NetworkToNetworkInterconnects.ListByNetworkFabric |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/networkfabricskus | 0 | subscription | arm-envelope | armmanagednetworkfabric:NetworkFabricSKUs.ListBySubscription |
| microsoft.managednetworkfabric | microsoft.managednetworkfabric/networkmonitors | 0 | subscription | item-write | armmanagednetworkfabric:NetworkMonitors.ListByResourceGroup, armmanagednetworkfabric:NetworkMonitors.ListBySubscription |
| microsoft.managedops | microsoft.managedops/managedops | 0 | subscription | item-write | armmanagedops:Client.List |
| microsoft.management | microsoft.management/settings | 0 | management-group | item-write | armmanagementgroups:HierarchySettings.List |
| microsoft.management | microsoft.management/subscriptions | 0 | management-group | item-write | armmanagementgroups:ManagementGroupSubscriptions.GetSubscriptionsUnderManagementGroup |
| microsoft.managementpartner | microsoft.managementpartner/partners | 0 | tenant | item-write | armmanagementpartner:Partners.Get |
| microsoft.maps | microsoft.maps/accounts/creators | 1 | resource-group | item-write | armmaps:Creators.ListByAccount |
| microsoft.maps | microsoft.maps/accounts/privateendpointconnections | 1 | resource-group | item-write | armmaps:PrivateEndpointConnections.ListByAccount |
| microsoft.maps | microsoft.maps/accounts/privatelinkresources | 1 | resource-group | arm-envelope | armmaps:PrivateLinkResources.ListByAccount |
| microsoft.marketplace | microsoft.marketplace/privatestores | 0 | tenant | item-write | armmarketplace:PrivateStore.List |
| microsoft.marketplace | microsoft.marketplace/privatestores/adminrequestapprovals | 1 | tenant | item-write | armmarketplace:PrivateStore.AdminRequestApprovalsList |
| microsoft.marketplace | microsoft.marketplace/privatestores/collections | 1 | tenant | item-write | armmarketplace:PrivateStoreCollection.List |
| microsoft.marketplace | microsoft.marketplace/privatestores/collections/offers | 2 | tenant | item-write | armmarketplace:PrivateStoreCollectionOffer.List |
| microsoft.marketplace | microsoft.marketplace/privatestores/requestapprovals | 1 | tenant | item-write | armmarketplace:PrivateStore.GetApprovalRequestsList |
| microsoft.media | microsoft.media/videoanalyzers | 0 | subscription | item-write | armvideoanalyzer:VideoAnalyzers.List, armvideoanalyzer:VideoAnalyzers.ListBySubscription |
| microsoft.media | microsoft.media/videoanalyzers/accesspolicies | 1 | resource-group | item-write | armvideoanalyzer:AccessPolicies.List |
| microsoft.media | microsoft.media/videoanalyzers/edgemodules | 1 | resource-group | item-write | armvideoanalyzer:EdgeModules.List |
| microsoft.media | microsoft.media/videoanalyzers/livepipelines | 1 | resource-group | item-write | armvideoanalyzer:LivePipelines.List |
| microsoft.media | microsoft.media/videoanalyzers/pipelinejobs | 1 | resource-group | item-write | armvideoanalyzer:PipelineJobs.List |
| microsoft.media | microsoft.media/videoanalyzers/pipelinetopologies | 1 | resource-group | item-write | armvideoanalyzer:PipelineTopologies.List |
| microsoft.media | microsoft.media/videoanalyzers/privateendpointconnections | 1 | resource-group | item-write | armvideoanalyzer:PrivateEndpointConnections.List |
| microsoft.media | microsoft.media/videoanalyzers/privatelinkresources | 1 | resource-group | arm-envelope | armvideoanalyzer:PrivateLinkResources.List |
| microsoft.media | microsoft.media/videoanalyzers/videos | 1 | resource-group | item-write | armvideoanalyzer:Videos.List |
| microsoft.migrate | microsoft.migrate/assessmentprojects/aksassessmentoptions | 1 | resource-group | arm-envelope | armmigrationassessment:AksOptionsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/aksassessments | 1 | resource-group | item-write | armmigrationassessment:AksAssessmentOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/aksassessments/assessedwebapps | 2 | resource-group | arm-envelope | armmigrationassessment:AssessedWebApplicationOperations.ListByAksAssessment |
| microsoft.migrate | microsoft.migrate/assessmentprojects/aksassessments/clusters | 2 | resource-group | arm-envelope | armmigrationassessment:AksClusterOperations.ListByAksAssessment |
| microsoft.migrate | microsoft.migrate/assessmentprojects/aksassessments/summaries | 2 | resource-group | arm-envelope | armmigrationassessment:AksSummaryOperations.ListByAksAssessment |
| microsoft.migrate | microsoft.migrate/assessmentprojects/assessmentoptions | 1 | resource-group | arm-envelope | armmigrate:Projects.AssessmentOptionsList, armmigrationassessment:AssessmentOptionsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/avsassessmentoptions | 1 | resource-group | arm-envelope | armmigrationassessment:AvsAssessmentOptionsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases | 1 | resource-group | item-write | armmigrationassessment:BusinessCaseOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/avssummaries | 2 | resource-group | arm-envelope | armmigrationassessment:BusinessCaseAvsSummaryOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/evaluatedavsmachines | 2 | resource-group | arm-envelope | armmigrationassessment:EvaluatedAvsMachinesOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/evaluatedmachines | 2 | resource-group | arm-envelope | armmigrationassessment:EvaluatedMachinesOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/evaluatedsqlentities | 2 | resource-group | arm-envelope | armmigrationassessment:EvaluatedSQLEntitiesOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/evaluatedwebapps | 2 | resource-group | arm-envelope | armmigrationassessment:EvaluatedWebAppsOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/iaassummaries | 2 | resource-group | arm-envelope | armmigrationassessment:BusinessCaseIaasSummaryOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/overviewsummaries | 2 | resource-group | arm-envelope | armmigrationassessment:BusinessCaseOverviewSummaryOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/businesscases/paassummaries | 2 | resource-group | arm-envelope | armmigrationassessment:BusinessCasePaasSummaryOperations.ListByBusinessCase |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups | 1 | resource-group | item-write | armmigrate:Groups.ListByProject, armmigrationassessment:GroupsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/assessments | 2 | resource-group | item-write | armmigrate:Assessments.ListByGroup, armmigrate:Assessments.ListByProject, armmigrationassessment:AssessmentsOperations.ListByGroup |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/assessments/assessedmachines | 3 | resource-group | arm-envelope | armmigrate:AssessedMachines.ListByAssessment, armmigrationassessment:AssessedMachinesOperations.ListByAssessment |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/avsassessments | 2 | resource-group | item-write | armmigrationassessment:AvsAssessmentsOperations.ListByGroup |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/avsassessments/avsassessedmachines | 3 | resource-group | arm-envelope | armmigrationassessment:AvsAssessedMachinesOperations.ListByAvsAssessment |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/sqlassessments | 2 | resource-group | item-write | armmigrationassessment:SQLAssessmentV2Operations.ListByGroup |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/sqlassessments/assessedsqldatabases | 3 | resource-group | arm-envelope | armmigrationassessment:AssessedSQLDatabaseV2Operations.ListBySQLAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/sqlassessments/assessedsqlinstances | 3 | resource-group | arm-envelope | armmigrationassessment:AssessedSQLInstanceV2Operations.ListBySQLAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/sqlassessments/assessedsqlmachines | 3 | resource-group | arm-envelope | armmigrationassessment:AssessedSQLMachinesOperations.ListBySQLAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/sqlassessments/recommendedassessedentities | 3 | resource-group | arm-envelope | armmigrationassessment:AssessedSQLRecommendedEntityOperations.ListBySQLAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/sqlassessments/summaries | 3 | resource-group | arm-envelope | armmigrationassessment:SQLAssessmentV2SummaryOperations.ListBySQLAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/webappassessments | 2 | resource-group | item-write | armmigrationassessment:WebAppAssessmentV2Operations.ListByGroup |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/webappassessments/assessedwebapps | 3 | resource-group | arm-envelope | armmigrationassessment:AssessedWebAppV2Operations.ListByWebAppAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/webappassessments/summaries | 3 | resource-group | arm-envelope | armmigrationassessment:WebAppAssessmentV2SummaryOperations.ListByWebAppAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/groups/webappassessments/webappserviceplans | 3 | resource-group | arm-envelope | armmigrationassessment:WebAppServicePlanV2Operations.ListByWebAppAssessmentV2 |
| microsoft.migrate | microsoft.migrate/assessmentprojects/hypervcollectors | 1 | resource-group | item-write | armmigrate:HyperVCollectors.ListByProject, armmigrationassessment:HypervCollectorsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/importcollectors | 1 | resource-group | item-write | armmigrate:ImportCollectors.ListByProject, armmigrationassessment:ImportCollectorsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/machines | 1 | resource-group | arm-envelope | armmigrate:Machines.ListByProject, armmigrationassessment:MachinesOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/privateendpointconnections | 1 | resource-group | item-write | armmigrate:PrivateEndpointConnection.ListByProject, armmigrationassessment:PrivateEndpointConnectionOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/privatelinkresources | 1 | resource-group | arm-envelope | armmigrate:PrivateLinkResource.ListByProject, armmigrationassessment:PrivateLinkResourceOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/projectsummary | 1 | resource-group | arm-envelope | armmigrationassessment:AssessmentProjectSummaryOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/servercollectors | 1 | resource-group | item-write | armmigrate:ServerCollectors.ListByProject, armmigrationassessment:ServerCollectorsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/sqlassessmentoptions | 1 | resource-group | arm-envelope | armmigrationassessment:SQLAssessmentOptionsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/sqlcollectors | 1 | resource-group | item-write | armmigrationassessment:SQLCollectorOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/vmwarecollectors | 1 | resource-group | item-write | armmigrate:VMwareCollectors.ListByProject, armmigrationassessment:VmwareCollectorsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/webappassessmentoptions | 1 | resource-group | arm-envelope | armmigrationassessment:WebAppAssessmentOptionsOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/assessmentprojects/webappcollectors | 1 | resource-group | item-write | armmigrationassessment:WebAppCollectorOperations.ListByAssessmentProject |
| microsoft.migrate | microsoft.migrate/movecollections | 0 | subscription | item-write | armresourcemover:MoveCollections.ListMoveCollectionsByResourceGroup, armresourcemover:MoveCollections.ListMoveCollectionsBySubscription |
| microsoft.migrate | microsoft.migrate/movecollections/moveresources | 1 | resource-group | item-write | armresourcemover:MoveResources.List |
| microsoft.mission | microsoft.mission/approvals | 0 | extension | item-write | armenclave:Approval.ListByParent |
| microsoft.mission | microsoft.mission/communities | 0 | subscription | item-write | armenclave:Community.ListByResourceGroup, armenclave:Community.ListBySubscription |
| microsoft.mission | microsoft.mission/communities/communityendpoints | 1 | subscription | item-write | armenclave:CommunityEndpoints.ListByCommunityResource, armenclave:CommunityEndpoints.ListBySubscription |
| microsoft.mission | microsoft.mission/communities/dedicatedhubs | 1 | subscription | item-write | armenclave:DedicatedHub.ListByCommunityResource, armenclave:DedicatedHub.ListBySubscription |
| microsoft.mission | microsoft.mission/communities/transithubs | 1 | subscription | item-write | armenclave:TransitHub.ListByCommunityResource, armenclave:TransitHub.ListBySubscription |
| microsoft.mission | microsoft.mission/enclaveconnections | 0 | subscription | item-write | armenclave:Connection.ListByResourceGroup, armenclave:Connection.ListBySubscription |
| microsoft.mission | microsoft.mission/virtualenclaves | 0 | subscription | item-write | armenclave:VirtualEnclave.ListByResourceGroup, armenclave:VirtualEnclave.ListBySubscription |
| microsoft.mission | microsoft.mission/virtualenclaves/enclaveendpoints | 1 | subscription | item-write | armenclave:Endpoints.ListByEnclaveResource, armenclave:Endpoints.ListBySubscription |
| microsoft.mission | microsoft.mission/virtualenclaves/workloads | 1 | subscription | item-write | armenclave:Workload.ListByEnclaveResource, armenclave:Workload.ListBySubscription |
| microsoft.mixedreality | microsoft.mixedreality/objectanchorsaccounts | 0 | subscription | item-write | armmixedreality:ObjectAnchorsAccounts.ListByResourceGroup, armmixedreality:ObjectAnchorsAccounts.ListBySubscription |
| microsoft.mixedreality | microsoft.mixedreality/remoterenderingaccounts | 0 | subscription | item-write | armmixedreality:RemoteRenderingAccounts.ListByResourceGroup, armmixedreality:RemoteRenderingAccounts.ListBySubscription |
| microsoft.mixedreality | microsoft.mixedreality/spatialanchorsaccounts | 0 | subscription | item-write | armmixedreality:SpatialAnchorsAccounts.ListByResourceGroup, armmixedreality:SpatialAnchorsAccounts.ListBySubscription |
| microsoft.monitor | microsoft.monitor/accounts | 0 | subscription | item-write | armmonitorworkspaces:AzureMonitorWorkspaces.ListByResourceGroup, armmonitorworkspaces:AzureMonitorWorkspaces.ListBySubscription |
| microsoft.monitor | microsoft.monitor/accounts/issues | 1 | resource-group | item-write | armmonitorworkspaces:Issue.List |
| microsoft.monitor | microsoft.monitor/accounts/metricscontainers | 1 | resource-group | item-write | armmonitorworkspaces:MetricsContainers.ListByAzureMonitorWorkspace |
| microsoft.monitor | microsoft.monitor/slis | 0 | tenant | item-write | armslis:Client.ListByParent |
| microsoft.netapp | microsoft.netapp/activedirectoryconfigs | 0 | subscription | item-write | armnetapp:ActiveDirectoryConfigs.ListByResourceGroup, armnetapp:ActiveDirectoryConfigs.ListBySubscription |
| microsoft.netapp | microsoft.netapp/elasticaccounts | 0 | subscription | item-write | armnetapp:ElasticAccounts.ListByResourceGroup, armnetapp:ElasticAccounts.ListBySubscription |
| microsoft.netapp | microsoft.netapp/elasticaccounts/elasticbackuppolicies | 1 | resource-group | item-write | armnetapp:ElasticBackupPolicies.ListByElasticAccount |
| microsoft.netapp | microsoft.netapp/elasticaccounts/elasticbackupvaults | 1 | resource-group | item-write | armnetapp:ElasticBackupVaults.ListByElasticAccount |
| microsoft.netapp | microsoft.netapp/elasticaccounts/elasticbackupvaults/elasticbackups | 2 | resource-group | item-write | armnetapp:ElasticBackups.ListByVault |
| microsoft.netapp | microsoft.netapp/elasticaccounts/elasticcapacitypools | 1 | resource-group | item-write | armnetapp:ElasticCapacityPools.ListByElasticAccount |
| microsoft.netapp | microsoft.netapp/elasticaccounts/elasticcapacitypools/elasticvolumes | 2 | resource-group | item-write | armnetapp:ElasticSnapshotPolicies.ListElasticVolumes, armnetapp:ElasticVolumes.ListByElasticPool |
| microsoft.netapp | microsoft.netapp/elasticaccounts/elasticcapacitypools/elasticvolumes/elasticsnapshots | 3 | resource-group | item-write | armnetapp:ElasticSnapshots.ListByElasticVolume |
| microsoft.netapp | microsoft.netapp/elasticaccounts/elasticsnapshotpolicies | 1 | resource-group | item-write | armnetapp:ElasticSnapshotPolicies.ListByElasticAccount |
| microsoft.netapp | microsoft.netapp/netappaccounts/backuppolicies | 1 | resource-group | item-write | armnetapp:BackupPolicies.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/backupvaults | 1 | resource-group | item-write | armnetapp:BackupVaults.ListByNetAppAccount |
| microsoft.netapp | microsoft.netapp/netappaccounts/backupvaults/backups | 2 | resource-group | item-write | armnetapp:Backups.ListByVault |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools | 1 | resource-group | item-write | armnetapp:Pools.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools/caches | 2 | resource-group | item-write | armnetapp:Caches.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools/volumes | 2 | resource-group | item-write | armnetapp:SnapshotPolicies.ListVolumes, armnetapp:Volumes.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools/volumes/buckets | 3 | resource-group | item-write | armnetapp:Buckets.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools/volumes/ransomwarereports | 3 | resource-group | arm-envelope | armnetapp:RansomwareReports.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools/volumes/snapshots | 3 | resource-group | item-write | armnetapp:Snapshots.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools/volumes/subvolumes | 3 | resource-group | item-write | armnetapp:Subvolumes.ListByVolume |
| microsoft.netapp | microsoft.netapp/netappaccounts/capacitypools/volumes/volumequotarules | 3 | resource-group | item-write | armnetapp:VolumeQuotaRules.ListByVolume |
| microsoft.netapp | microsoft.netapp/netappaccounts/quotalimits | 1 | resource-group | arm-envelope | armnetapp:ResourceQuotaLimitsAccount.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/snapshotpolicies | 1 | resource-group | item-write | armnetapp:SnapshotPolicies.List |
| microsoft.netapp | microsoft.netapp/netappaccounts/volumegroups | 1 | resource-group | item-write | armnetapp:VolumeGroups.ListByNetAppAccount |
| microsoft.network | microsoft.network/applicationgateways/privateendpointconnections | 1 | resource-group | item-write | armnetwork:ApplicationGatewayPrivateEndpointConnections.List |
| microsoft.network | microsoft.network/applicationsecuritygroups/addressprefixsets | 1 | resource-group | item-write | armnetwork:AddressPrefixSets.List |
| microsoft.network | microsoft.network/authenticationpolicies | 0 | subscription | item-write | armnetwork:AuthenticationPolicies.List, armnetwork:AuthenticationPolicies.ListAll |
| microsoft.network | microsoft.network/cloudserviceslots | 0 | resource-group | item-write | armnetwork:VipSwap.List |
| microsoft.network | microsoft.network/ddoscustompolicies | 0 | subscription | item-write | armnetwork:DdosCustomPolicies.List, armnetwork:DdosCustomPolicies.ListAll |
| microsoft.network | microsoft.network/dnsforwardingrulesets/forwardingrules | 1 | resource-group | item-write | armdnsresolver:ForwardingRules.List |
| microsoft.network | microsoft.network/dnsforwardingrulesets/virtualnetworklinks | 1 | resource-group | item-write | armdnsresolver:VirtualNetworkLinks.List |
| microsoft.network | microsoft.network/dnsresolverpolicies/dnssecurityrules | 1 | resource-group | item-write | armdnsresolver:DNSSecurityRules.List |
| microsoft.network | microsoft.network/dnsresolverpolicies/virtualnetworklinks | 1 | resource-group | item-write | armdnsresolver:PolicyVirtualNetworkLinks.List |
| microsoft.network | microsoft.network/dnsresolvers/inboundendpoints | 1 | resource-group | item-write | armdnsresolver:InboundEndpoints.List |
| microsoft.network | microsoft.network/dnsresolvers/outboundendpoints | 1 | resource-group | item-write | armdnsresolver:OutboundEndpoints.List |
| microsoft.network | microsoft.network/dnszones/dnssecconfigs | 1 | resource-group | item-write | armdns:DnssecConfigs.ListByDNSZone |
| microsoft.network | microsoft.network/expressroutecircuits/authorizations | 1 | resource-group | item-write | armnetwork:ExpressRouteCircuitAuthorizations.List |
| microsoft.network | microsoft.network/expressroutecircuits/peerings | 1 | resource-group | item-write | armnetwork:ExpressRouteCircuitPeerings.List |
| microsoft.network | microsoft.network/expressroutecircuits/peerings/connections | 2 | resource-group | item-write | armnetwork:ExpressRouteCircuitConnections.List |
| microsoft.network | microsoft.network/expressroutecircuits/peerings/peerconnections | 2 | resource-group | arm-envelope | armnetwork:PeerExpressRouteCircuitConnections.List |
| microsoft.network | microsoft.network/expressroutecrossconnections | 0 | subscription | item-write | armnetwork:ExpressRouteCrossConnections.List, armnetwork:ExpressRouteCrossConnections.ListByResourceGroup |
| microsoft.network | microsoft.network/expressroutecrossconnections/peerings | 1 | resource-group | item-write | armnetwork:ExpressRouteCrossConnectionPeerings.List |
| microsoft.network | microsoft.network/expressroutegateways/expressrouteconnections | 1 | resource-group | item-write | armnetwork:ExpressRouteConnections.List |
| microsoft.network | microsoft.network/expressroutelags | 0 | subscription | item-write | armnetwork:ExpressRouteLags.List, armnetwork:ExpressRouteLags.ListByResourceGroup |
| microsoft.network | microsoft.network/expressroutelags/links | 1 | resource-group | arm-envelope | armnetwork:ExpressRouteLags.LinksList |
| microsoft.network | microsoft.network/expressroutelags/links/members | 2 | resource-group | arm-envelope | armnetwork:ExpressRouteLags.MembersList |
| microsoft.network | microsoft.network/expressrouteports/authorizations | 1 | resource-group | item-write | armnetwork:ExpressRoutePortAuthorizations.List |
| microsoft.network | microsoft.network/expressrouteproviderports | 0 | subscription | arm-envelope | armnetwork:ExpressRouteProviderPortsLocation.List |
| microsoft.network | microsoft.network/firewallpolicies/kubeselectorgroups | 1 | resource-group | item-write | armnetwork:FirewallPolicyKubeSelectorGroups.List |
| microsoft.network | microsoft.network/firewallpolicies/rulecollectiongroups | 1 | resource-group | item-write | armnetwork:FirewallPolicyRuleCollectionGroups.List |
| microsoft.network | microsoft.network/firewallpolicies/signatureoverrides | 1 | resource-group | item-write | armnetwork:FirewallPolicyIdpsSignaturesOverrides.List |
| microsoft.network | microsoft.network/firstpartyservicetags | 0 | subscription | item-write | armnetwork:FirstPartyServiceTags.List, armnetwork:FirstPartyServiceTags.ListAll |
| microsoft.network | microsoft.network/frontdoors/frontendendpoints | 1 | resource-group | arm-envelope | armfrontdoor:FrontendEndpoints.ListByFrontDoor |
| microsoft.network | microsoft.network/frontdoors/rulesengines | 1 | resource-group | item-write | armfrontdoor:RulesEngines.ListByFrontDoor |
| microsoft.network | microsoft.network/interconnectgroups | 0 | subscription | item-write | armnetwork:InterconnectGroups.List, armnetwork:InterconnectGroups.ListAll |
| microsoft.network | microsoft.network/interconnectgroups/subgroups | 1 | resource-group | arm-envelope | armnetwork:Subgroups.List |
| microsoft.network | microsoft.network/loadbalancers/backendaddresspools | 1 | resource-group | item-write | armnetwork:LoadBalancerBackendAddressPools.List |
| microsoft.network | microsoft.network/loadbalancers/frontendipconfigurations | 1 | resource-group | arm-envelope | armnetwork:LoadBalancerFrontendIPConfigurations.List |
| microsoft.network | microsoft.network/loadbalancers/inboundnatrules | 1 | resource-group | item-write | armnetwork:InboundNatRules.List |
| microsoft.network | microsoft.network/loadbalancers/loadbalancingrules | 1 | resource-group | arm-envelope | armnetwork:LoadBalancerLoadBalancingRules.List |
| microsoft.network | microsoft.network/loadbalancers/outboundrules | 1 | resource-group | arm-envelope | armnetwork:LoadBalancerOutboundRules.List |
| microsoft.network | microsoft.network/loadbalancers/probes | 1 | resource-group | arm-envelope | armnetwork:LoadBalancerProbes.List |
| microsoft.network | microsoft.network/networkexperimentprofiles/experiments | 1 | resource-group | item-write | armfrontdoor:Experiments.ListByProfile |
| microsoft.network | microsoft.network/networkinterfaces/ipconfigurations | 1 | resource-group | arm-envelope | armnetwork:InterfaceIPConfigurations.List |
| microsoft.network | microsoft.network/networkinterfaces/tapconfigurations | 1 | resource-group | item-write | armnetwork:InterfaceTapConfigurations.List |
| microsoft.network | microsoft.network/networkmanagers/commits | 1 | resource-group | item-write | armnetwork:Commits.List |
| microsoft.network | microsoft.network/networkmanagers/connectivityconfigurations | 1 | resource-group | item-write | armnetwork:ConnectivityConfigurations.List |
| microsoft.network | microsoft.network/networkmanagers/ipampools | 1 | resource-group | item-write | armnetwork:IpamPools.List |
| microsoft.network | microsoft.network/networkmanagers/ipampools/staticcidrs | 2 | resource-group | item-write | armnetwork:StaticCidrs.List |
| microsoft.network | microsoft.network/networkmanagers/networkgroups | 1 | resource-group | item-write | armnetwork:Groups.List |
| microsoft.network | microsoft.network/networkmanagers/networkgroups/staticmembers | 2 | resource-group | item-write | armnetwork:StaticMembers.List |
| microsoft.network | microsoft.network/networkmanagers/routingconfigurations | 1 | resource-group | item-write | armnetwork:ManagerRoutingConfigurations.List |
| microsoft.network | microsoft.network/networkmanagers/routingconfigurations/rulecollections | 2 | resource-group | item-write | armnetwork:RoutingRuleCollections.List |
| microsoft.network | microsoft.network/networkmanagers/routingconfigurations/rulecollections/rules | 3 | resource-group | item-write | armnetwork:RoutingRules.List |
| microsoft.network | microsoft.network/networkmanagers/scopeconnections | 1 | resource-group | item-write | armnetwork:ScopeConnections.List |
| microsoft.network | microsoft.network/networkmanagers/securityadminconfigurations | 1 | resource-group | item-write | armnetwork:SecurityAdminConfigurations.List |
| microsoft.network | microsoft.network/networkmanagers/securityadminconfigurations/rulecollections | 2 | resource-group | item-write | armnetwork:AdminRuleCollections.List |
| microsoft.network | microsoft.network/networkmanagers/securityadminconfigurations/rulecollections/rules | 3 | resource-group | item-write | armnetwork:AdminRules.List |
| microsoft.network | microsoft.network/networkmanagers/securityuserconfigurations | 1 | resource-group | item-write | armnetwork:SecurityUserConfigurations.List |
| microsoft.network | microsoft.network/networkmanagers/securityuserconfigurations/rulecollections | 2 | resource-group | item-write | armnetwork:SecurityUserRuleCollections.List |
| microsoft.network | microsoft.network/networkmanagers/securityuserconfigurations/rulecollections/rules | 3 | resource-group | item-write | armnetwork:SecurityUserRules.List |
| microsoft.network | microsoft.network/networkmanagers/verifierworkspaces | 1 | resource-group | item-write | armnetwork:VerifierWorkspaces.List |
| microsoft.network | microsoft.network/networkmanagers/verifierworkspaces/reachabilityanalysisintents | 2 | resource-group | item-write | armnetwork:ReachabilityAnalysisIntents.List |
| microsoft.network | microsoft.network/networkmanagers/verifierworkspaces/reachabilityanalysisruns | 2 | resource-group | item-write | armnetwork:ReachabilityAnalysisRuns.List |
| microsoft.network | microsoft.network/networksecuritygroups/defaultsecurityrules | 1 | resource-group | arm-envelope | armnetwork:DefaultSecurityRules.List |
| microsoft.network | microsoft.network/networksecuritygroups/securityrules | 1 | resource-group | item-write | armnetwork:SecurityRules.List |
| microsoft.network | microsoft.network/networksecurityperimeters | 0 | subscription | item-write | armnetwork:SecurityPerimeters.List, armnetwork:SecurityPerimeters.ListBySubscription |
| microsoft.network | microsoft.network/networksecurityperimeters/linkreferences | 1 | resource-group | item-write | armnetwork:SecurityPerimeterLinkReferences.List |
| microsoft.network | microsoft.network/networksecurityperimeters/links | 1 | resource-group | item-write | armnetwork:SecurityPerimeterLinks.List |
| microsoft.network | microsoft.network/networksecurityperimeters/loggingconfigurations | 1 | resource-group | item-write | armnetwork:SecurityPerimeterLoggingConfigurations.List |
| microsoft.network | microsoft.network/networksecurityperimeters/profiles | 1 | resource-group | item-write | armnetwork:SecurityPerimeterProfiles.List |
| microsoft.network | microsoft.network/networksecurityperimeters/profiles/accessrules | 2 | resource-group | item-write | armnetwork:SecurityPerimeterAccessRules.List |
| microsoft.network | microsoft.network/networksecurityperimeters/resourceassociations | 1 | resource-group | item-write | armnetwork:SecurityPerimeterAssociations.List |
| microsoft.network | microsoft.network/networkvirtualappliances/networkvirtualapplianceconnections | 1 | resource-group | item-write | armnetwork:VirtualApplianceConnections.List |
| microsoft.network | microsoft.network/networkvirtualappliances/virtualappliancesites | 1 | resource-group | item-write | armnetwork:VirtualApplianceSites.List |
| microsoft.network | microsoft.network/networkvirtualapplianceskus | 0 | subscription | arm-envelope | armnetwork:VirtualApplianceSKUs.List |
| microsoft.network | microsoft.network/networkwatchers/connectionanalyzers | 1 | resource-group | item-write | armnetwork:Watchers.ConnectionAnalyzersList |
| microsoft.network | microsoft.network/networkwatchers/connectionmonitors | 1 | resource-group | item-write | armnetwork:ConnectionMonitors.List |
| microsoft.network | microsoft.network/networkwatchers/flowlogs | 1 | resource-group | item-write | armnetwork:FlowLogs.List |
| microsoft.network | microsoft.network/networkwatchers/packetcaptures | 1 | resource-group | item-write | armnetwork:PacketCaptures.List |
| microsoft.network | microsoft.network/privateendpoints/privatednszonegroups | 1 | resource-group | item-write | armnetwork:PrivateDNSZoneGroups.List |
| microsoft.network | microsoft.network/privatelinkservices/privateendpointconnections | 1 | resource-group | item-write | armnetwork:PrivateLinkServices.ListPrivateEndpointConnections |
| microsoft.network | microsoft.network/routefilters/routefilterrules | 1 | resource-group | item-write | armnetwork:RouteFilterRules.ListByRouteFilter |
| microsoft.network | microsoft.network/routetables/routes | 1 | resource-group | item-write | armnetwork:Routes.List |
| microsoft.network | microsoft.network/serviceendpointpolicies/serviceendpointpolicydefinitions | 1 | resource-group | item-write | armnetwork:ServiceEndpointPolicyDefinitions.ListByResourceGroup |
| microsoft.network | microsoft.network/servicegateways | 0 | subscription | item-write | armnetwork:ServiceGateways.List, armnetwork:ServiceGateways.ListAll |
| microsoft.network | microsoft.network/virtualhubs/bgpconnections | 1 | resource-group | item-write | armnetwork:VirtualHubBgpConnections.List |
| microsoft.network | microsoft.network/virtualhubs/connectionpolicies | 1 | resource-group | item-write | armnetwork:ConnectionPolicies.List |
| microsoft.network | microsoft.network/virtualhubs/hubroutetables | 1 | resource-group | item-write | armnetwork:HubRouteTables.List |
| microsoft.network | microsoft.network/virtualhubs/hubvirtualnetworkconnections | 1 | resource-group | item-write | armnetwork:HubVirtualNetworkConnections.List |
| microsoft.network | microsoft.network/virtualhubs/ipconfigurations | 1 | resource-group | item-write | armnetwork:VirtualHubIPConfiguration.List |
| microsoft.network | microsoft.network/virtualhubs/routemaps | 1 | resource-group | item-write | armnetwork:RouteMaps.List |
| microsoft.network | microsoft.network/virtualhubs/routetables | 1 | resource-group | item-write | armnetwork:VirtualHubRouteTableV2S.List |
| microsoft.network | microsoft.network/virtualhubs/routingintent | 1 | resource-group | item-write | armnetwork:RoutingIntent.List |
| microsoft.network | microsoft.network/virtualnetworkappliances | 0 | subscription | item-write | armnetwork:VirtualNetworkAppliances.List, armnetwork:VirtualNetworkAppliances.ListAll |
| microsoft.network | microsoft.network/virtualnetworkgateways/natrules | 1 | resource-group | item-write | armnetwork:VirtualNetworkGatewayNatRules.ListByVirtualNetworkGateway |
| microsoft.network | microsoft.network/virtualnetworks/virtualnetworkpeerings | 1 | resource-group | item-write | armnetwork:VirtualNetworkPeerings.List |
| microsoft.network | microsoft.network/virtualrouters/peerings | 1 | resource-group | item-write | armnetwork:VirtualRouterPeerings.List |
| microsoft.network | microsoft.network/vpngateways/natrules | 1 | resource-group | item-write | armnetwork:NatRules.ListByVPNGateway |
| microsoft.network | microsoft.network/vpngateways/vpnconnections | 1 | resource-group | item-write | armnetwork:VPNConnections.ListByVPNGateway |
| microsoft.network | microsoft.network/vpngateways/vpnconnections/vpnlinkconnections | 2 | resource-group | arm-envelope | armnetwork:VPNLinkConnections.ListByVPNConnection |
| microsoft.network | microsoft.network/vpngateways/vpnconnections/vpnlinkconnections/sharedkeys | 3 | resource-group | item-write | armnetwork:VPNLinkConnections.GetAllSharedKeys |
| microsoft.network | microsoft.network/vpnserverconfigurations/configurationpolicygroups | 1 | resource-group | item-write | armnetwork:ConfigurationPolicyGroups.ListByVPNServerConfiguration |
| microsoft.network | microsoft.network/vpnsites/vpnsitelinks | 1 | resource-group | arm-envelope | armnetwork:VPNSiteLinks.ListByVPNSite |
| microsoft.networkanalytics | microsoft.networkanalytics/dataproducts | 0 | subscription | item-write | armnetworkanalytics:DataProducts.ListByResourceGroup, armnetworkanalytics:DataProducts.ListBySubscription |
| microsoft.networkanalytics | microsoft.networkanalytics/dataproducts/datatypes | 1 | resource-group | item-write | armnetworkanalytics:DataTypes.ListByDataProduct |
| microsoft.networkanalytics | microsoft.networkanalytics/dataproductscatalogs | 0 | subscription | arm-envelope | armnetworkanalytics:DataProductsCatalogs.ListByResourceGroup, armnetworkanalytics:DataProductsCatalogs.ListBySubscription |
| microsoft.networkcloud | microsoft.networkcloud/accessbridges | 0 | subscription | item-write | armnetworkcloud:AccessBridges.ListByResourceGroup, armnetworkcloud:AccessBridges.ListBySubscription |
| microsoft.networkcloud | microsoft.networkcloud/clusters/baremetalmachinekeysets | 1 | resource-group | item-write | armnetworkcloud:BareMetalMachineKeySets.ListByCluster |
| microsoft.networkcloud | microsoft.networkcloud/clusters/bmckeysets | 1 | resource-group | item-write | armnetworkcloud:BmcKeySets.ListByCluster |
| microsoft.networkcloud | microsoft.networkcloud/clusters/metricsconfigurations | 1 | resource-group | item-write | armnetworkcloud:MetricsConfigurations.ListByCluster |
| microsoft.networkcloud | microsoft.networkcloud/kubernetesclusters/agentpools | 1 | resource-group | item-write | armnetworkcloud:AgentPools.ListByKubernetesCluster |
| microsoft.networkcloud | microsoft.networkcloud/kubernetesclusters/features | 1 | resource-group | item-write | armnetworkcloud:KubernetesClusterFeatures.ListByKubernetesCluster |
| microsoft.networkcloud | microsoft.networkcloud/kubernetesversions | 0 | subscription | item-write | armnetworkcloud:KubernetesVersions.ListByResourceGroup, armnetworkcloud:KubernetesVersions.ListBySubscription |
| microsoft.networkcloud | microsoft.networkcloud/virtualmachines/consoles | 1 | resource-group | item-write | armnetworkcloud:Consoles.ListByVirtualMachine |
| microsoft.networkfunction | microsoft.networkfunction/azuretrafficcollectors/collectorpolicies | 1 | resource-group | item-write | armnetworkfunction:CollectorPolicies.List |
| microsoft.notificationhubs | microsoft.notificationhubs/namespaces/authorizationrules | 1 | resource-group | item-write | armnotificationhubs:Namespaces.ListAuthorizationRules |
| microsoft.notificationhubs | microsoft.notificationhubs/namespaces/notificationhubs | 1 | resource-group | item-write | armnotificationhubs:Client.List |
| microsoft.notificationhubs | microsoft.notificationhubs/namespaces/notificationhubs/authorizationrules | 2 | resource-group | item-write | armnotificationhubs:Client.ListAuthorizationRules |
| microsoft.notificationhubs | microsoft.notificationhubs/namespaces/privateendpointconnections | 1 | resource-group | item-write | armnotificationhubs:PrivateEndpointConnections.List |
| microsoft.notificationhubs | microsoft.notificationhubs/namespaces/privatelinkresources | 1 | resource-group | arm-envelope | armnotificationhubs:PrivateEndpointConnections.ListGroupIDs |
| microsoft.offazurespringboot | microsoft.offazurespringboot/springbootsites/errorsummaries | 1 | resource-group | arm-envelope | armspringappdiscovery:ErrorSummaries.ListBySite |
| microsoft.offazurespringboot | microsoft.offazurespringboot/springbootsites/springbootapps | 1 | subscription | item-write | armspringappdiscovery:Springbootapps.ListByResourceGroup, armspringappdiscovery:Springbootapps.ListBySubscription |
| microsoft.offazurespringboot | microsoft.offazurespringboot/springbootsites/springbootservers | 1 | subscription | item-write | armspringappdiscovery:Springbootservers.ListByResourceGroup, armspringappdiscovery:Springbootservers.ListBySubscription |
| microsoft.offazurespringboot | microsoft.offazurespringboot/springbootsites/summaries | 1 | resource-group | arm-envelope | armspringappdiscovery:Summaries.ListBySite |
| microsoft.openenergyplatform | microsoft.openenergyplatform/energyservices | 0 | subscription | item-write | armoep:EnergyServices.ListByResourceGroup, armoep:EnergyServices.ListBySubscription |
| microsoft.operationalinsights | microsoft.operationalinsights/querypacks | 0 | subscription | item-write | armoperationalinsights:QueryPacks.List, armoperationalinsights:QueryPacks.ListByResourceGroup |
| microsoft.operationalinsights | microsoft.operationalinsights/querypacks/queries | 1 | resource-group | item-write | armoperationalinsights:Queries.List |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/dataexports | 1 | resource-group | item-write | armoperationalinsights:DataExports.ListByWorkspace |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/datasources | 1 | resource-group | item-write | armoperationalinsights:DataSources.ListByWorkspace |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/linkedservices | 1 | resource-group | item-write | armoperationalinsights:LinkedServices.ListByWorkspace |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/linkedstorageaccounts | 1 | resource-group | item-write | armoperationalinsights:LinkedStorageAccounts.ListByWorkspace |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armoperationalinsights:Workspaces.ListNSP |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/savedsearches | 1 | resource-group | item-write | armoperationalinsights:SavedSearches.ListByWorkspace |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/storageinsightconfigs | 1 | resource-group | item-write | armoperationalinsights:StorageInsightConfigs.ListByWorkspace |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/summarylogs | 1 | resource-group | item-write | armoperationalinsights:SummaryLogs.ListByWorkspace |
| microsoft.operationalinsights | microsoft.operationalinsights/workspaces/tables | 1 | resource-group | item-write | armoperationalinsights:Tables.ListByWorkspace |
| microsoft.operationsmanagement | microsoft.operationsmanagement/managementassociations | 0 | subscription | item-write | armoperationsmanagement:ManagementAssociations.ListBySubscription |
| microsoft.operationsmanagement | microsoft.operationsmanagement/managementconfigurations | 0 | subscription | item-write | armoperationsmanagement:ManagementConfigurations.ListBySubscription |
| microsoft.orbital | microsoft.orbital/contactprofiles | 0 | subscription | item-write | armorbital:ContactProfiles.List, armorbital:ContactProfiles.ListBySubscription |
| microsoft.orbital | microsoft.orbital/spacecrafts | 0 | subscription | item-write | armorbital:Spacecrafts.List, armorbital:Spacecrafts.ListBySubscription |
| microsoft.orbital | microsoft.orbital/spacecrafts/contacts | 1 | resource-group | item-write | armorbital:Contacts.List |
| microsoft.peering | microsoft.peering/peerings/registeredasns | 1 | resource-group | item-write | armpeering:RegisteredAsns.ListByPeering |
| microsoft.peering | microsoft.peering/peerings/registeredprefixes | 1 | resource-group | item-write | armpeering:RegisteredPrefixes.ListByPeering |
| microsoft.peering | microsoft.peering/peeringservices/connectionmonitortests | 1 | resource-group | item-write | armpeering:ConnectionMonitorTests.ListByPeeringService |
| microsoft.peering | microsoft.peering/peeringservices/prefixes | 1 | resource-group | item-write | armpeering:Prefixes.ListByPeeringService |
| microsoft.portal | microsoft.portal/dashboards | 0 | subscription | item-write | armportal:Dashboards.ListByResourceGroup, armportal:Dashboards.ListBySubscription |
| microsoft.portal | microsoft.portal/tenantconfigurations | 0 | tenant | item-write | armportal:TenantConfigurations.List |
| microsoft.powerbi | microsoft.powerbi/privatelinkservicesforpowerbi | 0 | subscription | item-write | armpowerbiprivatelinks:PrivateLinkServices.ListByResourceGroup, armpowerbiprivatelinks:PrivateLinkServicesForPowerBI.ListBySubscriptionID |
| microsoft.powerbi | microsoft.powerbi/privatelinkservicesforpowerbi/privateendpointconnections | 1 | resource-group | item-write | armpowerbiprivatelinks:PrivateEndpointConnections.ListByResource |
| microsoft.powerbi | microsoft.powerbi/privatelinkservicesforpowerbi/privatelinkresources | 1 | resource-group | arm-envelope | armpowerbiprivatelinks:PrivateLinkResources.ListByResource |
| microsoft.powerbi | microsoft.powerbi/workspacecollections | 0 | subscription | item-write | armpowerbiembedded:WorkspaceCollections.ListByResourceGroup, armpowerbiembedded:WorkspaceCollections.ListBySubscription |
| microsoft.powerplatform | microsoft.powerplatform/enterprisepolicies/privateendpointconnections | 1 | resource-group | item-write | armpowerplatform:PrivateEndpointConnections.ListByEnterprisePolicy |
| microsoft.powerplatform | microsoft.powerplatform/enterprisepolicies/privatelinkresources | 1 | resource-group | arm-envelope | armpowerplatform:PrivateLinkResources.ListByEnterprisePolicy |
| microsoft.programenrollment | microsoft.programenrollment/eduenrollments | 0 | subscription | item-write | armprogramenrollment:EduEnrollments.ListByResourceGroup, armprogramenrollment:EduEnrollments.ListBySubscription |
| microsoft.programmableconnectivity | microsoft.programmableconnectivity/gateways | 0 | subscription | item-write | armprogrammableconnectivity:Gateways.ListByResourceGroup, armprogrammableconnectivity:Gateways.ListBySubscription |
| microsoft.programmableconnectivity | microsoft.programmableconnectivity/operatorapiconnections | 0 | subscription | item-write | armprogrammableconnectivity:OperatorAPIConnections.ListByResourceGroup, armprogrammableconnectivity:OperatorAPIConnections.ListBySubscription |
| microsoft.programmableconnectivity | microsoft.programmableconnectivity/operatorapiplans | 0 | subscription | arm-envelope | armprogrammableconnectivity:OperatorAPIPlans.ListBySubscription |
| microsoft.providerhub | microsoft.providerhub/providermonitorsettings | 0 | subscription | item-write | armproviderhub:ProviderMonitorSettings.ListByResourceGroup, armproviderhub:ProviderMonitorSettings.ListBySubscription |
| microsoft.providerhub | microsoft.providerhub/providerregistrations | 0 | subscription | item-write | armproviderhub:ProviderRegistrations.List |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/authorizedapplications | 1 | subscription | item-write | armproviderhub:AuthorizedApplications.List |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/customrollouts | 1 | subscription | item-write | armproviderhub:CustomRollouts.ListByProviderRegistration |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/defaultrollouts | 1 | subscription | item-write | armproviderhub:DefaultRollouts.ListByProviderRegistration |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/notificationregistrations | 1 | subscription | item-write | armproviderhub:NotificationRegistrations.ListByProviderRegistration |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/resourcetyperegistrations | 1 | subscription | item-write | armproviderhub:ResourceTypeRegistrations.ListByProviderRegistration |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/resourcetyperegistrations/resourcetyperegistrations/resourcetyperegistrations/resourcetyperegistrations/skus | 5 | subscription | item-write | armproviderhub:SKUs.ListByResourceTypeRegistrationsNestedResourceTypeThird |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/resourcetyperegistrations/resourcetyperegistrations/resourcetyperegistrations/skus | 4 | subscription | item-write | armproviderhub:SKUs.ListByResourceTypeRegistrationsNestedResourceTypeSecond |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/resourcetyperegistrations/resourcetyperegistrations/skus | 3 | subscription | item-write | armproviderhub:SKUs.ListByResourceTypeRegistrationsNestedResourceTypeFirst |
| microsoft.providerhub | microsoft.providerhub/providerregistrations/resourcetyperegistrations/skus | 2 | subscription | item-write | armproviderhub:SKUs.ListByResourceTypeRegistrations |
| microsoft.purview | microsoft.purview/accounts/kafkaconfigurations | 1 | resource-group | item-write | armpurview:KafkaConfigurations.ListByAccount |
| microsoft.purview | microsoft.purview/accounts/privateendpointconnections | 1 | resource-group | item-write | armpurview:IngestionPrivateEndpointConnections.List, armpurview:PrivateEndpointConnections.ListByAccount |
| microsoft.purview | microsoft.purview/accounts/privatelinkresources | 1 | resource-group | arm-envelope | armpurview:PrivateLinkResources.ListByAccount |
| microsoft.quota | microsoft.quota/groupquotas | 0 | management-group | item-write | armquota:GroupQuotas.List |
| microsoft.quota | microsoft.quota/groupquotas/resourceproviders/quotaallocationrequests | 2 | subscription | arm-envelope | armquota:GroupQuotaSubscriptionAllocationRequest.List |
| microsoft.quota | microsoft.quota/groupquotas/subscriptionrequests | 1 | management-group | arm-envelope | armquota:GroupQuotaSubscriptionRequests.List |
| microsoft.quota | microsoft.quota/groupquotas/subscriptions | 1 | management-group | item-write | armquota:GroupQuotaSubscriptions.List |
| microsoft.recoveryservices | microsoft.recoveryservices/replicationeligibilityresults | 0 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ReplicationEligibilityResults.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/backupengines | 1 | resource-group | arm-envelope | armrecoveryservicesbackup:BackupEngines.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/backupfabrics/protectioncontainers/protecteditems/recoverypoints | 4 | resource-group | arm-envelope | armrecoveryservicesbackup:RecoveryPoints.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/backupjobs | 1 | resource-group | arm-envelope | armrecoveryservicesbackup:BackupJobs.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/backuppolicies | 1 | resource-group | item-write | armrecoveryservicesbackup:BackupPolicies.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/backupresourceguardproxies | 1 | resource-group | item-write | armrecoveryservicesbackup:ResourceGuardProxies.Get |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/privatelinkresources | 1 | resource-group | arm-envelope | armrecoveryservices:PrivateLinkResources.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationalertsettings | 1 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationAlertSettings.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationevents | 1 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ReplicationEvents.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics | 1 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationFabrics.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationlogicalnetworks | 2 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ReplicationLogicalNetworks.ListByReplicationFabrics |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationnetworks | 2 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ReplicationNetworks.ListByReplicationFabrics |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationnetworks/replicationnetworkmappings | 3 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationNetworkMappings.List, armrecoveryservicessiterecovery:ReplicationNetworkMappings.ListByReplicationNetworks |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers | 2 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationProtectionContainers.List, armrecoveryservicessiterecovery:ReplicationProtectionContainers.ListByReplicationFabrics |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationmigrationitems | 3 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationMigrationItems.List, armrecoveryservicessiterecovery:ReplicationMigrationItems.ListByReplicationProtectionContainers |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationmigrationitems/migrationrecoverypoints | 4 | resource-group | arm-envelope | armrecoveryservicessiterecovery:MigrationRecoveryPoints.ListByReplicationMigrationItems |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationprotectableitems | 3 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ReplicationProtectableItems.ListByReplicationProtectionContainers |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationprotecteditems | 3 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationProtectedItems.List, armrecoveryservicessiterecovery:ReplicationProtectedItems.ListByReplicationProtectionContainers |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationprotecteditems/recoverypoints | 4 | resource-group | arm-envelope | armrecoveryservicessiterecovery:RecoveryPoints.ListByReplicationProtectedItems |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationprotectionclusters | 3 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationProtectionClusters.List, armrecoveryservicessiterecovery:ReplicationProtectionClusters.ListByReplicationProtectionContainers |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationprotectionclusters/recoverypoints | 4 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ClusterRecoveryPoints.ListByReplicationProtectionCluster |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationprotectioncontainers/replicationprotectioncontainermappings | 3 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationProtectionContainerMappings.List, armrecoveryservicessiterecovery:ReplicationProtectionContainerMappings.ListByReplicationProtectionContainers |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationrecoveryservicesproviders | 2 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationRecoveryServicesProviders.List, armrecoveryservicessiterecovery:ReplicationRecoveryServicesProviders.ListByReplicationFabrics |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationstorageclassifications | 2 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ReplicationStorageClassifications.ListByReplicationFabrics |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationstorageclassifications/replicationstorageclassificationmappings | 3 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationStorageClassificationMappings.List, armrecoveryservicessiterecovery:ReplicationStorageClassificationMappings.ListByReplicationStorageClassifications |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationfabrics/replicationvcenters | 2 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationvCenters.List, armrecoveryservicessiterecovery:ReplicationvCenters.ListByReplicationFabrics |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationjobs | 1 | resource-group | arm-envelope | armrecoveryservicessiterecovery:ReplicationJobs.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationpolicies | 1 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationPolicies.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationprotectionintents | 1 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationProtectionIntents.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationrecoveryplans | 1 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationRecoveryPlans.List |
| microsoft.recoveryservices | microsoft.recoveryservices/vaults/replicationvaultsettings | 1 | resource-group | item-write | armrecoveryservicessiterecovery:ReplicationVaultSetting.List |
| microsoft.redhatopenshift | microsoft.redhatopenshift/hcpopenshiftclusters | 0 | subscription | item-write | armredhatopenshifthcp:HcpOpenShiftClusters.ListByResourceGroup, armredhatopenshifthcp:HcpOpenShiftClusters.ListBySubscription |
| microsoft.redhatopenshift | microsoft.redhatopenshift/hcpopenshiftclusters/externalauths | 1 | resource-group | item-write | armredhatopenshifthcp:ExternalAuths.ListByParent |
| microsoft.redhatopenshift | microsoft.redhatopenshift/hcpopenshiftclusters/nodepools | 1 | resource-group | item-write | armredhatopenshifthcp:NodePools.ListByParent |
| microsoft.relay | microsoft.relay/namespaces/authorizationrules | 1 | resource-group | item-write | armrelay:Namespaces.ListAuthorizationRules |
| microsoft.relay | microsoft.relay/namespaces/hybridconnections | 1 | resource-group | item-write | armrelay:HybridConnections.ListByNamespace |
| microsoft.relay | microsoft.relay/namespaces/hybridconnections/authorizationrules | 2 | resource-group | item-write | armrelay:HybridConnections.ListAuthorizationRules |
| microsoft.relay | microsoft.relay/namespaces/privateendpointconnections | 1 | resource-group | item-write | armrelay:PrivateEndpointConnections.List |
| microsoft.relay | microsoft.relay/namespaces/privatelinkresources | 1 | resource-group | arm-envelope | armrelay:PrivateLinkResources.List |
| microsoft.relay | microsoft.relay/namespaces/wcfrelays | 1 | resource-group | item-write | armrelay:WCFRelays.ListByNamespace |
| microsoft.relay | microsoft.relay/namespaces/wcfrelays/authorizationrules | 2 | resource-group | item-write | armrelay:WCFRelays.ListAuthorizationRules |
| microsoft.resourcegraph | microsoft.resourcegraph/queries | 0 | subscription | item-write | armresourcegraph:GraphQuery.List, armresourcegraph:GraphQuery.ListBySubscription |
| microsoft.resourcehealth | microsoft.resourcehealth/events/impactedresources | 1 | tenant | arm-envelope | armresourcehealth:ImpactedResources.ListBySubscriptionIDAndEventID, armresourcehealth:ImpactedResources.ListByTenantIDAndEventID |
| microsoft.resources | microsoft.resources/builtintemplatespecs/versions | 1 | tenant | arm-envelope | armtemplatespecs:TemplateSpecVersions.ListBuiltIns |
| microsoft.resources | microsoft.resources/deployments | 0 | extension | item-write | armdeployments:Deployments.ListAtManagementGroupScope, armdeployments:Deployments.ListAtScope, armdeployments:Deployments.ListAtSubscriptionScope, armdeployments:Deployments.ListAtTenantScope, armdeployments:Deployments.ListByResourceGroup |
| microsoft.resources | microsoft.resources/deploymentscripts | 0 | subscription | item-write | armdeploymentscripts:Client.ListByResourceGroup, armdeploymentscripts:Client.ListBySubscription |
| microsoft.resources | microsoft.resources/deploymentscripts/logs | 1 | resource-group | arm-envelope | armdeploymentscripts:Client.GetLogs |
| microsoft.resources | microsoft.resources/deploymentstacks | 0 | management-group | item-write | armdeploymentstacks:Client.ListAtManagementGroup, armdeploymentstacks:Client.ListAtResourceGroup, armdeploymentstacks:Client.ListAtSubscription |
| microsoft.resources | microsoft.resources/deploymentstackswhatifresults | 0 | management-group | item-write | armdeploymentstacks:WhatIfResultsAtManagementGroup.List, armdeploymentstacks:WhatIfResultsAtResourceGroup.List, armdeploymentstacks:WhatIfResultsAtSubscription.List |
| microsoft.resources | microsoft.resources/tagnames | 0 | subscription | item-write | armresources:Tags.List |
| microsoft.resources | microsoft.resources/templatespecs | 0 | subscription | item-write | armtemplatespecs:Client.ListByResourceGroup, armtemplatespecs:Client.ListBySubscription |
| microsoft.resources | microsoft.resources/templatespecs/versions | 1 | resource-group | item-write | armtemplatespecs:TemplateSpecVersions.List |
| microsoft.saas | microsoft.saas/saasresources | 0 | tenant | item-write | armsaas:Resources.List |
| microsoft.scheduler | microsoft.scheduler/jobcollections | 0 | subscription | item-write | armscheduler:JobCollections.ListByResourceGroup, armscheduler:JobCollections.ListBySubscription |
| microsoft.scheduler | microsoft.scheduler/jobcollections/jobs | 1 | resource-group | item-write | armscheduler:Jobs.List |
| microsoft.scvmm | microsoft.scvmm/virtualmachineinstances | 0 | extension | item-write | armscvmm:VirtualMachineInstances.List |
| microsoft.scvmm | microsoft.scvmm/virtualmachineinstances/guestagents | 1 | extension | item-write | armscvmm:GuestAgents.ListByVirtualMachineInstance |
| microsoft.scvmm | microsoft.scvmm/virtualmachineinstances/hybrididentitymetadata | 1 | extension | arm-envelope | armscvmm:VMInstanceHybridIdentityMetadatas.ListByVirtualMachineInstance |
| microsoft.scvmm | microsoft.scvmm/vmmservers/inventoryitems | 1 | resource-group | item-write | armscvmm:InventoryItems.ListByVmmServer |
| microsoft.search | microsoft.search/searchservices/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armsearch:NetworkSecurityPerimeterConfigurations.ListByService |
| microsoft.search | microsoft.search/searchservices/privateendpointconnections | 1 | resource-group | item-write | armsearch:PrivateEndpointConnections.ListByService |
| microsoft.search | microsoft.search/searchservices/sharedprivatelinkresources | 1 | resource-group | item-write | armsearch:SharedPrivateLinkResources.ListByService |
| microsoft.security | microsoft.security/alerts | 0 | subscription | arm-envelope | armsecurity:Alerts.List, armsecurity:Alerts.ListByResourceGroup, armsecurity:Alerts.ListResourceGroupLevelByRegion, armsecurity:Alerts.ListSubscriptionLevelByRegion |
| microsoft.security | microsoft.security/alertssuppressionrules | 0 | subscription | item-write | armsecurity:AlertsSuppressionRules.List |
| microsoft.security | microsoft.security/allowedconnections | 0 | subscription | arm-envelope | armsecurity:AllowedConnections.List, armsecurity:AllowedConnections.ListByHomeRegion |
| microsoft.security | microsoft.security/apicollections | 0 | subscription | item-write | armsecurity:APICollections.ListByAzureAPIManagementService, armsecurity:APICollections.ListByResourceGroup, armsecurity:APICollections.ListBySubscription |
| microsoft.security | microsoft.security/applications | 0 | subscription | item-write | armsecurity:Applications.List, armsecurity:ConnectorApplications.List |
| microsoft.security | microsoft.security/assessmentmetadata | 0 | tenant | item-write | armsecurity:AssessmentsMetadata.List, armsecurity:AssessmentsMetadata.ListBySubscription |
| microsoft.security | microsoft.security/assessments | 0 | extension | item-write | armsecurity:Assessments.List |
| microsoft.security | microsoft.security/assessments/governanceassignments | 1 | extension | item-write | armsecurity:GovernanceAssignments.List |
| microsoft.security | microsoft.security/assessments/subassessments | 1 | extension | arm-envelope | armsecurity:SubAssessments.List |
| microsoft.security | microsoft.security/assignments | 0 | subscription | item-write | armsecurity:Assignments.List, armsecurity:Assignments.ListBySubscription |
| microsoft.security | microsoft.security/automations | 0 | subscription | item-write | armsecurity:Automations.List, armsecurity:Automations.ListByResourceGroup |
| microsoft.security | microsoft.security/autoprovisioningsettings | 0 | subscription | item-write | armsecurity:AutoProvisioningSettings.List |
| microsoft.security | microsoft.security/customrecommendations | 0 | extension | item-write | armsecurity:CustomRecommendations.List |
| microsoft.security | microsoft.security/defenderforstoragesettings | 0 | extension | item-write | armsecurity:DefenderForStorage.List |
| microsoft.security | microsoft.security/devicesecuritygroups | 0 | extension | item-write | armsecurity:DeviceSecurityGroups.List |
| microsoft.security | microsoft.security/discoveredsecuritysolutions | 0 | subscription | arm-envelope | armsecurity:DiscoveredSecuritySolutions.List, armsecurity:DiscoveredSecuritySolutions.ListByHomeRegion |
| microsoft.security | microsoft.security/externalsecuritysolutions | 0 | subscription | arm-envelope | armsecurity:ExternalSecuritySolutions.List, armsecurity:ExternalSecuritySolutions.ListByHomeRegion |
| microsoft.security | microsoft.security/governancerules | 0 | extension | item-write | armsecurity:GovernanceRules.List |
| microsoft.security | microsoft.security/informationprotectionpolicies | 0 | extension | item-write | armsecurity:InformationProtectionPolicies.List |
| microsoft.security | microsoft.security/iotsecuritysolutions | 0 | subscription | item-write | armsecurity:IotSecuritySolution.ListByResourceGroup, armsecurity:IotSecuritySolution.ListBySubscription |
| microsoft.security | microsoft.security/iotsecuritysolutions/analyticsmodels | 1 | resource-group | arm-envelope | armsecurity:IotSecuritySolutionAnalytics.List |
| microsoft.security | microsoft.security/iotsecuritysolutions/analyticsmodels/aggregatedalerts | 2 | resource-group | arm-envelope | armsecurity:IotSecuritySolutionsAnalyticsAggregatedAlert.List |
| microsoft.security | microsoft.security/iotsecuritysolutions/analyticsmodels/aggregatedrecommendations | 2 | resource-group | arm-envelope | armsecurity:IotSecuritySolutionsAnalyticsRecommendation.List |
| microsoft.security | microsoft.security/jitnetworkaccesspolicies | 0 | subscription | item-write | armsecurity:JitNetworkAccessPolicies.List, armsecurity:JitNetworkAccessPolicies.ListByRegion, armsecurity:JitNetworkAccessPolicies.ListByResourceGroup, armsecurity:JitNetworkAccessPolicies.ListByResourceGroupAndRegion |
| microsoft.security | microsoft.security/locations | 0 | subscription | arm-envelope | armsecurity:Locations.List |
| microsoft.security | microsoft.security/mdeonboardings | 0 | subscription | arm-envelope | armsecurity:MdeOnboardings.List |
| microsoft.security | microsoft.security/pricings/securityoperators | 1 | subscription | item-write | armsecurity:Operators.List |
| microsoft.security | microsoft.security/privatelinks | 0 | subscription | item-write | armsecurity:PrivateLinks.List, armsecurity:PrivateLinks.ListBySubscription |
| microsoft.security | microsoft.security/privatelinks/privateendpointconnections | 1 | resource-group | item-write | armsecurity:PrivateEndpointConnections.List |
| microsoft.security | microsoft.security/privatelinks/privatelinkresources | 1 | resource-group | arm-envelope | armsecurity:PrivateLinkResources.List |
| microsoft.security | microsoft.security/regulatorycompliancestandards | 0 | subscription | arm-envelope | armsecurity:RegulatoryComplianceStandards.List |
| microsoft.security | microsoft.security/regulatorycompliancestandards/regulatorycompliancecontrols | 1 | subscription | arm-envelope | armsecurity:RegulatoryComplianceControls.List |
| microsoft.security | microsoft.security/regulatorycompliancestandards/regulatorycompliancecontrols/regulatorycomplianceassessments | 2 | subscription | arm-envelope | armsecurity:RegulatoryComplianceAssessments.List |
| microsoft.security | microsoft.security/securescores | 0 | subscription | arm-envelope | armsecurity:SecureScores.List |
| microsoft.security | microsoft.security/securityconnectors | 0 | subscription | item-write | armsecurity:Connectors.List, armsecurity:Connectors.ListByResourceGroup |
| microsoft.security | microsoft.security/securityconnectors/devops | 1 | resource-group | item-write | armsecurity:DevOpsConfigurations.List |
| microsoft.security | microsoft.security/securityconnectors/devops/azuredevopsorgs | 2 | resource-group | item-write | armsecurity:AzureDevOpsOrgs.List |
| microsoft.security | microsoft.security/securityconnectors/devops/azuredevopsorgs/projects | 3 | resource-group | item-write | armsecurity:AzureDevOpsProjects.List |
| microsoft.security | microsoft.security/securityconnectors/devops/azuredevopsorgs/projects/repos | 4 | resource-group | item-write | armsecurity:AzureDevOpsRepos.List |
| microsoft.security | microsoft.security/securityconnectors/devops/githubowners | 2 | resource-group | arm-envelope | armsecurity:GitHubOwners.List |
| microsoft.security | microsoft.security/securityconnectors/devops/githubowners/repos | 3 | resource-group | arm-envelope | armsecurity:GitHubRepos.List |
| microsoft.security | microsoft.security/securityconnectors/devops/gitlabgroups | 2 | resource-group | arm-envelope | armsecurity:GitLabGroups.List |
| microsoft.security | microsoft.security/securityconnectors/devops/gitlabgroups/projects | 3 | resource-group | arm-envelope | armsecurity:GitLabProjects.List |
| microsoft.security | microsoft.security/securitycontacts | 0 | subscription | item-write | armsecurity:Contacts.List |
| microsoft.security | microsoft.security/securitysolutions | 0 | subscription | arm-envelope | armsecurity:Solutions.List |
| microsoft.security | microsoft.security/securitystandards | 0 | extension | item-write | armsecurity:ArmSecurityStandards.List |
| microsoft.security | microsoft.security/sensitivitysettings | 0 | tenant | item-write | armsecurity:SensitivitySettings.List |
| microsoft.security | microsoft.security/servervulnerabilityassessments | 0 | extension | item-write | armsecurity:ServerVulnerabilityAssessment.ListByExtendedResource |
| microsoft.security | microsoft.security/servervulnerabilityassessmentssettings | 0 | subscription | item-write | armsecurity:ServerVulnerabilityAssessmentsSettings.ListBySubscription |
| microsoft.security | microsoft.security/settings | 0 | subscription | item-write | armsecurity:Settings.List |
| microsoft.security | microsoft.security/sqlvulnerabilityassessments/baselinerules | 1 | extension | item-write | armsecurity:SQLVulnerabilityAssessmentBaselineRules.List |
| microsoft.security | microsoft.security/sqlvulnerabilityassessments/scans | 1 | extension | arm-envelope | armsecurity:SQLVulnerabilityAssessmentScans.List |
| microsoft.security | microsoft.security/sqlvulnerabilityassessments/scans/scanresults | 2 | extension | arm-envelope | armsecurity:SQLVulnerabilityAssessmentScanResults.List |
| microsoft.security | microsoft.security/standardassignments | 0 | extension | item-write | armsecurity:StandardAssignments.List |
| microsoft.security | microsoft.security/standards | 0 | subscription | item-write | armsecurity:Standards.List, armsecurity:Standards.ListBySubscription |
| microsoft.security | microsoft.security/tasks | 0 | subscription | arm-envelope | armsecurity:Tasks.List, armsecurity:Tasks.ListByHomeRegion, armsecurity:Tasks.ListByResourceGroup |
| microsoft.security | microsoft.security/topologies | 0 | subscription | arm-envelope | armsecurity:Topology.List, armsecurity:Topology.ListByHomeRegion |
| microsoft.security | microsoft.security/workspacesettings | 0 | subscription | item-write | armsecurity:WorkspaceSettings.List |
| microsoft.securitydevops | microsoft.securitydevops/azuredevopsconnectors | 0 | subscription | item-write | armsecuritydevops:AzureDevOpsConnector.ListByResourceGroup, armsecuritydevops:AzureDevOpsConnector.ListBySubscription |
| microsoft.securitydevops | microsoft.securitydevops/azuredevopsconnectors/orgs | 1 | resource-group | item-write | armsecuritydevops:AzureDevOpsOrg.List |
| microsoft.securitydevops | microsoft.securitydevops/azuredevopsconnectors/orgs/projects | 2 | resource-group | item-write | armsecuritydevops:AzureDevOpsProject.List |
| microsoft.securitydevops | microsoft.securitydevops/azuredevopsconnectors/orgs/projects/repos | 3 | resource-group | item-write | armsecuritydevops:AzureDevOpsRepo.List, armsecuritydevops:AzureDevOpsRepo.ListByConnector |
| microsoft.securitydevops | microsoft.securitydevops/githubconnectors | 0 | subscription | item-write | armsecuritydevops:GitHubConnector.ListByResourceGroup, armsecuritydevops:GitHubConnector.ListBySubscription |
| microsoft.securitydevops | microsoft.securitydevops/githubconnectors/owners | 1 | resource-group | item-write | armsecuritydevops:GitHubOwner.List |
| microsoft.securitydevops | microsoft.securitydevops/githubconnectors/owners/repos | 2 | resource-group | item-write | armsecuritydevops:GitHubRepo.List, armsecuritydevops:GitHubRepo.ListByConnector |
| microsoft.securityinsights | microsoft.securityinsights/alertrules | 0 | resource-group | item-write | armsecurityinsights:AlertRules.List |
| microsoft.securityinsights | microsoft.securityinsights/alertrules/actions | 1 | resource-group | item-write | armsecurityinsights:Actions.ListByAlertRule |
| microsoft.securityinsights | microsoft.securityinsights/alertruletemplates | 0 | resource-group | arm-envelope | armsecurityinsights:AlertRuleTemplates.List |
| microsoft.securityinsights | microsoft.securityinsights/automationrules | 0 | resource-group | item-write | armsecurityinsights:AutomationRules.List |
| microsoft.securityinsights | microsoft.securityinsights/billingstatistics | 0 | resource-group | arm-envelope | armsecurityinsights:BillingStatistics.List |
| microsoft.securityinsights | microsoft.securityinsights/bookmarks | 0 | resource-group | item-write | armsecurityinsights:Bookmarks.List |
| microsoft.securityinsights | microsoft.securityinsights/bookmarks/relations | 1 | resource-group | item-write | armsecurityinsights:BookmarkRelations.List |
| microsoft.securityinsights | microsoft.securityinsights/contentpackages | 0 | resource-group | item-write | armsecurityinsights:ContentPackages.List |
| microsoft.securityinsights | microsoft.securityinsights/contentproductpackages | 0 | resource-group | arm-envelope | armsecurityinsights:ProductPackages.List |
| microsoft.securityinsights | microsoft.securityinsights/contentproducttemplates | 0 | resource-group | arm-envelope | armsecurityinsights:ProductTemplates.List |
| microsoft.securityinsights | microsoft.securityinsights/contenttemplates | 0 | resource-group | item-write | armsecurityinsights:ContentTemplates.List |
| microsoft.securityinsights | microsoft.securityinsights/dataconnectordefinitions | 0 | resource-group | item-write | armsecurityinsights:DataConnectorDefinitions.List |
| microsoft.securityinsights | microsoft.securityinsights/dataconnectors | 0 | resource-group | item-write | armsecurityinsights:DataConnectors.List |
| microsoft.securityinsights | microsoft.securityinsights/entities | 0 | resource-group | arm-envelope | armsecurityinsights:Entities.List |
| microsoft.securityinsights | microsoft.securityinsights/entities/relations | 1 | resource-group | arm-envelope | armsecurityinsights:EntitiesRelations.List |
| microsoft.securityinsights | microsoft.securityinsights/entityqueries | 0 | resource-group | item-write | armsecurityinsights:EntityQueries.List |
| microsoft.securityinsights | microsoft.securityinsights/entityquerytemplates | 0 | resource-group | arm-envelope | armsecurityinsights:EntityQueryTemplates.List |
| microsoft.securityinsights | microsoft.securityinsights/fileimports | 0 | resource-group | item-write | armsecurityinsights:FileImports.List |
| microsoft.securityinsights | microsoft.securityinsights/hunts | 0 | resource-group | item-write | armsecurityinsights:Hunts.List |
| microsoft.securityinsights | microsoft.securityinsights/hunts/comments | 1 | resource-group | item-write | armsecurityinsights:HuntComments.List |
| microsoft.securityinsights | microsoft.securityinsights/hunts/relations | 1 | resource-group | item-write | armsecurityinsights:HuntRelations.List |
| microsoft.securityinsights | microsoft.securityinsights/incidents | 0 | resource-group | item-write | armsecurityinsights:Incidents.List |
| microsoft.securityinsights | microsoft.securityinsights/incidents/comments | 1 | resource-group | item-write | armsecurityinsights:IncidentComments.List |
| microsoft.securityinsights | microsoft.securityinsights/incidents/relations | 1 | resource-group | item-write | armsecurityinsights:IncidentRelations.List |
| microsoft.securityinsights | microsoft.securityinsights/incidents/tasks | 1 | resource-group | item-write | armsecurityinsights:IncidentTasks.List |
| microsoft.securityinsights | microsoft.securityinsights/metadata | 0 | resource-group | item-write | armsecurityinsights:Metadata.List |
| microsoft.securityinsights | microsoft.securityinsights/officeconsents | 0 | resource-group | item-write | armsecurityinsights:OfficeConsents.List |
| microsoft.securityinsights | microsoft.securityinsights/onboardingstates | 0 | resource-group | item-write | armsecurityinsights:SentinelOnboardingStates.List |
| microsoft.securityinsights | microsoft.securityinsights/recommendations | 0 | resource-group | item-write | armsecurityinsights:GetRecommendations.List |
| microsoft.securityinsights | microsoft.securityinsights/securitymlanalyticssettings | 0 | resource-group | item-write | armsecurityinsights:SecurityMLAnalyticsSettings.List |
| microsoft.securityinsights | microsoft.securityinsights/settings | 0 | resource-group | item-write | armsecurityinsights:ProductSettings.List |
| microsoft.securityinsights | microsoft.securityinsights/sourcecontrols | 0 | resource-group | item-write | armsecurityinsights:SourceControls.List |
| microsoft.securityinsights | microsoft.securityinsights/threatintelligence/indicators | 1 | resource-group | item-write | armsecurityinsights:ThreatIntelligenceIndicators.List |
| microsoft.securityinsights | microsoft.securityinsights/triggeredanalyticsruleruns | 0 | resource-group | arm-envelope | armsecurityinsights:GetTriggeredAnalyticsRuleRuns.List |
| microsoft.securityinsights | microsoft.securityinsights/watchlists | 0 | resource-group | item-write | armsecurityinsights:Watchlists.List |
| microsoft.securityinsights | microsoft.securityinsights/watchlists/watchlistitems | 1 | resource-group | item-write | armsecurityinsights:WatchlistItems.List |
| microsoft.securityinsights | microsoft.securityinsights/workspacemanagerassignments | 0 | resource-group | item-write | armsecurityinsights:WorkspaceManagerAssignments.List |
| microsoft.securityinsights | microsoft.securityinsights/workspacemanagerassignments/jobs | 1 | resource-group | item-write | armsecurityinsights:WorkspaceManagerAssignmentJobs.List |
| microsoft.securityinsights | microsoft.securityinsights/workspacemanagerconfigurations | 0 | resource-group | item-write | armsecurityinsights:WorkspaceManagerConfigurations.List |
| microsoft.securityinsights | microsoft.securityinsights/workspacemanagergroups | 0 | resource-group | item-write | armsecurityinsights:WorkspaceManagerGroups.List |
| microsoft.securityinsights | microsoft.securityinsights/workspacemanagermembers | 0 | resource-group | item-write | armsecurityinsights:WorkspaceManagerMembers.List |
| microsoft.serialconsole | microsoft.serialconsole/serialports | 0 | extension | item-write | armserialconsole:SerialPorts.List, armserialconsole:SerialPorts.ListBySubscriptions |
| microsoft.servicebus | microsoft.servicebus/namespaces/authorizationrules | 1 | resource-group | item-write | armservicebus:Namespaces.ListAuthorizationRules |
| microsoft.servicebus | microsoft.servicebus/namespaces/disasterrecoveryconfigs | 1 | resource-group | item-write | armservicebus:DisasterRecoveryConfigs.List |
| microsoft.servicebus | microsoft.servicebus/namespaces/disasterrecoveryconfigs/authorizationrules | 2 | resource-group | arm-envelope | armservicebus:DisasterRecoveryConfigs.ListAuthorizationRules |
| microsoft.servicebus | microsoft.servicebus/namespaces/migrationconfigurations | 1 | resource-group | item-write | armservicebus:MigrationConfigs.List |
| microsoft.servicebus | microsoft.servicebus/namespaces/networkrulesets | 1 | resource-group | item-write | armservicebus:Namespaces.ListNetworkRuleSets |
| microsoft.servicebus | microsoft.servicebus/namespaces/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armservicebus:NetworkSecurityPerimeterConfiguration.List |
| microsoft.servicebus | microsoft.servicebus/namespaces/privateendpointconnections | 1 | resource-group | item-write | armservicebus:PrivateEndpointConnections.List |
| microsoft.servicebus | microsoft.servicebus/namespaces/queues | 1 | resource-group | item-write | armservicebus:Queues.ListByNamespace |
| microsoft.servicebus | microsoft.servicebus/namespaces/queues/authorizationrules | 2 | resource-group | item-write | armservicebus:Queues.ListAuthorizationRules |
| microsoft.servicebus | microsoft.servicebus/namespaces/topics | 1 | resource-group | item-write | armservicebus:Topics.ListByNamespace |
| microsoft.servicebus | microsoft.servicebus/namespaces/topics/authorizationrules | 2 | resource-group | item-write | armservicebus:Topics.ListAuthorizationRules |
| microsoft.servicebus | microsoft.servicebus/namespaces/topics/rules | 2 | resource-group | item-write | armservicebus:Rules.ListBySubscriptions |
| microsoft.servicebus | microsoft.servicebus/namespaces/topics/subscriptions | 2 | resource-group | item-write | armservicebus:Subscriptions.ListByTopic |
| microsoft.servicefabric | microsoft.servicefabric/clusters/applications | 1 | resource-group | item-write | armservicefabric:Applications.List |
| microsoft.servicefabric | microsoft.servicefabric/clusters/applications/services | 2 | resource-group | item-write | armservicefabric:Services.List |
| microsoft.servicefabric | microsoft.servicefabric/clusters/applicationtypes | 1 | resource-group | item-write | armservicefabric:ApplicationTypes.List |
| microsoft.servicefabric | microsoft.servicefabric/clusters/applicationtypes/versions | 2 | resource-group | item-write | armservicefabric:ApplicationTypeVersions.List |
| microsoft.servicefabric | microsoft.servicefabric/managedclusters/applications | 1 | resource-group | item-write | armservicefabricmanagedclusters:Applications.List |
| microsoft.servicefabric | microsoft.servicefabric/managedclusters/applications/services | 2 | resource-group | item-write | armservicefabricmanagedclusters:Services.ListByApplications |
| microsoft.servicefabric | microsoft.servicefabric/managedclusters/applicationtypes | 1 | resource-group | item-write | armservicefabricmanagedclusters:ApplicationTypes.List |
| microsoft.servicefabric | microsoft.servicefabric/managedclusters/applicationtypes/versions | 2 | resource-group | item-write | armservicefabricmanagedclusters:ApplicationTypeVersions.ListByApplicationTypes |
| microsoft.servicefabric | microsoft.servicefabric/managedclusters/nodetypes | 1 | resource-group | item-write | armservicefabricmanagedclusters:NodeTypes.ListByManagedClusters |
| microsoft.servicefabricmesh | microsoft.servicefabricmesh/applications | 0 | subscription | item-write | armservicefabricmesh:Application.ListByResourceGroup, armservicefabricmesh:Application.ListBySubscription |
| microsoft.servicefabricmesh | microsoft.servicefabricmesh/applications/services | 1 | resource-group | arm-envelope | armservicefabricmesh:Service.List |
| microsoft.servicefabricmesh | microsoft.servicefabricmesh/gateways | 0 | subscription | item-write | armservicefabricmesh:Gateway.ListByResourceGroup, armservicefabricmesh:Gateway.ListBySubscription |
| microsoft.servicefabricmesh | microsoft.servicefabricmesh/networks | 0 | subscription | item-write | armservicefabricmesh:Network.ListByResourceGroup, armservicefabricmesh:Network.ListBySubscription |
| microsoft.servicefabricmesh | microsoft.servicefabricmesh/secrets | 0 | subscription | item-write | armservicefabricmesh:Secret.ListByResourceGroup, armservicefabricmesh:Secret.ListBySubscription |
| microsoft.servicefabricmesh | microsoft.servicefabricmesh/secrets/values | 1 | resource-group | item-write | armservicefabricmesh:SecretValue.List |
| microsoft.servicefabricmesh | microsoft.servicefabricmesh/volumes | 0 | subscription | item-write | armservicefabricmesh:Volume.ListByResourceGroup, armservicefabricmesh:Volume.ListBySubscription |
| microsoft.servicelinker | microsoft.servicelinker/connectors | 0 | resource-group | item-write | armservicelinker:Connector.List |
| microsoft.servicelinker | microsoft.servicelinker/dryruns | 0 | extension | item-write | armservicelinker:Connector.ListDryrun, armservicelinker:Linkers.ListDryrun |
| microsoft.servicelinker | microsoft.servicelinker/linkers | 0 | extension | item-write | armservicelinker:Linker.List |
| microsoft.servicenetworking | microsoft.servicenetworking/trafficcontrollers/associations | 1 | resource-group | item-write | armservicenetworking:AssociationsInterface.ListByTrafficController |
| microsoft.servicenetworking | microsoft.servicenetworking/trafficcontrollers/frontends | 1 | resource-group | item-write | armservicenetworking:FrontendsInterface.ListByTrafficController |
| microsoft.servicenetworking | microsoft.servicenetworking/trafficcontrollers/privateendpointconnections | 1 | resource-group | item-write | armservicenetworking:PrivateEndpointConnectionsInterface.ListByTrafficController |
| microsoft.servicenetworking | microsoft.servicenetworking/trafficcontrollers/privatelinkresources | 1 | resource-group | arm-envelope | armservicenetworking:PrivateLinkResourcesInterface.ListByTrafficController |
| microsoft.servicenetworking | microsoft.servicenetworking/trafficcontrollers/securitypolicies | 1 | resource-group | item-write | armservicenetworking:SecurityPoliciesInterface.ListByTrafficController |
| microsoft.signalrservice | microsoft.signalrservice/signalr/customcertificates | 1 | resource-group | item-write | armsignalr:CustomCertificates.List |
| microsoft.signalrservice | microsoft.signalrservice/signalr/customdomains | 1 | resource-group | item-write | armsignalr:CustomDomains.List |
| microsoft.signalrservice | microsoft.signalrservice/signalr/privateendpointconnections | 1 | resource-group | item-write | armsignalr:PrivateEndpointConnections.List |
| microsoft.signalrservice | microsoft.signalrservice/signalr/replicas | 1 | resource-group | item-write | armsignalr:Replicas.List |
| microsoft.signalrservice | microsoft.signalrservice/signalr/replicas/sharedprivatelinkresources | 2 | resource-group | item-write | armsignalr:ReplicaSharedPrivateLinkResources.List |
| microsoft.signalrservice | microsoft.signalrservice/signalr/sharedprivatelinkresources | 1 | resource-group | item-write | armsignalr:SharedPrivateLinkResources.List |
| microsoft.signalrservice | microsoft.signalrservice/webpubsub/customcertificates | 1 | resource-group | item-write | armwebpubsub:CustomCertificates.List |
| microsoft.signalrservice | microsoft.signalrservice/webpubsub/customdomains | 1 | resource-group | item-write | armwebpubsub:CustomDomains.List |
| microsoft.signalrservice | microsoft.signalrservice/webpubsub/hubs | 1 | resource-group | item-write | armwebpubsub:Hubs.List |
| microsoft.signalrservice | microsoft.signalrservice/webpubsub/privateendpointconnections | 1 | resource-group | item-write | armwebpubsub:PrivateEndpointConnections.List |
| microsoft.signalrservice | microsoft.signalrservice/webpubsub/replicas | 1 | resource-group | item-write | armwebpubsub:Replicas.List |
| microsoft.signalrservice | microsoft.signalrservice/webpubsub/replicas/sharedprivatelinkresources | 2 | resource-group | item-write | armwebpubsub:ReplicaSharedPrivateLinkResources.List |
| microsoft.signalrservice | microsoft.signalrservice/webpubsub/sharedprivatelinkresources | 1 | resource-group | item-write | armwebpubsub:SharedPrivateLinkResources.List |
| microsoft.sql | microsoft.sql/deletedservers | 0 | subscription | arm-envelope | armsql:DeletedServers.List, armsql:DeletedServers.ListByLocation |
| microsoft.sql | microsoft.sql/instancefailovergroups | 0 | resource-group | item-write | armsql:InstanceFailoverGroups.ListByLocation |
| microsoft.sql | microsoft.sql/instancepools/operations | 1 | resource-group | arm-envelope | armsql:InstancePoolOperations.ListByInstancePool |
| microsoft.sql | microsoft.sql/longtermretentionmanagedinstances/longtermretentiondatabases/longtermretentionmanagedinstancebackups | 2 | subscription | item-write | armsql:LongTermRetentionManagedInstanceBackups.ListByDatabase, armsql:LongTermRetentionManagedInstanceBackups.ListByInstance, armsql:LongTermRetentionManagedInstanceBackups.ListByLocation, armsql:LongTermRetentionManagedInstanceBackups.ListByResourceGroupDatabase, armsql:LongTermRetentionManagedInstanceBackups.ListByResourceGroupInstance, armsql:LongTermRetentionManagedInstanceBackups.ListByResourceGroupLocation |
| microsoft.sql | microsoft.sql/longtermretentionservers/longtermretentiondatabases/longtermretentionbackups | 2 | subscription | item-write | armsql:LongTermRetentionBackups.ListByDatabase, armsql:LongTermRetentionBackups.ListByLocation, armsql:LongTermRetentionBackups.ListByResourceGroupDatabase, armsql:LongTermRetentionBackups.ListByResourceGroupLocation, armsql:LongTermRetentionBackups.ListByResourceGroupServer, armsql:LongTermRetentionBackups.ListByServer |
| microsoft.sql | microsoft.sql/managedinstances/advancedthreatprotectionsettings | 1 | resource-group | item-write | armsql:ManagedInstanceAdvancedThreatProtectionSettings.ListByInstance |
| microsoft.sql | microsoft.sql/managedinstances/azureadonlyauthentications | 1 | resource-group | item-write | armsql:ManagedInstanceAzureADOnlyAuthentications.ListByInstance |
| microsoft.sql | microsoft.sql/managedinstances/databases/advancedthreatprotectionsettings | 2 | resource-group | item-write | armsql:ManagedDatabaseAdvancedThreatProtectionSettings.ListByDatabase |
| microsoft.sql | microsoft.sql/managedinstances/databases/backuplongtermretentionpolicies | 2 | resource-group | item-write | armsql:ManagedInstanceLongTermRetentionPolicies.ListByDatabase |
| microsoft.sql | microsoft.sql/managedinstances/databases/backupshorttermretentionpolicies | 2 | resource-group | item-write | armsql:ManagedBackupShortTermRetentionPolicies.ListByDatabase |
| microsoft.sql | microsoft.sql/managedinstances/databases/ledgerdigestuploads | 2 | resource-group | item-write | armsql:ManagedLedgerDigestUploads.ListByDatabase |
| microsoft.sql | microsoft.sql/managedinstances/databases/schemas | 2 | resource-group | arm-envelope | armsql:ManagedDatabaseSchemas.ListByDatabase |
| microsoft.sql | microsoft.sql/managedinstances/databases/schemas/tables | 3 | resource-group | arm-envelope | armsql:ManagedDatabaseTables.ListBySchema |
| microsoft.sql | microsoft.sql/managedinstances/databases/schemas/tables/columns | 4 | resource-group | arm-envelope | armsql:ManagedDatabaseColumns.ListByTable |
| microsoft.sql | microsoft.sql/managedinstances/databases/vulnerabilityassessments/scans | 3 | resource-group | arm-envelope | armsql:ManagedDatabaseVulnerabilityAssessmentScans.ListByDatabase |
| microsoft.sql | microsoft.sql/managedinstances/distributedavailabilitygroups | 1 | resource-group | item-write | armsql:DistributedAvailabilityGroups.ListByInstance |
| microsoft.sql | microsoft.sql/managedinstances/dnsaliases | 1 | resource-group | item-write | armsql:ManagedServerDNSAliases.ListByManagedInstance |
| microsoft.sql | microsoft.sql/managedinstances/dtc | 1 | resource-group | item-write | armsql:ManagedInstanceDtcs.ListByManagedInstance |
| microsoft.sql | microsoft.sql/managedinstances/endpointcertificates | 1 | resource-group | arm-envelope | armsql:EndpointCertificates.ListByInstance |
| microsoft.sql | microsoft.sql/managedinstances/operations | 1 | resource-group | arm-envelope | armsql:ManagedInstanceOperations.ListByManagedInstance |
| microsoft.sql | microsoft.sql/managedinstances/privatelinkresources | 1 | resource-group | arm-envelope | armsql:ManagedInstancePrivateLinkResources.ListByManagedInstance |
| microsoft.sql | microsoft.sql/managedinstances/recoverabledatabases | 1 | resource-group | arm-envelope | armsql:RecoverableManagedDatabases.ListByInstance |
| microsoft.sql | microsoft.sql/managedinstances/restorabledroppeddatabases | 1 | resource-group | arm-envelope | armsql:RestorableDroppedManagedDatabases.ListByInstance |
| microsoft.sql | microsoft.sql/managedinstances/restorabledroppeddatabases/backupshorttermretentionpolicies | 2 | resource-group | item-write | armsql:ManagedRestorableDroppedDatabaseBackupShortTermRetentionPolicies.ListByRestorableDroppedDatabase |
| microsoft.sql | microsoft.sql/managedinstances/serverconfigurationoptions | 1 | resource-group | item-write | armsql:ServerConfigurationOptions.ListByManagedInstance |
| microsoft.sql | microsoft.sql/managedinstances/servertrustcertificates | 1 | resource-group | item-write | armsql:ServerTrustCertificates.ListByInstance |
| microsoft.sql | microsoft.sql/managedinstances/startstopschedules | 1 | resource-group | item-write | armsql:StartStopManagedInstanceSchedules.ListByInstance |
| microsoft.sql | microsoft.sql/servers/advisors | 1 | resource-group | item-write | armsql:ServerAdvisors.ListByServer |
| microsoft.sql | microsoft.sql/servers/azureadonlyauthentications | 1 | resource-group | item-write | armsql:ServerAzureADOnlyAuthentications.ListByServer |
| microsoft.sql | microsoft.sql/servers/connectionpolicies | 1 | resource-group | item-write | armsql:ServerConnectionPolicies.ListByServer |
| microsoft.sql | microsoft.sql/servers/databases/advisors | 2 | resource-group | item-write | armsql:DatabaseAdvisors.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/advisors/recommendedactions | 3 | resource-group | item-write | armsql:DatabaseRecommendedActions.ListByDatabaseAdvisor |
| microsoft.sql | microsoft.sql/servers/databases/backuplongtermretentionpolicies | 2 | resource-group | item-write | armsql:LongTermRetentionPolicies.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/backupshorttermretentionpolicies | 2 | resource-group | item-write | armsql:BackupShortTermRetentionPolicies.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/datamaskingpolicies/rules | 3 | resource-group | item-write | armsql:DataMaskingRules.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/datawarehouseuseractivities | 2 | resource-group | arm-envelope | armsql:DataWarehouseUserActivities.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/extendedauditingsettings | 2 | resource-group | item-write | armsql:ExtendedDatabaseBlobAuditingPolicies.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/extensions | 2 | resource-group | item-write | armsql:DatabaseExtensions.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/restorepoints | 2 | resource-group | item-write | armsql:RestorePoints.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/schemas | 2 | resource-group | arm-envelope | armsql:DatabaseSchemas.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/schemas/tables | 3 | resource-group | arm-envelope | armsql:DatabaseTables.ListBySchema |
| microsoft.sql | microsoft.sql/servers/databases/schemas/tables/columns | 4 | resource-group | arm-envelope | armsql:DatabaseColumns.ListByTable |
| microsoft.sql | microsoft.sql/servers/databases/sqlvulnerabilityassessments | 2 | resource-group | arm-envelope | armsql:DatabaseSQLVulnerabilityAssessmentsSettings.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/sqlvulnerabilityassessments/baselines | 3 | resource-group | item-write | armsql:DatabaseSQLVulnerabilityAssessmentBaselines.ListBySQLVulnerabilityAssessment |
| microsoft.sql | microsoft.sql/servers/databases/sqlvulnerabilityassessments/baselines/rules | 4 | resource-group | item-write | armsql:DatabaseSQLVulnerabilityAssessmentRuleBaselines.ListByBaseline |
| microsoft.sql | microsoft.sql/servers/databases/sqlvulnerabilityassessments/scans | 3 | resource-group | arm-envelope | armsql:DatabaseSQLVulnerabilityAssessmentScans.ListBySQLVulnerabilityAssessments |
| microsoft.sql | microsoft.sql/servers/databases/sqlvulnerabilityassessments/scans/scanresults | 4 | resource-group | arm-envelope | armsql:DatabaseSQLVulnerabilityAssessmentScanResult.ListByScan |
| microsoft.sql | microsoft.sql/servers/databases/syncgroups/syncmembers | 3 | resource-group | item-write | armsql:SyncMembers.ListBySyncGroup |
| microsoft.sql | microsoft.sql/servers/databases/vulnerabilityassessments/scans | 3 | resource-group | arm-envelope | armsql:DatabaseVulnerabilityAssessmentScans.ListByDatabase |
| microsoft.sql | microsoft.sql/servers/databases/workloadgroups/workloadclassifiers | 3 | resource-group | item-write | armsql:WorkloadClassifiers.ListByWorkloadGroup |
| microsoft.sql | microsoft.sql/servers/firewallrules | 1 | resource-group | item-write | armsql:FirewallRules.ListByServer |
| microsoft.sql | microsoft.sql/servers/ipv6firewallrules | 1 | resource-group | item-write | armsql:IPv6FirewallRules.ListByServer |
| microsoft.sql | microsoft.sql/servers/jobagents/credentials | 2 | resource-group | item-write | armsql:JobCredentials.ListByAgent |
| microsoft.sql | microsoft.sql/servers/jobagents/jobs | 2 | resource-group | item-write | armsql:Jobs.ListByAgent |
| microsoft.sql | microsoft.sql/servers/jobagents/jobs/executions | 3 | resource-group | item-write | armsql:JobExecutions.ListByAgent, armsql:JobExecutions.ListByJob, armsql:JobTargetExecutions.ListByJobExecution |
| microsoft.sql | microsoft.sql/servers/jobagents/jobs/executions/steps | 4 | resource-group | arm-envelope | armsql:JobStepExecutions.ListByJobExecution |
| microsoft.sql | microsoft.sql/servers/jobagents/jobs/executions/steps/targets | 5 | resource-group | arm-envelope | armsql:JobTargetExecutions.ListByStep |
| microsoft.sql | microsoft.sql/servers/jobagents/jobs/steps | 3 | resource-group | item-write | armsql:JobSteps.ListByJob |
| microsoft.sql | microsoft.sql/servers/jobagents/jobs/versions | 3 | resource-group | arm-envelope | armsql:JobVersions.ListByJob |
| microsoft.sql | microsoft.sql/servers/jobagents/jobs/versions/steps | 4 | resource-group | arm-envelope | armsql:JobSteps.ListByVersion |
| microsoft.sql | microsoft.sql/servers/jobagents/privateendpoints | 2 | resource-group | item-write | armsql:JobPrivateEndpoints.ListByAgent |
| microsoft.sql | microsoft.sql/servers/jobagents/targetgroups | 2 | resource-group | item-write | armsql:JobTargetGroups.ListByAgent |
| microsoft.sql | microsoft.sql/servers/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armsql:NetworkSecurityPerimeterConfigurations.ListByServer |
| microsoft.sql | microsoft.sql/servers/outboundfirewallrules | 1 | resource-group | item-write | armsql:OutboundFirewallRules.ListByServer |
| microsoft.sql | microsoft.sql/servers/privateendpointconnections | 1 | resource-group | item-write | armsql:PrivateEndpointConnections.ListByServer |
| microsoft.sql | microsoft.sql/servers/privatelinkresources | 1 | resource-group | arm-envelope | armsql:PrivateLinkResources.ListByServer |
| microsoft.sql | microsoft.sql/servers/recoverabledatabases | 1 | resource-group | arm-envelope | armsql:RecoverableDatabases.ListByServer |
| microsoft.sql | microsoft.sql/servers/sqlvulnerabilityassessments | 1 | resource-group | item-write | armsql:VulnerabilityAssessmentsSettings.ListByServer |
| microsoft.sql | microsoft.sql/servers/sqlvulnerabilityassessments/baselines | 2 | resource-group | item-write | armsql:VulnerabilityAssessmentBaseline.ListBySQLVulnerabilityAssessment |
| microsoft.sql | microsoft.sql/servers/sqlvulnerabilityassessments/baselines/rules | 3 | resource-group | item-write | armsql:VulnerabilityAssessmentRuleBaseline.ListByBaseline |
| microsoft.sql | microsoft.sql/servers/sqlvulnerabilityassessments/scans | 2 | resource-group | arm-envelope | armsql:VulnerabilityAssessmentScans.ListBySQLVulnerabilityAssessments |
| microsoft.sql | microsoft.sql/servers/sqlvulnerabilityassessments/scans/scanresults | 3 | resource-group | arm-envelope | armsql:VulnerabilityAssessmentScanResult.ListByScan |
| microsoft.sql | microsoft.sql/servertrustgroups | 0 | resource-group | item-write | armsql:ServerTrustGroups.ListByInstance, armsql:ServerTrustGroups.ListByLocation |
| microsoft.sqlvirtualmachine | microsoft.sqlvirtualmachine/sqlvirtualmachinegroups/availabilitygrouplisteners | 1 | resource-group | item-write | armsqlvirtualmachine:AvailabilityGroupListeners.ListByGroup |
| microsoft.standbypool | microsoft.standbypool/standbycontainergrouppools/runtimeviews | 1 | resource-group | arm-envelope | armstandbypool:StandbyContainerGroupPoolRuntimeViews.ListByStandbyPool |
| microsoft.standbypool | microsoft.standbypool/standbyvirtualmachinepools/runtimeviews | 1 | resource-group | arm-envelope | armstandbypool:StandbyVirtualMachinePoolRuntimeViews.ListByStandbyPool |
| microsoft.standbypool | microsoft.standbypool/standbyvirtualmachinepools/standbyvirtualmachines | 1 | resource-group | arm-envelope | armstandbypool:StandbyVirtualMachines.ListByStandbyVirtualMachinePoolResource |
| microsoft.storage | microsoft.storage/deletedaccounts | 0 | subscription | arm-envelope | armstorage:DeletedAccounts.List |
| microsoft.storage | microsoft.storage/storageaccounts/advancedplatformmetrics | 1 | resource-group | item-write | armstorage:AdvancedPlatformMetrics.List |
| microsoft.storage | microsoft.storage/storageaccounts/blobservices | 1 | resource-group | item-write | armstorage:BlobServices.List |
| microsoft.storage | microsoft.storage/storageaccounts/blobservices/containers | 2 | resource-group | item-write | armstorage:BlobContainers.List |
| microsoft.storage | microsoft.storage/storageaccounts/connectors | 1 | resource-group | item-write | armstorage:Connectors.ListByStorageAccount |
| microsoft.storage | microsoft.storage/storageaccounts/datashares | 1 | resource-group | item-write | armstorage:DataShares.ListByStorageAccount |
| microsoft.storage | microsoft.storage/storageaccounts/encryptionscopes | 1 | resource-group | item-write | armstorage:EncryptionScopes.List |
| microsoft.storage | microsoft.storage/storageaccounts/fileservices | 1 | resource-group | item-write | armstorage:FileServices.List |
| microsoft.storage | microsoft.storage/storageaccounts/fileservices/shares | 2 | resource-group | item-write | armstorage:FileShares.List |
| microsoft.storage | microsoft.storage/storageaccounts/fileservices/usages | 2 | resource-group | arm-envelope | armstorage:FileServices.ListServiceUsages |
| microsoft.storage | microsoft.storage/storageaccounts/inventorypolicies | 1 | resource-group | item-write | armstorage:BlobInventoryPolicies.List |
| microsoft.storage | microsoft.storage/storageaccounts/localusers | 1 | resource-group | item-write | armstorage:LocalUsers.List |
| microsoft.storage | microsoft.storage/storageaccounts/networksecurityperimeterconfigurations | 1 | resource-group | arm-envelope | armstorage:NetworkSecurityPerimeterConfigurations.List |
| microsoft.storage | microsoft.storage/storageaccounts/objectreplicationpolicies | 1 | resource-group | item-write | armstorage:ObjectReplicationPolicies.List |
| microsoft.storage | microsoft.storage/storageaccounts/privateendpointconnections | 1 | resource-group | item-write | armstorage:PrivateEndpointConnections.List |
| microsoft.storage | microsoft.storage/storageaccounts/queueservices | 1 | resource-group | item-write | armstorage:QueueServices.List |
| microsoft.storage | microsoft.storage/storageaccounts/queueservices/queues | 2 | resource-group | item-write | armstorage:Queue.List |
| microsoft.storage | microsoft.storage/storageaccounts/storagetaskassignments | 1 | resource-group | item-write | armstorage:TaskAssignments.List |
| microsoft.storage | microsoft.storage/storageaccounts/tableservices | 1 | resource-group | item-write | armstorage:TableServices.List |
| microsoft.storage | microsoft.storage/storageaccounts/tableservices/tables | 2 | resource-group | item-write | armstorage:Table.List |
| microsoft.storagecache | microsoft.storagecache/amlfilesystems | 0 | subscription | item-write | armstoragecache:AmlFilesystems.List, armstoragecache:AmlFilesystems.ListByResourceGroup |
| microsoft.storagecache | microsoft.storagecache/amlfilesystems/autoexportjobs | 1 | resource-group | item-write | armstoragecache:AutoExportJobs.ListByAmlFilesystem |
| microsoft.storagecache | microsoft.storagecache/amlfilesystems/autoimportjobs | 1 | resource-group | item-write | armstoragecache:AutoImportJobs.ListByAmlFilesystem |
| microsoft.storagecache | microsoft.storagecache/amlfilesystems/expansionjobs | 1 | resource-group | item-write | armstoragecache:ExpansionJobs.ListByAmlFilesystem |
| microsoft.storagecache | microsoft.storagecache/amlfilesystems/importjobs | 1 | resource-group | item-write | armstoragecache:ImportJobs.ListByAmlFilesystem |
| microsoft.storagecache | microsoft.storagecache/caches/storagetargets | 1 | resource-group | item-write | armstoragecache:StorageTargets.ListByCache |
| microsoft.storagemover | microsoft.storagemover/storagemovers/agents | 1 | resource-group | item-write | armstoragemover:Agents.List |
| microsoft.storagemover | microsoft.storagemover/storagemovers/connections | 1 | resource-group | item-write | armstoragemover:Connections.List |
| microsoft.storagemover | microsoft.storagemover/storagemovers/endpoints | 1 | resource-group | item-write | armstoragemover:Endpoints.List |
| microsoft.storagemover | microsoft.storagemover/storagemovers/projects | 1 | resource-group | item-write | armstoragemover:Projects.List |
| microsoft.storagemover | microsoft.storagemover/storagemovers/projects/jobdefinitions | 2 | resource-group | item-write | armstoragemover:JobDefinitions.List |
| microsoft.storagemover | microsoft.storagemover/storagemovers/projects/jobdefinitions/jobruns | 3 | resource-group | arm-envelope | armstoragemover:JobRuns.List |
| microsoft.storagepool | microsoft.storagepool/diskpools | 0 | subscription | item-write | armstoragepool:DiskPools.ListByResourceGroup, armstoragepool:DiskPools.ListBySubscription |
| microsoft.storagepool | microsoft.storagepool/diskpools/iscsitargets | 1 | resource-group | item-write | armstoragepool:IscsiTargets.ListByDiskPool |
| microsoft.storagesync | microsoft.storagesync/storagesyncservices/privateendpointconnections | 1 | resource-group | item-write | armstoragesync:PrivateEndpointConnections.ListByStorageSyncService |
| microsoft.storagesync | microsoft.storagesync/storagesyncservices/registeredservers | 1 | resource-group | item-write | armstoragesync:RegisteredServers.ListByStorageSyncService |
| microsoft.storagesync | microsoft.storagesync/storagesyncservices/syncgroups | 1 | resource-group | item-write | armstoragesync:SyncGroups.ListByStorageSyncService |
| microsoft.storagesync | microsoft.storagesync/storagesyncservices/syncgroups/cloudendpoints | 2 | resource-group | item-write | armstoragesync:CloudEndpoints.ListBySyncGroup |
| microsoft.storagesync | microsoft.storagesync/storagesyncservices/syncgroups/serverendpoints | 2 | resource-group | item-write | armstoragesync:ServerEndpoints.ListBySyncGroup |
| microsoft.storagesync | microsoft.storagesync/storagesyncservices/workflows | 1 | resource-group | arm-envelope | armstoragesync:Workflows.ListByStorageSyncService |
| microsoft.storsimple | microsoft.storsimple/managers | 0 | subscription | item-write | armstorsimple1200series:Managers.List, armstorsimple1200series:Managers.ListByResourceGroup, armstorsimple8000series:Managers.List, armstorsimple8000series:Managers.ListByResourceGroup |
| microsoft.storsimple | microsoft.storsimple/managers/accesscontrolrecords | 1 | resource-group | item-write | armstorsimple1200series:AccessControlRecords.ListByManager, armstorsimple8000series:AccessControlRecords.ListByManager |
| microsoft.storsimple | microsoft.storsimple/managers/bandwidthsettings | 1 | resource-group | item-write | armstorsimple8000series:BandwidthSettings.ListByManager |
| microsoft.storsimple | microsoft.storsimple/managers/devices | 1 | resource-group | item-write | armstorsimple1200series:Devices.ListByManager, armstorsimple1200series:Devices.ListFailoverTarget, armstorsimple8000series:Devices.ListByManager |
| microsoft.storsimple | microsoft.storsimple/managers/devices/backuppolicies | 2 | resource-group | item-write | armstorsimple8000series:BackupPolicies.ListByDevice |
| microsoft.storsimple | microsoft.storsimple/managers/devices/backuppolicies/schedules | 3 | resource-group | item-write | armstorsimple8000series:BackupSchedules.ListByBackupPolicy |
| microsoft.storsimple | microsoft.storsimple/managers/devices/backups | 2 | resource-group | item-write | armstorsimple1200series:Backups.ListByDevice, armstorsimple1200series:Backups.ListByManager, armstorsimple8000series:Backups.ListByDevice |
| microsoft.storsimple | microsoft.storsimple/managers/devices/backupschedulegroups | 2 | resource-group | item-write | armstorsimple1200series:BackupScheduleGroups.ListByDevice |
| microsoft.storsimple | microsoft.storsimple/managers/devices/chapsettings | 2 | resource-group | item-write | armstorsimple1200series:ChapSettings.ListByDevice |
| microsoft.storsimple | microsoft.storsimple/managers/devices/fileservers | 2 | resource-group | item-write | armstorsimple1200series:FileServers.ListByDevice, armstorsimple1200series:FileServers.ListByManager |
| microsoft.storsimple | microsoft.storsimple/managers/devices/fileservers/shares | 3 | resource-group | item-write | armstorsimple1200series:FileShares.ListByDevice, armstorsimple1200series:FileShares.ListByFileServer |
| microsoft.storsimple | microsoft.storsimple/managers/devices/iscsiservers | 2 | resource-group | item-write | armstorsimple1200series:IscsiServers.ListByDevice, armstorsimple1200series:IscsiServers.ListByManager |
| microsoft.storsimple | microsoft.storsimple/managers/devices/iscsiservers/disks | 3 | resource-group | item-write | armstorsimple1200series:IscsiDisks.ListByDevice, armstorsimple1200series:IscsiDisks.ListByIscsiServer |
| microsoft.storsimple | microsoft.storsimple/managers/devices/jobs | 2 | resource-group | arm-envelope | armstorsimple1200series:Jobs.ListByDevice, armstorsimple8000series:Jobs.ListByDevice |
| microsoft.storsimple | microsoft.storsimple/managers/devices/volumecontainers | 2 | resource-group | item-write | armstorsimple8000series:VolumeContainers.ListByDevice |
| microsoft.storsimple | microsoft.storsimple/managers/devices/volumecontainers/volumes | 3 | resource-group | item-write | armstorsimple8000series:Volumes.ListByDevice, armstorsimple8000series:Volumes.ListByVolumeContainer |
| microsoft.storsimple | microsoft.storsimple/managers/storageaccountcredentials | 1 | resource-group | item-write | armstorsimple1200series:StorageAccountCredentials.ListByManager, armstorsimple8000series:StorageAccountCredentials.ListByManager |
| microsoft.storsimple | microsoft.storsimple/managers/storagedomains | 1 | resource-group | item-write | armstorsimple1200series:StorageDomains.ListByManager |
| microsoft.streamanalytics | microsoft.streamanalytics/clusters/privateendpoints | 1 | resource-group | item-write | armstreamanalytics:PrivateEndpoints.ListByCluster |
| microsoft.streamanalytics | microsoft.streamanalytics/streamingjobs/functions | 1 | resource-group | item-write | armstreamanalytics:Functions.ListByStreamingJob |
| microsoft.streamanalytics | microsoft.streamanalytics/streamingjobs/inputs | 1 | resource-group | item-write | armstreamanalytics:Inputs.ListByStreamingJob |
| microsoft.streamanalytics | microsoft.streamanalytics/streamingjobs/outputs | 1 | resource-group | item-write | armstreamanalytics:Outputs.ListByStreamingJob |
| microsoft.subscription | microsoft.subscription/aliases | 0 | tenant | item-write | armsubscription:Alias.List |
| microsoft.subscription | microsoft.subscription/changetenantrequest | 0 | subscription | item-write | armsubscription:Subscriptions.ListTargetDirectory |
| microsoft.subscription | microsoft.subscription/policies | 0 | tenant | item-write | armsubscription:Policy.ListPolicyForTenant |
| microsoft.support | microsoft.support/fileworkspaces/files | 1 | tenant | item-write | armsupport:Files.List, armsupport:FilesNoSubscription.List |
| microsoft.support | microsoft.support/services/problemclassifications | 1 | tenant | arm-envelope | armsupport:ProblemClassifications.List |
| microsoft.support | microsoft.support/supporttickets | 0 | tenant | item-write | armsupport:Tickets.List, armsupport:TicketsNoSubscription.List |
| microsoft.support | microsoft.support/supporttickets/chattranscripts | 1 | tenant | arm-envelope | armsupport:ChatTranscripts.List, armsupport:ChatTranscriptsNoSubscription.List |
| microsoft.support | microsoft.support/supporttickets/communications | 1 | tenant | item-write | armsupport:Communications.List, armsupport:CommunicationsNoSubscription.List |
| microsoft.synapse | microsoft.synapse/privatelinkhubs/privateendpointconnections | 1 | resource-group | arm-envelope | armsynapse:PrivateEndpointConnectionsPrivateLinkHub.List |
| microsoft.synapse | microsoft.synapse/privatelinkhubs/privatelinkresources | 1 | resource-group | arm-envelope | armsynapse:PrivateLinkHubPrivateLinkResources.List |
| microsoft.synapse | microsoft.synapse/workspaces/auditingsettings | 1 | resource-group | item-write | armsynapse:WorkspaceManagedSQLServerBlobAuditingPolicies.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/azureadonlyauthentications | 1 | resource-group | item-write | armsynapse:AzureADOnlyAuthentications.List |
| microsoft.synapse | microsoft.synapse/workspaces/bigdatapools | 1 | resource-group | item-write | armsynapse:BigDataPools.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/dedicatedsqlminimaltlssettings | 1 | resource-group | item-write | armsynapse:WorkspaceManagedSQLServerDedicatedSQLMinimalTLSSettings.List |
| microsoft.synapse | microsoft.synapse/workspaces/encryptionprotector | 1 | resource-group | item-write | armsynapse:WorkspaceManagedSQLServerEncryptionProtector.List |
| microsoft.synapse | microsoft.synapse/workspaces/extendedauditingsettings | 1 | resource-group | item-write | armsynapse:WorkspaceManagedSQLServerExtendedBlobAuditingPolicies.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/firewallrules | 1 | resource-group | item-write | armsynapse:IPFirewallRules.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/integrationruntimes | 1 | resource-group | item-write | armsynapse:IntegrationRuntimes.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/keys | 1 | resource-group | item-write | armsynapse:Keys.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/kustopools | 1 | resource-group | item-write | armsynapse:KustoPools.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/kustopools/attacheddatabaseconfigurations | 2 | resource-group | item-write | armsynapse:KustoPoolAttachedDatabaseConfigurations.ListByKustoPool |
| microsoft.synapse | microsoft.synapse/workspaces/kustopools/databases | 2 | resource-group | item-write | armsynapse:KustoPoolDatabases.ListByKustoPool |
| microsoft.synapse | microsoft.synapse/workspaces/kustopools/databases/dataconnections | 3 | resource-group | item-write | armsynapse:KustoPoolDataConnections.ListByDatabase |
| microsoft.synapse | microsoft.synapse/workspaces/kustopools/databases/principalassignments | 3 | resource-group | item-write | armsynapse:KustoPoolDatabasePrincipalAssignments.List |
| microsoft.synapse | microsoft.synapse/workspaces/kustopools/principalassignments | 2 | resource-group | item-write | armsynapse:KustoPoolPrincipalAssignments.List |
| microsoft.synapse | microsoft.synapse/workspaces/libraries | 1 | resource-group | arm-envelope | armsynapse:Libraries.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/privateendpointconnections | 1 | resource-group | item-write | armsynapse:PrivateEndpointConnections.List |
| microsoft.synapse | microsoft.synapse/workspaces/privatelinkresources | 1 | resource-group | arm-envelope | armsynapse:PrivateLinkResources.List |
| microsoft.synapse | microsoft.synapse/workspaces/recoverablesqlpools | 1 | resource-group | arm-envelope | armsynapse:WorkspaceManagedSQLServerRecoverableSQLPools.List |
| microsoft.synapse | microsoft.synapse/workspaces/restorabledroppedsqlpools | 1 | resource-group | arm-envelope | armsynapse:RestorableDroppedSQLPools.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/securityalertpolicies | 1 | resource-group | item-write | armsynapse:WorkspaceManagedSQLServerSecurityAlertPolicy.List |
| microsoft.synapse | microsoft.synapse/workspaces/sparkconfigurations | 1 | resource-group | arm-envelope | armsynapse:SparkConfigurations.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools | 1 | resource-group | item-write | armsynapse:SQLPools.ListByWorkspace |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/auditingsettings | 2 | resource-group | item-write | armsynapse:SQLPoolBlobAuditingPolicies.ListBySQLPool |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/datamaskingpolicies/rules | 3 | resource-group | item-write | armsynapse:DataMaskingRules.ListBySQLPool |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/extendedauditingsettings | 2 | resource-group | item-write | armsynapse:ExtendedSQLPoolBlobAuditingPolicies.ListBySQLPool |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/geobackuppolicies | 2 | resource-group | item-write | armsynapse:SQLPoolGeoBackupPolicies.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/replicationlinks | 2 | resource-group | arm-envelope | armsynapse:SQLPoolReplicationLinks.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/restorepoints | 2 | resource-group | item-write | armsynapse:SQLPoolRestorePoints.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/schemas | 2 | resource-group | arm-envelope | armsynapse:SQLPoolSchemas.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/schemas/tables | 3 | resource-group | arm-envelope | armsynapse:SQLPoolTables.ListBySchema |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/schemas/tables/columns | 4 | resource-group | arm-envelope | armsynapse:SQLPoolTableColumns.ListByTableName |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/securityalertpolicies | 2 | resource-group | item-write | armsynapse:SQLPoolSecurityAlertPolicies.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/transparentdataencryption | 2 | resource-group | item-write | armsynapse:SQLPoolTransparentDataEncryptions.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/vulnerabilityassessments | 2 | resource-group | item-write | armsynapse:SQLPoolVulnerabilityAssessments.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/vulnerabilityassessments/scans | 3 | resource-group | arm-envelope | armsynapse:SQLPoolVulnerabilityAssessmentScans.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/workloadgroups | 2 | resource-group | item-write | armsynapse:SQLPoolWorkloadGroup.List |
| microsoft.synapse | microsoft.synapse/workspaces/sqlpools/workloadgroups/workloadclassifiers | 3 | resource-group | item-write | armsynapse:SQLPoolWorkloadClassifier.List |
| microsoft.synapse | microsoft.synapse/workspaces/vulnerabilityassessments | 1 | resource-group | item-write | armsynapse:WorkspaceManagedSQLServerVulnerabilityAssessments.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts | 0 | subscription | item-write | armtestbase:Accounts.ListByResourceGroup, armtestbase:Accounts.ListBySubscription |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/availableoss | 1 | resource-group | arm-envelope | armtestbase:AvailableOS.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/customerevents | 1 | resource-group | item-write | armtestbase:CustomerEvents.ListByTestBaseAccount |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/emailevents | 1 | resource-group | arm-envelope | armtestbase:EmailEvents.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/flightingrings | 1 | resource-group | arm-envelope | armtestbase:FlightingRings.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/packages | 1 | resource-group | item-write | armtestbase:Packages.ListByTestBaseAccount |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/packages/favoriteprocesses | 2 | resource-group | item-write | armtestbase:FavoriteProcesses.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/packages/osupdates | 2 | resource-group | arm-envelope | armtestbase:OSUpdates.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/packages/testresults | 2 | resource-group | arm-envelope | armtestbase:TestResults.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/packages/testresults/analysisresults | 3 | resource-group | arm-envelope | armtestbase:AnalysisResults.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/testsummaries | 1 | resource-group | arm-envelope | armtestbase:TestSummaries.List |
| microsoft.testbase | microsoft.testbase/testbaseaccounts/testtypes | 1 | resource-group | arm-envelope | armtestbase:TestTypes.List |
| microsoft.timeseriesinsights | microsoft.timeseriesinsights/environments | 0 | subscription | item-write | armtimeseriesinsights:Environments.ListByResourceGroup, armtimeseriesinsights:Environments.ListBySubscription |
| microsoft.timeseriesinsights | microsoft.timeseriesinsights/environments/accesspolicies | 1 | resource-group | item-write | armtimeseriesinsights:AccessPolicies.ListByEnvironment |
| microsoft.timeseriesinsights | microsoft.timeseriesinsights/environments/eventsources | 1 | resource-group | item-write | armtimeseriesinsights:EventSources.ListByEnvironment |
| microsoft.timeseriesinsights | microsoft.timeseriesinsights/environments/referencedatasets | 1 | resource-group | item-write | armtimeseriesinsights:ReferenceDataSets.ListByEnvironment |
| microsoft.virtualmachineimages | microsoft.virtualmachineimages/imagetemplates/runoutputs | 1 | resource-group | arm-envelope | armvirtualmachineimagebuilder:VirtualMachineImageTemplates.ListRunOutputs |
| microsoft.virtualmachineimages | microsoft.virtualmachineimages/imagetemplates/triggers | 1 | resource-group | item-write | armvirtualmachineimagebuilder:Triggers.ListByImageTemplate |
| microsoft.visualstudio | microsoft.visualstudio/account | 0 | resource-group | item-write | armvisualstudio:Accounts.ListByResourceGroup |
| microsoft.visualstudio | microsoft.visualstudio/account/extension | 1 | resource-group | item-write | armvisualstudio:Extensions.ListByAccount |
| microsoft.visualstudio | microsoft.visualstudio/account/project | 1 | resource-group | item-write | armvisualstudio:Projects.ListByResourceGroup |
| microsoft.vmwarecloudsimple | microsoft.vmwarecloudsimple/dedicatedcloudnodes | 0 | subscription | item-write | armvmwarecloudsimple:DedicatedCloudNodes.ListByResourceGroup, armvmwarecloudsimple:DedicatedCloudNodes.ListBySubscription |
| microsoft.vmwarecloudsimple | microsoft.vmwarecloudsimple/dedicatedcloudservices | 0 | subscription | item-write | armvmwarecloudsimple:DedicatedCloudServices.ListByResourceGroup, armvmwarecloudsimple:DedicatedCloudServices.ListBySubscription |
| microsoft.vmwarecloudsimple | microsoft.vmwarecloudsimple/virtualmachines | 0 | subscription | item-write | armvmwarecloudsimple:VirtualMachines.ListByResourceGroup, armvmwarecloudsimple:VirtualMachines.ListBySubscription |
| microsoft.voiceservices | microsoft.voiceservices/communicationsgateways | 0 | subscription | item-write | armvoiceservices:CommunicationsGateways.ListByResourceGroup, armvoiceservices:CommunicationsGateways.ListBySubscription |
| microsoft.voiceservices | microsoft.voiceservices/communicationsgateways/testlines | 1 | resource-group | item-write | armvoiceservices:TestLines.ListByCommunicationsGateway |
| microsoft.web | microsoft.web/deletedsites | 0 | subscription | arm-envelope | armappservice:DeletedWebApps.List, armappservice:DeletedWebApps.ListByLocation |
| microsoft.web | microsoft.web/hostingenvironments/detectors | 1 | resource-group | arm-envelope | armappservice:Diagnostics.ListHostingEnvironmentDetectorResponses |
| microsoft.web | microsoft.web/hostingenvironments/privateendpointconnections | 1 | resource-group | item-write | armappservice:Environments.GetPrivateEndpointConnectionList |
| microsoft.web | microsoft.web/hostingenvironments/recommendations | 1 | resource-group | arm-envelope | armappservice:Recommendations.ListRecommendedRulesForHostingEnvironment |
| microsoft.web | microsoft.web/serverfarms/virtualnetworkconnections | 1 | resource-group | arm-envelope | armappservice:Plans.ListVnets |
| microsoft.web | microsoft.web/serverfarms/virtualnetworkconnections/routes | 2 | resource-group | item-write | armappservice:Plans.ListRoutesForVnet |
| microsoft.web | microsoft.web/sites/backups | 1 | resource-group | item-write | armappservice:WebApps.ListBackups |
| microsoft.web | microsoft.web/sites/basicpublishingcredentialspolicies | 1 | resource-group | item-write | armappservice:WebApps.ListBasicPublishingCredentialsPolicies |
| microsoft.web | microsoft.web/sites/certificates | 1 | resource-group | item-write | armappservice:SiteCertificates.List |
| microsoft.web | microsoft.web/sites/config | 1 | resource-group | item-write | armappservice:WebApps.ListConfigurations |
| microsoft.web | microsoft.web/sites/config/configreferences/appsettings | 1 | resource-group | arm-envelope | armappservice:WebApps.GetAppSettingsKeyVaultReferences |
| microsoft.web | microsoft.web/sites/config/configreferences/connectionstrings | 1 | resource-group | arm-envelope | armappservice:WebApps.GetSiteConnectionStringKeyVaultReferences |
| microsoft.web | microsoft.web/sites/config/snapshots | 2 | resource-group | arm-envelope | armappservice:WebApps.ListConfigurationSnapshotInfo |
| microsoft.web | microsoft.web/sites/continuouswebjobs | 1 | resource-group | item-write | armappservice:WebApps.ListContinuousWebJobs |
| microsoft.web | microsoft.web/sites/deployments | 1 | resource-group | item-write | armappservice:WebApps.ListDeployments |
| microsoft.web | microsoft.web/sites/deploymentstatus | 1 | resource-group | arm-envelope | armappservice:WebApps.ListProductionSiteDeploymentStatuses |
| microsoft.web | microsoft.web/sites/detectors | 1 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteDetectorResponses |
| microsoft.web | microsoft.web/sites/diagnostics | 1 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteDiagnosticCategories |
| microsoft.web | microsoft.web/sites/diagnostics/analyses | 2 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteAnalyses |
| microsoft.web | microsoft.web/sites/diagnostics/detectors | 2 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteDetectors |
| microsoft.web | microsoft.web/sites/domainownershipidentifiers | 1 | resource-group | item-write | armappservice:WebApps.ListDomainOwnershipIdentifiers |
| microsoft.web | microsoft.web/sites/functions | 1 | resource-group | item-write | armappservice:WebApps.ListFunctions |
| microsoft.web | microsoft.web/sites/hostnamebindings | 1 | resource-group | item-write | armappservice:WebApps.ListHostNameBindings |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/runs | 2 | resource-group | arm-envelope | armappservice:WorkflowRuns.List |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/runs/actions | 3 | resource-group | arm-envelope | armappservice:WorkflowRunActions.List |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/runs/actions/repetitions | 4 | resource-group | arm-envelope | armappservice:WorkflowRunActionRepetitions.List |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/runs/actions/repetitions/requesthistories | 5 | resource-group | arm-envelope | armappservice:WorkflowRunActionRepetitionsRequestHistories.List |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/runs/actions/scoperepetitions | 4 | resource-group | arm-envelope | armappservice:WorkflowRunActionScopeRepetitions.List |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/triggers | 2 | resource-group | arm-envelope | armappservice:WorkflowTriggers.List |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/triggers/histories | 3 | resource-group | arm-envelope | armappservice:WorkflowTriggerHistories.List |
| microsoft.web | microsoft.web/sites/hostruntime/runtime/webhooks/workflow/api/management/workflows/versions | 2 | resource-group | arm-envelope | armappservice:WorkflowVersions.List |
| microsoft.web | microsoft.web/sites/hybridconnection | 1 | resource-group | item-write | armappservice:WebApps.ListRelayServiceConnections |
| microsoft.web | microsoft.web/sites/instances | 1 | resource-group | arm-envelope | armappservice:WebApps.ListInstanceIdentifiers |
| microsoft.web | microsoft.web/sites/instances/processes | 2 | resource-group | item-write | armappservice:WebApps.ListInstanceProcesses |
| microsoft.web | microsoft.web/sites/instances/processes/modules | 3 | resource-group | arm-envelope | armappservice:WebApps.ListInstanceProcessModules |
| microsoft.web | microsoft.web/sites/premieraddons | 1 | resource-group | item-write | armappservice:WebApps.ListPremierAddOns |
| microsoft.web | microsoft.web/sites/privateendpointconnections | 1 | resource-group | item-write | armappservice:WebApps.GetPrivateEndpointConnectionList |
| microsoft.web | microsoft.web/sites/processes | 1 | resource-group | item-write | armappservice:WebApps.ListProcesses |
| microsoft.web | microsoft.web/sites/processes/modules | 2 | resource-group | arm-envelope | armappservice:WebApps.ListProcessModules |
| microsoft.web | microsoft.web/sites/publiccertificates | 1 | resource-group | item-write | armappservice:WebApps.ListPublicCertificates |
| microsoft.web | microsoft.web/sites/recommendations | 1 | resource-group | arm-envelope | armappservice:Recommendations.ListRecommendedRulesForWebApp |
| microsoft.web | microsoft.web/sites/resourcehealthmetadata | 1 | resource-group | arm-envelope | armappservice:ResourceHealthMetadata.ListBySite |
| microsoft.web | microsoft.web/sites/sitecontainers | 1 | resource-group | item-write | armappservice:WebApps.ListSiteContainers |
| microsoft.web | microsoft.web/sites/siteextensions | 1 | resource-group | item-write | armappservice:WebApps.ListSiteExtensions |
| microsoft.web | microsoft.web/sites/slots/backups | 2 | resource-group | item-write | armappservice:WebApps.ListBackupsSlot |
| microsoft.web | microsoft.web/sites/slots/basicpublishingcredentialspolicies | 2 | resource-group | item-write | armappservice:WebApps.ListBasicPublishingCredentialsPoliciesSlot |
| microsoft.web | microsoft.web/sites/slots/certificates | 2 | resource-group | item-write | armappservice:SiteCertificates.ListSlot |
| microsoft.web | microsoft.web/sites/slots/config | 2 | resource-group | item-write | armappservice:WebApps.ListConfigurationsSlot |
| microsoft.web | microsoft.web/sites/slots/config/configreferences/appsettings | 2 | resource-group | arm-envelope | armappservice:WebApps.GetAppSettingsKeyVaultReferencesSlot |
| microsoft.web | microsoft.web/sites/slots/config/configreferences/connectionstrings | 2 | resource-group | arm-envelope | armappservice:WebApps.GetSiteConnectionStringKeyVaultReferencesSlot |
| microsoft.web | microsoft.web/sites/slots/config/snapshots | 3 | resource-group | arm-envelope | armappservice:WebApps.ListConfigurationSnapshotInfoSlot |
| microsoft.web | microsoft.web/sites/slots/continuouswebjobs | 2 | resource-group | item-write | armappservice:WebApps.ListContinuousWebJobsSlot |
| microsoft.web | microsoft.web/sites/slots/deployments | 2 | resource-group | item-write | armappservice:WebApps.ListDeploymentsSlot |
| microsoft.web | microsoft.web/sites/slots/deploymentstatus | 2 | resource-group | arm-envelope | armappservice:WebApps.ListSlotSiteDeploymentStatusesSlot |
| microsoft.web | microsoft.web/sites/slots/detectors | 2 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteDetectorResponsesSlot |
| microsoft.web | microsoft.web/sites/slots/diagnostics | 2 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteDiagnosticCategoriesSlot |
| microsoft.web | microsoft.web/sites/slots/diagnostics/analyses | 3 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteAnalysesSlot |
| microsoft.web | microsoft.web/sites/slots/diagnostics/detectors | 3 | resource-group | arm-envelope | armappservice:Diagnostics.ListSiteDetectorsSlot |
| microsoft.web | microsoft.web/sites/slots/domainownershipidentifiers | 2 | resource-group | item-write | armappservice:WebApps.ListDomainOwnershipIdentifiersSlot |
| microsoft.web | microsoft.web/sites/slots/functions | 2 | resource-group | item-write | armappservice:WebApps.ListInstanceFunctionsSlot |
| microsoft.web | microsoft.web/sites/slots/hostnamebindings | 2 | resource-group | item-write | armappservice:WebApps.ListHostNameBindingsSlot |
| microsoft.web | microsoft.web/sites/slots/hybridconnection | 2 | resource-group | item-write | armappservice:WebApps.ListRelayServiceConnectionsSlot |
| microsoft.web | microsoft.web/sites/slots/instances | 2 | resource-group | arm-envelope | armappservice:WebApps.ListInstanceIdentifiersSlot |
| microsoft.web | microsoft.web/sites/slots/instances/processes | 3 | resource-group | item-write | armappservice:WebApps.ListInstanceProcessesSlot |
| microsoft.web | microsoft.web/sites/slots/instances/processes/modules | 4 | resource-group | arm-envelope | armappservice:WebApps.ListInstanceProcessModulesSlot |
| microsoft.web | microsoft.web/sites/slots/premieraddons | 2 | resource-group | item-write | armappservice:WebApps.ListPremierAddOnsSlot |
| microsoft.web | microsoft.web/sites/slots/privateendpointconnections | 2 | resource-group | item-write | armappservice:WebApps.GetPrivateEndpointConnectionListSlot |
| microsoft.web | microsoft.web/sites/slots/processes | 2 | resource-group | item-write | armappservice:WebApps.ListProcessesSlot |
| microsoft.web | microsoft.web/sites/slots/processes/modules | 3 | resource-group | arm-envelope | armappservice:WebApps.ListProcessModulesSlot |
| microsoft.web | microsoft.web/sites/slots/publiccertificates | 2 | resource-group | item-write | armappservice:WebApps.ListPublicCertificatesSlot |
| microsoft.web | microsoft.web/sites/slots/resourcehealthmetadata | 2 | resource-group | arm-envelope | armappservice:ResourceHealthMetadata.ListBySiteSlot |
| microsoft.web | microsoft.web/sites/slots/sitecontainers | 2 | resource-group | item-write | armappservice:WebApps.ListSiteContainersSlot |
| microsoft.web | microsoft.web/sites/slots/siteextensions | 2 | resource-group | item-write | armappservice:WebApps.ListSiteExtensionsSlot |
| microsoft.web | microsoft.web/sites/slots/triggeredwebjobs | 2 | resource-group | item-write | armappservice:WebApps.ListTriggeredWebJobsSlot |
| microsoft.web | microsoft.web/sites/slots/triggeredwebjobs/history | 3 | resource-group | arm-envelope | armappservice:WebApps.ListTriggeredWebJobHistorySlot |
| microsoft.web | microsoft.web/sites/slots/virtualnetworkconnections | 2 | resource-group | item-write | armappservice:WebApps.ListVnetConnectionsSlot |
| microsoft.web | microsoft.web/sites/slots/webjobs | 2 | resource-group | arm-envelope | armappservice:WebApps.ListWebJobsSlot |
| microsoft.web | microsoft.web/sites/slots/workflows | 2 | resource-group | arm-envelope | armappservice:WebApps.ListInstanceWorkflowsSlot |
| microsoft.web | microsoft.web/sites/triggeredwebjobs | 1 | resource-group | item-write | armappservice:WebApps.ListTriggeredWebJobs |
| microsoft.web | microsoft.web/sites/triggeredwebjobs/history | 2 | resource-group | arm-envelope | armappservice:WebApps.ListTriggeredWebJobHistory |
| microsoft.web | microsoft.web/sites/virtualnetworkconnections | 1 | resource-group | item-write | armappservice:WebApps.ListVnetConnections |
| microsoft.web | microsoft.web/sites/webjobs | 1 | resource-group | arm-envelope | armappservice:WebApps.ListWebJobs |
| microsoft.web | microsoft.web/sites/workflows | 1 | resource-group | arm-envelope | armappservice:WebApps.ListWorkflows |
| microsoft.web | microsoft.web/sourcecontrols | 0 | tenant | item-write | armappservice:WebSiteManagement.ListSourceControls |
| microsoft.web | microsoft.web/staticsites/basicauth | 1 | resource-group | item-write | armappservice:StaticSites.ListBasicAuth |
| microsoft.web | microsoft.web/staticsites/builds/databaseconnections | 2 | resource-group | item-write | armappservice:StaticSites.GetBuildDatabaseConnections |
| microsoft.web | microsoft.web/staticsites/builds/linkedbackends | 2 | resource-group | item-write | armappservice:StaticSites.GetLinkedBackendsForBuild |
| microsoft.web | microsoft.web/staticsites/builds/userprovidedfunctionapps | 2 | resource-group | item-write | armappservice:StaticSites.GetUserProvidedFunctionAppsForStaticSiteBuild |
| microsoft.web | microsoft.web/staticsites/customdomains | 1 | resource-group | item-write | armappservice:StaticSites.ListStaticSiteCustomDomains |
| microsoft.web | microsoft.web/staticsites/databaseconnections | 1 | resource-group | item-write | armappservice:StaticSites.GetDatabaseConnections |
| microsoft.web | microsoft.web/staticsites/linkedbackends | 1 | resource-group | item-write | armappservice:StaticSites.GetLinkedBackends |
| microsoft.web | microsoft.web/staticsites/privateendpointconnections | 1 | resource-group | item-write | armappservice:StaticSites.GetPrivateEndpointConnectionList |
| microsoft.web | microsoft.web/staticsites/userprovidedfunctionapps | 1 | resource-group | item-write | armappservice:StaticSites.GetUserProvidedFunctionAppsForStaticSite |
| microsoft.weightsandbiases | microsoft.weightsandbiases/instances | 0 | subscription | item-write | armweightsandbiases:Instances.ListByResourceGroup, armweightsandbiases:Instances.ListBySubscription |
| microsoft.windowsesu | microsoft.windowsesu/multipleactivationkeys | 0 | subscription | item-write | armwindowsesu:MultipleActivationKeys.List, armwindowsesu:MultipleActivationKeys.ListByResourceGroup |
| microsoft.windowsiot | microsoft.windowsiot/deviceservices | 0 | subscription | item-write | armwindowsiot:Services.List, armwindowsiot:Services.ListByResourceGroup |
| microsoft.workloadmonitor | microsoft.workloadmonitor/monitors/history | 1 | extension | arm-envelope | armworkloadmonitor:HealthMonitors.ListStateChanges |
| microsoft.workloads | microsoft.workloads/monitors/providerinstances | 1 | resource-group | item-write | armworkloads:ProviderInstances.List |
| microsoft.workloads | microsoft.workloads/monitors/saplandscapemonitor | 1 | resource-group | item-write | armworkloads:SapLandscapeMonitor.List |
| microsoft.workloads | microsoft.workloads/sapdiscoverysites | 0 | subscription | item-write | armmigrationdiscoverysap:SapDiscoverySites.ListByResourceGroup, armmigrationdiscoverysap:SapDiscoverySites.ListBySubscription |
| microsoft.workloads | microsoft.workloads/sapdiscoverysites/sapinstances | 1 | resource-group | item-write | armmigrationdiscoverysap:SapInstances.ListBySapDiscoverySite |
| microsoft.workloads | microsoft.workloads/sapdiscoverysites/sapinstances/serverinstances | 2 | resource-group | item-write | armmigrationdiscoverysap:ServerInstances.ListBySapInstance |
| microsoft.workloads | microsoft.workloads/sapvirtualinstances/applicationinstances | 1 | resource-group | item-write | armworkloads:SAPApplicationServerInstances.List, armworkloadssapvirtualinstance:SAPApplicationServerInstances.List |
| microsoft.workloads | microsoft.workloads/sapvirtualinstances/centralinstances | 1 | resource-group | item-write | armworkloads:SAPCentralInstances.List, armworkloadssapvirtualinstance:SAPCentralServerInstances.List |
| microsoft.workloads | microsoft.workloads/sapvirtualinstances/databaseinstances | 1 | resource-group | item-write | armworkloads:SAPDatabaseInstances.List, armworkloadssapvirtualinstance:SAPDatabaseInstances.List |
| mongodb.atlas | mongodb.atlas/organizations | 0 | subscription | item-write | armmongodbatlas:Organizations.ListByResourceGroup, armmongodbatlas:Organizations.ListBySubscription |
| mongodb.atlas | mongodb.atlas/organizations/projects | 1 | resource-group | item-write | armmongodbatlas:Projects.List |
| mongodb.atlas | mongodb.atlas/organizations/projects/clusters | 2 | resource-group | item-write | armmongodbatlas:Clusters.List |
| napster.companionapi | napster.companionapi/organizations | 0 | subscription | item-write | armnapsteromniagentapi:Organizations.ListByResourceGroup, armnapsteromniagentapi:Organizations.ListBySubscription |
| newrelic.observability | newrelic.observability/monitors | 0 | subscription | item-write | armnewrelicobservability:Monitors.ListByResourceGroup, armnewrelicobservability:Monitors.ListBySubscription |
| newrelic.observability | newrelic.observability/monitors/monitoredsubscriptions | 1 | resource-group | item-write | armnewrelicobservability:MonitoredSubscriptions.List |
| newrelic.observability | newrelic.observability/monitors/tagrules | 1 | resource-group | item-write | armnewrelicobservability:TagRules.ListByNewRelicMonitorResource |
| nginx.nginxplus | nginx.nginxplus/nginxdeployments | 0 | subscription | item-write | armnginx:Deployments.List, armnginx:Deployments.ListByResourceGroup |
| nginx.nginxplus | nginx.nginxplus/nginxdeployments/apikeys | 1 | resource-group | item-write | armnginx:APIKeys.List |
| nginx.nginxplus | nginx.nginxplus/nginxdeployments/certificates | 1 | resource-group | item-write | armnginx:Certificates.List |
| nginx.nginxplus | nginx.nginxplus/nginxdeployments/configurations | 1 | resource-group | item-write | armnginx:Configurations.List |
| nginx.nginxplus | nginx.nginxplus/nginxdeployments/wafpolicies | 1 | resource-group | item-write | armnginx:WafPolicy.List |
| oracle.database | oracle.database/autonomousdatabases | 0 | subscription | item-write | armoracledatabase:AutonomousDatabases.ListByResourceGroup, armoracledatabase:AutonomousDatabases.ListBySubscription |
| oracle.database | oracle.database/autonomousdatabases/autonomousdatabasebackups | 1 | resource-group | item-write | armoracledatabase:AutonomousDatabaseBackups.ListByAutonomousDatabase |
| oracle.database | oracle.database/cloudexadatainfrastructures | 0 | subscription | item-write | armoracledatabase:CloudExadataInfrastructures.ListByResourceGroup, armoracledatabase:CloudExadataInfrastructures.ListBySubscription |
| oracle.database | oracle.database/cloudexadatainfrastructures/dbservers | 1 | resource-group | arm-envelope | armoracledatabase:DbServers.ListByCloudExadataInfrastructure |
| oracle.database | oracle.database/cloudvmclusters | 0 | subscription | item-write | armoracledatabase:CloudVMClusters.ListByResourceGroup, armoracledatabase:CloudVMClusters.ListBySubscription |
| oracle.database | oracle.database/cloudvmclusters/dbnodes | 1 | resource-group | arm-envelope | armoracledatabase:DbNodes.ListByCloudVMCluster |
| oracle.database | oracle.database/cloudvmclusters/virtualnetworkaddresses | 1 | resource-group | item-write | armoracledatabase:VirtualNetworkAddresses.ListByCloudVMCluster |
| oracle.database | oracle.database/dbsystems | 0 | subscription | item-write | armoracledatabase:DbSystems.ListByResourceGroup, armoracledatabase:DbSystems.ListBySubscription |
| oracle.database | oracle.database/exadbvmclusters | 0 | subscription | item-write | armoracledatabase:ExadbVMClusters.ListByResourceGroup, armoracledatabase:ExadbVMClusters.ListBySubscription |
| oracle.database | oracle.database/exadbvmclusters/dbnodes | 1 | resource-group | arm-envelope | armoracledatabase:ExascaleDbNodes.ListByParent |
| oracle.database | oracle.database/exascaledbstoragevaults | 0 | subscription | item-write | armoracledatabase:ExascaleDbStorageVaults.ListByResourceGroup, armoracledatabase:ExascaleDbStorageVaults.ListBySubscription |
| oracle.database | oracle.database/networkanchors | 0 | subscription | item-write | armoracledatabase:NetworkAnchors.ListByResourceGroup, armoracledatabase:NetworkAnchors.ListBySubscription |
| oracle.database | oracle.database/oraclesubscriptions | 0 | subscription | item-write | armoracledatabase:OracleSubscriptions.ListBySubscription |
| oracle.database | oracle.database/resourceanchors | 0 | subscription | item-write | armoracledatabase:ResourceAnchors.ListByResourceGroup, armoracledatabase:ResourceAnchors.ListBySubscription |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/firewalls | 0 | subscription | item-write | armpanngfw:Firewalls.ListByResourceGroup, armpanngfw:Firewalls.ListBySubscription |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/firewalls/metrics | 1 | resource-group | item-write | armpanngfw:MetricsObjectFirewall.ListByFirewalls |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/firewalls/statuses | 1 | resource-group | arm-envelope | armpanngfw:FirewallStatus.ListByFirewalls |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/globalrulestacks | 0 | tenant | item-write | armpanngfw:GlobalRulestack.List |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/globalrulestacks/certificates | 1 | tenant | item-write | armpanngfw:CertificateObjectGlobalRulestack.List |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/globalrulestacks/fqdnlists | 1 | tenant | item-write | armpanngfw:FqdnListGlobalRulestack.List |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/globalrulestacks/postrules | 1 | tenant | item-write | armpanngfw:PostRules.List |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/globalrulestacks/prefixlists | 1 | tenant | item-write | armpanngfw:PrefixListGlobalRulestack.List |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/globalrulestacks/prerules | 1 | tenant | item-write | armpanngfw:PreRules.List |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/localrulestacks | 0 | subscription | item-write | armpanngfw:LocalRulestacks.ListByResourceGroup, armpanngfw:LocalRulestacks.ListBySubscription |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/localrulestacks/certificates | 1 | resource-group | item-write | armpanngfw:CertificateObjectLocalRulestack.ListByLocalRulestacks |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/localrulestacks/fqdnlists | 1 | resource-group | item-write | armpanngfw:FqdnListLocalRulestack.ListByLocalRulestacks |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/localrulestacks/localrules | 1 | resource-group | item-write | armpanngfw:LocalRules.ListByLocalRulestacks |
| paloaltonetworks.cloudngfw | paloaltonetworks.cloudngfw/localrulestacks/prefixlists | 1 | resource-group | item-write | armpanngfw:PrefixListLocalRulestack.ListByLocalRulestacks |
| pinecone.vectordb | pinecone.vectordb/organizations | 0 | subscription | item-write | armpineconevectordb:Organizations.ListByResourceGroup, armpineconevectordb:Organizations.ListBySubscription |
| purestorage.block | purestorage.block/reservations | 0 | subscription | item-write | armpurestorageblock:Reservations.ListByResourceGroup, armpurestorageblock:Reservations.ListBySubscription |
| purestorage.block | purestorage.block/storagepools | 0 | subscription | item-write | armpurestorageblock:StoragePools.ListByResourceGroup, armpurestorageblock:StoragePools.ListBySubscription |
| purestorage.block | purestorage.block/storagepools/avsstoragecontainers | 1 | resource-group | item-write | armpurestorageblock:AvsStorageContainers.ListByStoragePool |
| purestorage.block | purestorage.block/storagepools/avsstoragecontainers/volumes | 2 | resource-group | item-write | armpurestorageblock:AvsStorageContainerVolumes.ListByAvsStorageContainer |
| purestorage.block | purestorage.block/storagepools/avsvms | 1 | resource-group | item-write | armpurestorageblock:AvsVMs.ListByStoragePool |
| purestorage.block | purestorage.block/storagepools/avsvms/avsvmvolumes | 2 | resource-group | item-write | armpurestorageblock:AvsVMVolumes.ListByAvsVM |
| purestorage.block | purestorage.block/storagepools/recoverablevolumegroups | 1 | resource-group | item-write | armpurestorageblock:RecoverableVolumeGroups.ListByStoragePool |
| purestorage.block | purestorage.block/storagepools/volumegroups | 1 | resource-group | item-write | armpurestorageblock:VolumeGroups.ListByStoragePool |
| purestorage.block | purestorage.block/storagepools/volumegroups/snapshots | 2 | resource-group | item-write | armpurestorageblock:VolumeGroupSnapshots.ListByVolumeGroup |
| purestorage.block | purestorage.block/storagepools/volumegroups/volumes | 2 | resource-group | item-write | armpurestorageblock:Volumes.ListByVolumeGroup |
| qumulo.storage | qumulo.storage/filesystems | 0 | subscription | item-write | armqumulo:FileSystems.ListByResourceGroup, armqumulo:FileSystems.ListBySubscription |


## GCP

**Coverage:** 23.2% (240/1034 listable) · depth0 28.0% · depth1 18.6% · depth2 11.1% · depth3 0.0% · depth4 0.0% · depth5 0.0% · attribute 0 · excluded 626 · disco-only 5 (0 unexplained)

Pins: google.golang.org/api@v0.292.0

The denominator is every candidate the provider's own SDK can list that the extractor classified `resource` — not every API operation, and not a curated list. Attributes (detail reads), catalogs (provider-published, read-only) and non-resources are outside it and are listed below. Each row names the rule that classified it; the table below gives the coverage of each rule, so a rule that admits rows no scanner can close is visible as a low percentage rather than as a smaller number.

| Admitting rule | Covered | Uncovered | % |
|---|---|---|---|
| create | 224 | 731 | 23.5 |
| delete-only | 9 | 41 | 18.0 |
| mutable | 5 | 21 | 19.2 |
| created-elsewhere | 2 | 1 | 66.7 |

| Service | Covered | Uncovered | % |
|---|---|---|---|
| aiplatform | 0 | 58 | 0.0 |
| dialogflow | 0 | 44 | 0.0 |
| apigee | 0 | 43 | 0.0 |
| contactcenterinsights | 0 | 28 | 0.0 |
| discoveryengine | 0 | 26 | 0.0 |
| dataplex | 0 | 25 | 0.0 |
| networksecurity | 0 | 25 | 0.0 |
| admin | 1 | 20 | 4.8 |
| networkservices | 0 | 19 | 0.0 |
| networkconnectivity | 0 | 16 | 0.0 |
| vmwareengine | 0 | 14 | 0.0 |
| apihub | 0 | 13 | 0.0 |
| gkehub | 0 | 11 | 0.0 |
| netapp | 0 | 11 | 0.0 |
| oracledatabase | 0 | 11 | 0.0 |
| compute | 92 | 10 | 90.2 |
| dlp | 0 | 10 | 0.0 |
| healthcare | 0 | 10 | 0.0 |
| migrationcenter | 0 | 10 | 0.0 |
| vmmigration | 0 | 10 | 0.0 |
| apigeeregistry | 0 | 9 | 0.0 |
| ces | 0 | 9 | 0.0 |
| appengine | 0 | 8 | 0.0 |
| connectors | 0 | 8 | 0.0 |
| integrations | 0 | 8 | 0.0 |
| saasservicemgmt | 0 | 8 | 0.0 |
| securesourcemanager | 0 | 8 | 0.0 |
| clouddeploy | 0 | 7 | 0.0 |
| dataform | 0 | 7 | 0.0 |
| eventarc | 0 | 7 | 0.0 |
| identitytoolkit | 0 | 7 | 0.0 |
| managedkafka | 0 | 7 | 0.0 |
| securitycenter | 0 | 7 | 0.0 |
| backupdr | 0 | 6 | 0.0 |
| baremetalsolution | 0 | 6 | 0.0 |
| datacatalog | 0 | 6 | 0.0 |
| datamigration | 0 | 6 | 0.0 |
| gkebackup | 0 | 6 | 0.0 |
| gkeonprem | 0 | 6 | 0.0 |
| redis | 0 | 6 | 0.0 |
| translate | 0 | 6 | 0.0 |
| beyondcorp | 0 | 5 | 0.0 |
| datastream | 0 | 5 | 0.0 |
| developerconnect | 0 | 5 | 0.0 |
| jobs | 0 | 5 | 0.0 |
| metastore | 0 | 5 | 0.0 |
| ml | 0 | 5 | 0.0 |
| notebooks | 0 | 5 | 0.0 |
| privateca | 0 | 5 | 0.0 |
| retail | 0 | 5 | 0.0 |
| alloydb | 0 | 4 | 0.0 |
| analyticshub | 0 | 4 | 0.0 |
| apphub | 0 | 4 | 0.0 |
| bigqueryreservation | 0 | 4 | 0.0 |
| cloudsupport | 0 | 4 | 0.0 |
| documentai | 0 | 4 | 0.0 |
| firebaseappdistribution | 0 | 4 | 0.0 |
| firebaseapphosting | 0 | 4 | 0.0 |
| osconfig | 0 | 4 | 0.0 |
| parametermanager | 0 | 4 | 0.0 |
| workloadmanager | 0 | 4 | 0.0 |
| apigateway | 0 | 3 | 0.0 |
| biglake | 0 | 3 | 0.0 |
| cloudbuild | 5 | 3 | 62.5 |
| config | 0 | 3 | 0.0 |
| contentwarehouse | 0 | 3 | 0.0 |
| datalineage | 0 | 3 | 0.0 |
| file | 0 | 3 | 0.0 |
| firebaseappcheck | 0 | 3 | 0.0 |
| firebasedataconnect | 0 | 3 | 0.0 |
| health | 0 | 3 | 0.0 |
| iam | 12 | 3 | 80.0 |
| iap | 0 | 3 | 0.0 |
| managedidentities | 0 | 3 | 0.0 |
| networkmanagement | 0 | 3 | 0.0 |
| pubsublite | 0 | 3 | 0.0 |
| servicedirectory | 0 | 3 | 0.0 |
| servicemanagement | 0 | 3 | 0.0 |
| vision | 0 | 3 | 0.0 |
| workstations | 0 | 3 | 0.0 |
| agentidentity | 0 | 2 | 0.0 |
| agentregistry | 0 | 2 | 0.0 |
| artifactregistry | 5 | 2 | 71.4 |
| bigquerydatatransfer | 0 | 2 | 0.0 |
| cloudasset | 0 | 2 | 0.0 |
| cloudbilling | 0 | 2 | 0.0 |
| cloudiot | 0 | 2 | 0.0 |
| cloudtasks | 0 | 2 | 0.0 |
| containeranalysis | 0 | 2 | 0.0 |
| datafusion | 0 | 2 | 0.0 |
| firebaserules | 0 | 2 | 0.0 |
| firestore | 4 | 2 | 66.7 |
| looker | 0 | 2 | 0.0 |
| observability | 0 | 2 | 0.0 |
| orgpolicy | 0 | 2 | 0.0 |
| recaptchaenterprise | 0 | 2 | 0.0 |
| securityposture | 0 | 2 | 0.0 |
| servicenetworking | 0 | 2 | 0.0 |
| speech | 0 | 2 | 0.0 |
| storage | 8 | 2 | 80.0 |
| storagetransfer | 0 | 2 | 0.0 |
| tpu | 0 | 2 | 0.0 |
| transcoder | 0 | 2 | 0.0 |
| websecurityscanner | 0 | 2 | 0.0 |
| apikeys | 0 | 1 | 0.0 |
| assuredworkloads | 0 | 1 | 0.0 |
| bigquery | 5 | 1 | 83.3 |
| bigquerydatapolicy | 0 | 1 | 0.0 |
| billingbudgets | 0 | 1 | 0.0 |
| binaryauthorization | 1 | 1 | 50.0 |
| blockchainnodeengine | 0 | 1 | 0.0 |
| cloudcommerceprocurement | 0 | 1 | 0.0 |
| cloudcontrolspartner | 0 | 1 | 0.0 |
| clouddebugger | 0 | 1 | 0.0 |
| cloudkms | 7 | 1 | 87.5 |
| cloudprofiler | 0 | 1 | 0.0 |
| cloudscheduler | 0 | 1 | 0.0 |
| composer | 2 | 1 | 66.7 |
| datapipelines | 0 | 1 | 0.0 |
| datastore | 0 | 1 | 0.0 |
| deploymentmanager | 0 | 1 | 0.0 |
| dns | 5 | 1 | 83.3 |
| domains | 0 | 1 | 0.0 |
| essentialcontacts | 0 | 1 | 0.0 |
| hypercomputecluster | 0 | 1 | 0.0 |
| ids | 0 | 1 | 0.0 |
| memcache | 0 | 1 | 0.0 |
| monitoring | 8 | 1 | 88.9 |
| parallelstore | 0 | 1 | 0.0 |
| policysimulator | 0 | 1 | 0.0 |
| rapidmigrationassessment | 0 | 1 | 0.0 |
| resourcesettings | 0 | 1 | 0.0 |
| run | 8 | 1 | 88.9 |
| serviceconsumermanagement | 0 | 1 | 0.0 |
| sourcerepo | 0 | 1 | 0.0 |
| spanner | 6 | 1 | 85.7 |
| sqladmin | 5 | 1 | 83.3 |
| storagebatchoperations | 0 | 1 | 0.0 |
| testing | 0 | 1 | 0.0 |
| vpcaccess | 0 | 1 | 0.0 |
| workflowexecutions | 0 | 1 | 0.0 |
| workflows | 0 | 1 | 0.0 |
| accesscontextmanager | 5 | 0 | 100.0 |
| batch | 1 | 0 | 100.0 |
| bigqueryconnection | 1 | 0 | 100.0 |
| bigtableadmin | 9 | 0 | 100.0 |
| certificatemanager | 6 | 0 | 100.0 |
| cloudfunctions | 1 | 0 | 100.0 |
| cloudidentity | 10 | 0 | 100.0 |
| cloudresourcemanager | 8 | 0 | 100.0 |
| container | 2 | 0 | 100.0 |
| dataflow | 2 | 0 | 100.0 |
| dataproc | 7 | 0 | 100.0 |
| logging | 8 | 0 | 100.0 |
| pubsub | 4 | 0 | 100.0 |
| secretmanager | 2 | 0 | 100.0 |

### Uncovered (listable, no scanner) (794)

| Service | Key | Depth | Scope | Rule | Ops |
| --- | --- | --- | --- | --- | --- |
| admin | admin/asps | 1 | global | delete-only | admin:asps.list |
| admin | admin/chrome/printers | 0 | tenant | create | admin:customers.chrome.printers.list |
| admin | admin/chrome/printservers | 0 | tenant | create | admin:customers.chrome.printServers.list |
| admin | admin/chromeosdevices | 0 | tenant | mutable | admin:chromeosdevices.list |
| admin | admin/domainaliases | 0 | tenant | create | admin:domainAliases.list |
| admin | admin/domains | 0 | tenant | create | admin:domains.list |
| admin | admin/groups | 0 | global | create | admin:groups.list |
| admin | admin/groups/aliases | 1 | global | create | admin:groups.aliases.list |
| admin | admin/members | 1 | global | create | admin:members.list |
| admin | admin/mobiledevices | 0 | tenant | delete-only | admin:mobiledevices.list |
| admin | admin/orgunits | 0 | tenant | create | admin:orgunits.list |
| admin | admin/resources/buildings | 0 | tenant | create | admin:resources.buildings.list |
| admin | admin/resources/calendars | 0 | tenant | create | admin:resources.calendars.list |
| admin | admin/resources/features | 0 | tenant | create | admin:resources.features.list |
| admin | admin/roleassignments | 0 | tenant | create | admin:roleAssignments.list |
| admin | admin/roles | 0 | tenant | create | admin:roles.list |
| admin | admin/schemas | 0 | tenant | create | admin:schemas.list |
| admin | admin/tokens | 1 | global | delete-only | admin:tokens.list |
| admin | admin/transfers | 0 | global | create | admin:transfers.list |
| admin | admin/users/aliases | 1 | global | create | admin:users.aliases.list |
| agentidentity | agentidentity/authproviders | 0 | project | create | agentidentity:projects.locations.authProviders.list |
| agentidentity | agentidentity/authproviders/authorizations | 1 | project | delete-only | agentidentity:projects.locations.authProviders.authorizations.list |
| agentregistry | agentregistry/bindings | 0 | project | create | agentregistry:projects.locations.bindings.fetchAvailable, agentregistry:projects.locations.bindings.list |
| agentregistry | agentregistry/services | 0 | project | create | agentregistry:projects.locations.services.list |
| aiplatform | aiplatform/agents | 0 | project | create | aiplatform:projects.locations.agents.list |
| aiplatform | aiplatform/batchpredictionjobs | 0 | project | create | aiplatform:batchPredictionJobs.list, aiplatform:projects.locations.batchPredictionJobs.list |
| aiplatform | aiplatform/cachedcontents | 0 | project | create | aiplatform:projects.locations.cachedContents.list |
| aiplatform | aiplatform/customjobs | 0 | project | create | aiplatform:projects.locations.customJobs.list |
| aiplatform | aiplatform/datalabelingjobs | 0 | project | create | aiplatform:projects.locations.dataLabelingJobs.list |
| aiplatform | aiplatform/datasets | 0 | project | create | aiplatform:datasets.list, aiplatform:projects.locations.datasets.list |
| aiplatform | aiplatform/datasets/datasetversions | 1 | project | create | aiplatform:datasets.datasetVersions.list, aiplatform:projects.locations.datasets.datasetVersions.list |
| aiplatform | aiplatform/datasets/savedqueries | 1 | project | delete-only | aiplatform:projects.locations.datasets.savedQueries.list |
| aiplatform | aiplatform/deploymentresourcepools | 0 | project | create | aiplatform:projects.locations.deploymentResourcePools.list |
| aiplatform | aiplatform/endpoints | 0 | project | create | aiplatform:projects.locations.endpoints.list |
| aiplatform | aiplatform/evaluationitems | 0 | project | create | aiplatform:projects.locations.evaluationItems.list |
| aiplatform | aiplatform/evaluationmetrics | 0 | project | create | aiplatform:projects.locations.evaluationMetrics.list |
| aiplatform | aiplatform/evaluationruns | 0 | project | create | aiplatform:projects.locations.evaluationRuns.list |
| aiplatform | aiplatform/evaluationsets | 0 | project | create | aiplatform:projects.locations.evaluationSets.list |
| aiplatform | aiplatform/featuregroups | 0 | project | create | aiplatform:projects.locations.featureGroups.list |
| aiplatform | aiplatform/featuregroups/features | 1 | project | create | aiplatform:projects.locations.featureGroups.features.list |
| aiplatform | aiplatform/featureonlinestores | 0 | project | create | aiplatform:projects.locations.featureOnlineStores.list |
| aiplatform | aiplatform/featureonlinestores/featureviews | 1 | project | create | aiplatform:projects.locations.featureOnlineStores.featureViews.list |
| aiplatform | aiplatform/featurestores | 0 | project | create | aiplatform:projects.locations.featurestores.list |
| aiplatform | aiplatform/featurestores/entitytypes | 1 | project | create | aiplatform:projects.locations.featurestores.entityTypes.list |
| aiplatform | aiplatform/featurestores/entitytypes/features | 2 | project | create | aiplatform:projects.locations.featurestores.entityTypes.features.list |
| aiplatform | aiplatform/hyperparametertuningjobs | 0 | project | create | aiplatform:projects.locations.hyperparameterTuningJobs.list |
| aiplatform | aiplatform/indexendpoints | 0 | project | create | aiplatform:projects.locations.indexEndpoints.list |
| aiplatform | aiplatform/indexes | 0 | project | create | aiplatform:projects.locations.indexes.list |
| aiplatform | aiplatform/metadatastores | 0 | project | create | aiplatform:projects.locations.metadataStores.list |
| aiplatform | aiplatform/metadatastores/artifacts | 1 | project | create | aiplatform:projects.locations.metadataStores.artifacts.list |
| aiplatform | aiplatform/metadatastores/contexts | 1 | project | create | aiplatform:projects.locations.metadataStores.contexts.list |
| aiplatform | aiplatform/metadatastores/executions | 1 | project | create | aiplatform:projects.locations.metadataStores.executions.list |
| aiplatform | aiplatform/metadatastores/metadataschemas | 1 | project | create | aiplatform:projects.locations.metadataStores.metadataSchemas.list |
| aiplatform | aiplatform/modeldeploymentmonitoringjobs | 0 | project | create | aiplatform:projects.locations.modelDeploymentMonitoringJobs.list |
| aiplatform | aiplatform/models | 0 | project | delete-only | aiplatform:projects.locations.models.list |
| aiplatform | aiplatform/models/evaluations | 1 | project | create | aiplatform:projects.locations.models.evaluations.list |
| aiplatform | aiplatform/nasjobs | 0 | project | create | aiplatform:projects.locations.nasJobs.list |
| aiplatform | aiplatform/notebookexecutionjobs | 0 | project | create | aiplatform:projects.locations.notebookExecutionJobs.list |
| aiplatform | aiplatform/notebookruntimes | 0 | project | delete-only | aiplatform:projects.locations.notebookRuntimes.list |
| aiplatform | aiplatform/notebookruntimetemplates | 0 | project | create | aiplatform:projects.locations.notebookRuntimeTemplates.list |
| aiplatform | aiplatform/onlineevaluators | 0 | project | create | aiplatform:projects.locations.onlineEvaluators.list |
| aiplatform | aiplatform/persistentresources | 0 | project | create | aiplatform:projects.locations.persistentResources.list |
| aiplatform | aiplatform/pipelinejobs | 0 | project | create | aiplatform:projects.locations.pipelineJobs.list |
| aiplatform | aiplatform/ragcorpora | 0 | project | create | aiplatform:projects.locations.ragCorpora.list |
| aiplatform | aiplatform/ragcorpora/ragfiles | 1 | project | create | aiplatform:projects.locations.ragCorpora.ragFiles.list |
| aiplatform | aiplatform/reasoningengines | 0 | project | create | aiplatform:projects.locations.reasoningEngines.list, aiplatform:reasoningEngines.list |
| aiplatform | aiplatform/reasoningengines/memories | 1 | project | create | aiplatform:projects.locations.memoryBanks.memories.list, aiplatform:projects.locations.reasoningEngines.memories.list, aiplatform:reasoningEngines.memories.list |
| aiplatform | aiplatform/reasoningengines/sandboxenvironments | 1 | project | create | aiplatform:projects.locations.reasoningEngines.sandboxEnvironments.list, aiplatform:reasoningEngines.sandboxEnvironments.list |
| aiplatform | aiplatform/reasoningengines/sandboxenvironmentsnapshots | 1 | project | delete-only | aiplatform:projects.locations.reasoningEngines.sandboxEnvironmentSnapshots.list, aiplatform:reasoningEngines.sandboxEnvironmentSnapshots.list |
| aiplatform | aiplatform/reasoningengines/sandboxenvironmenttemplates | 1 | project | create | aiplatform:projects.locations.reasoningEngines.sandboxEnvironmentTemplates.list, aiplatform:reasoningEngines.sandboxEnvironmentTemplates.list |
| aiplatform | aiplatform/reasoningengines/sessions | 1 | project | create | aiplatform:projects.locations.reasoningEngines.sessions.list, aiplatform:reasoningEngines.sessions.list |
| aiplatform | aiplatform/schedules | 0 | project | create | aiplatform:projects.locations.schedules.list |
| aiplatform | aiplatform/semanticgovernancepolicies | 0 | project | create | aiplatform:projects.locations.semanticGovernancePolicies.list |
| aiplatform | aiplatform/specialistpools | 0 | project | create | aiplatform:projects.locations.specialistPools.list |
| aiplatform | aiplatform/studies | 0 | project | create | aiplatform:projects.locations.studies.list |
| aiplatform | aiplatform/studies/trials | 1 | project | create | aiplatform:projects.locations.studies.trials.list |
| aiplatform | aiplatform/tensorboards | 0 | project | create | aiplatform:projects.locations.tensorboards.list |
| aiplatform | aiplatform/tensorboards/experiments | 1 | project | create | aiplatform:projects.locations.tensorboards.experiments.list |
| aiplatform | aiplatform/tensorboards/experiments/runs | 2 | project | create | aiplatform:projects.locations.tensorboards.experiments.runs.list |
| aiplatform | aiplatform/tensorboards/experiments/runs/timeseries | 3 | project | create | aiplatform:projects.locations.tensorboards.experiments.runs.timeSeries.list |
| aiplatform | aiplatform/trainingpipelines | 0 | project | create | aiplatform:projects.locations.trainingPipelines.list |
| aiplatform | aiplatform/tuningjobs | 0 | project | create | aiplatform:projects.locations.tuningJobs.list |
| alloydb | alloydb/backups | 0 | project | create | alloydb:projects.locations.backups.list |
| alloydb | alloydb/clusters | 0 | project | create | alloydb:projects.locations.clusters.list |
| alloydb | alloydb/clusters/instances | 1 | project | create | alloydb:projects.locations.clusters.instances.list |
| alloydb | alloydb/clusters/users | 1 | project | create | alloydb:projects.locations.clusters.users.list |
| analyticshub | analyticshub/dataexchanges | 0 | project | create | analyticshub:projects.locations.dataExchanges.list |
| analyticshub | analyticshub/dataexchanges/listings | 1 | project | create | analyticshub:projects.locations.dataExchanges.listings.list |
| analyticshub | analyticshub/dataexchanges/querytemplates | 1 | project | create | analyticshub:projects.locations.dataExchanges.queryTemplates.list |
| analyticshub | analyticshub/subscriptions | 0 | project | delete-only | analyticshub:projects.locations.subscriptions.list |
| apigateway | apigateway/apis | 0 | project | create | apigateway:projects.locations.apis.list |
| apigateway | apigateway/apis/configs | 1 | project | create | apigateway:projects.locations.apis.configs.list |
| apigateway | apigateway/gateways | 0 | project | create | apigateway:projects.locations.gateways.list |
| apigee | apigee/organizations/analytics/datastores | 1 | org | create | apigee:organizations.analytics.datastores.list |
| apigee | apigee/organizations/apimserviceextensions | 1 | org | create | apigee:organizations.apimServiceExtensions.list |
| apigee | apigee/organizations/apiproducts | 1 | org | create | apigee:organizations.apiproducts.list |
| apigee | apigee/organizations/apiproducts/attributes | 2 | org | create | apigee:organizations.apiproducts.attributes.list |
| apigee | apigee/organizations/apiproducts/rateplans | 2 | org | create | apigee:organizations.apiproducts.rateplans.list |
| apigee | apigee/organizations/apis | 1 | org | create | apigee:organizations.apis.list |
| apigee | apigee/organizations/apis/keyvaluemaps/entries | 3 | org | create | apigee:organizations.apis.keyvaluemaps.entries.list |
| apigee | apigee/organizations/appgroups | 1 | org | create | apigee:organizations.appgroups.list |
| apigee | apigee/organizations/appgroups/apps | 2 | org | create | apigee:organizations.appgroups.apps.list |
| apigee | apigee/organizations/appgroups/subscriptions | 2 | org | create | apigee:organizations.appgroups.subscriptions.list |
| apigee | apigee/organizations/datacollectors | 1 | org | create | apigee:organizations.datacollectors.list |
| apigee | apigee/organizations/developers | 1 | org | create | apigee:organizations.developers.list |
| apigee | apigee/organizations/developers/apps | 2 | org | create | apigee:organizations.developers.apps.list |
| apigee | apigee/organizations/developers/apps/attributes | 3 | org | create | apigee:organizations.developers.apps.attributes.list |
| apigee | apigee/organizations/developers/attributes | 2 | org | create | apigee:organizations.developers.attributes.list |
| apigee | apigee/organizations/developers/subscriptions | 2 | org | create | apigee:organizations.developers.subscriptions.list |
| apigee | apigee/organizations/dnszones | 1 | org | create | apigee:organizations.dnsZones.list |
| apigee | apigee/organizations/endpointattachments | 1 | org | create | apigee:organizations.endpointAttachments.list |
| apigee | apigee/organizations/envgroups | 1 | org | create | apigee:organizations.envgroups.list |
| apigee | apigee/organizations/envgroups/attachments | 2 | org | create | apigee:organizations.envgroups.attachments.list |
| apigee | apigee/organizations/environments/analytics/exports | 2 | org | create | apigee:organizations.environments.analytics.exports.list |
| apigee | apigee/organizations/environments/archivedeployments | 2 | org | create | apigee:organizations.environments.archiveDeployments.list |
| apigee | apigee/organizations/environments/keyvaluemaps/entries | 3 | org | create | apigee:organizations.environments.keyvaluemaps.entries.list |
| apigee | apigee/organizations/environments/queries | 2 | org | create | apigee:organizations.environments.queries.list |
| apigee | apigee/organizations/environments/securityactions | 2 | org | create | apigee:organizations.environments.securityActions.list |
| apigee | apigee/organizations/environments/securityincidents | 2 | org | mutable | apigee:organizations.environments.securityIncidents.list |
| apigee | apigee/organizations/environments/securityreports | 2 | org | create | apigee:organizations.environments.securityReports.list |
| apigee | apigee/organizations/environments/traceconfig/overrides | 2 | org | create | apigee:organizations.environments.traceConfig.overrides.list |
| apigee | apigee/organizations/hostqueries | 1 | org | create | apigee:organizations.hostQueries.list |
| apigee | apigee/organizations/hostsecurityreports | 1 | org | create | apigee:organizations.hostSecurityReports.list |
| apigee | apigee/organizations/instances | 1 | org | create | apigee:organizations.instances.list |
| apigee | apigee/organizations/instances/attachments | 2 | org | create | apigee:organizations.instances.attachments.list |
| apigee | apigee/organizations/instances/nataddresses | 2 | org | create | apigee:organizations.instances.natAddresses.list |
| apigee | apigee/organizations/keyvaluemaps/entries | 2 | org | create | apigee:organizations.keyvaluemaps.entries.list |
| apigee | apigee/organizations/reports | 1 | org | create | apigee:organizations.reports.list |
| apigee | apigee/organizations/securityfeedback | 1 | org | create | apigee:organizations.securityFeedback.list |
| apigee | apigee/organizations/securitymonitoringconditions | 1 | org | create | apigee:organizations.securityMonitoringConditions.list |
| apigee | apigee/organizations/securityprofiles | 1 | org | create | apigee:organizations.securityProfiles.list |
| apigee | apigee/organizations/securityprofilesv2 | 1 | org | create | apigee:organizations.securityProfilesV2.list |
| apigee | apigee/organizations/sharedflows | 1 | org | create | apigee:organizations.sharedflows.list |
| apigee | apigee/organizations/sites/apicategories | 2 | org | create | apigee:organizations.sites.apicategories.list |
| apigee | apigee/organizations/sites/apidocs | 2 | org | create | apigee:organizations.sites.apidocs.list |
| apigee | apigee/organizations/spaces | 1 | org | create | apigee:organizations.spaces.list |
| apigeeregistry | apigeeregistry/apis | 0 | project | create | apigeeregistry:projects.locations.apis.list |
| apigeeregistry | apigeeregistry/apis/artifacts | 1 | project | create | apigeeregistry:projects.locations.apis.artifacts.list |
| apigeeregistry | apigeeregistry/apis/deployments | 1 | project | create | apigeeregistry:projects.locations.apis.deployments.list |
| apigeeregistry | apigeeregistry/apis/deployments/artifacts | 2 | project | create | apigeeregistry:projects.locations.apis.deployments.artifacts.list |
| apigeeregistry | apigeeregistry/apis/versions | 1 | project | create | apigeeregistry:projects.locations.apis.versions.list |
| apigeeregistry | apigeeregistry/apis/versions/artifacts | 2 | project | create | apigeeregistry:projects.locations.apis.versions.artifacts.list |
| apigeeregistry | apigeeregistry/apis/versions/specs | 2 | project | create | apigeeregistry:projects.locations.apis.versions.specs.list |
| apigeeregistry | apigeeregistry/apis/versions/specs/artifacts | 3 | project | create | apigeeregistry:projects.locations.apis.versions.specs.artifacts.list |
| apigeeregistry | apigeeregistry/artifacts | 0 | project | create | apigeeregistry:projects.locations.artifacts.list |
| apihub | apihub/apis | 0 | project | create | apihub:projects.locations.apis.list |
| apihub | apihub/apis/versions | 1 | project | create | apihub:projects.locations.apis.versions.list |
| apihub | apihub/apis/versions/operations | 2 | project | create | apihub:projects.locations.apis.versions.operations.list |
| apihub | apihub/apis/versions/specs | 2 | project | create | apihub:projects.locations.apis.versions.specs.list |
| apihub | apihub/attributes | 0 | project | create | apihub:projects.locations.attributes.list |
| apihub | apihub/curations | 0 | project | create | apihub:projects.locations.curations.list |
| apihub | apihub/dependencies | 0 | project | create | apihub:projects.locations.dependencies.list |
| apihub | apihub/deployments | 0 | project | create | apihub:projects.locations.deployments.list |
| apihub | apihub/externalapis | 0 | project | create | apihub:projects.locations.externalApis.list |
| apihub | apihub/hostprojectregistrations | 0 | project | create | apihub:projects.locations.hostProjectRegistrations.list |
| apihub | apihub/plugins | 0 | project | create | apihub:projects.locations.plugins.list |
| apihub | apihub/plugins/instances | 1 | project | create | apihub:projects.locations.plugins.instances.list |
| apihub | apihub/runtimeprojectattachments | 0 | project | create | apihub:projects.locations.runtimeProjectAttachments.list |
| apikeys | apikeys/keys | 0 | project | create | apikeys:projects.locations.keys.list |
| appengine | appengine/applications/authorizedcertificates | 1 | project | create | appengine:projects.locations.applications.authorizedCertificates.list |
| appengine | appengine/applications/domainmappings | 1 | project | create | appengine:projects.locations.applications.domainMappings.list |
| appengine | appengine/apps/authorizedcertificates | 1 | global | create | appengine:apps.authorizedCertificates.list |
| appengine | appengine/apps/domainmappings | 1 | global | create | appengine:apps.domainMappings.list |
| appengine | appengine/apps/firewall/ingressrules | 1 | global | create | appengine:apps.firewall.ingressRules.list |
| appengine | appengine/apps/services | 1 | global | delete-only | appengine:apps.services.list |
| appengine | appengine/apps/services/versions | 2 | global | create | appengine:apps.services.versions.list |
| appengine | appengine/apps/services/versions/instances | 3 | global | delete-only | appengine:apps.services.versions.instances.list |
| apphub | apphub/applications | 0 | project | create | apphub:projects.locations.applications.list |
| apphub | apphub/applications/services | 1 | project | create | apphub:projects.locations.applications.services.list |
| apphub | apphub/applications/workloads | 1 | project | create | apphub:projects.locations.applications.workloads.list |
| apphub | apphub/serviceprojectattachments | 0 | project | create | apphub:projects.locations.serviceProjectAttachments.list |
| artifactregistry | artifactregistry/repositories/files | 1 | project | delete-only | artifactregistry:projects.locations.repositories.files.list |
| artifactregistry | artifactregistry/repositories/packages/versions | 2 | project | delete-only | artifactregistry:projects.locations.repositories.packages.versions.list |
| assuredworkloads | assuredworkloads/workloads | 0 | org | create | assuredworkloads:organizations.locations.workloads.list |
| backupdr | backupdr/backupplanassociations | 0 | project | create | backupdr:projects.locations.backupPlanAssociations.fetchForResourceType, backupdr:projects.locations.backupPlanAssociations.list |
| backupdr | backupdr/backupplans | 0 | project | create | backupdr:projects.locations.backupPlans.list |
| backupdr | backupdr/backupvaults | 0 | project | create | backupdr:projects.locations.backupVaults.fetchUsable, backupdr:projects.locations.backupVaults.list |
| backupdr | backupdr/backupvaults/datasources | 1 | project | mutable | backupdr:projects.locations.backupVaults.dataSources.list |
| backupdr | backupdr/backupvaults/datasources/backups | 2 | project | delete-only | backupdr:projects.locations.backupVaults.dataSources.backups.fetchForResourceType, backupdr:projects.locations.backupVaults.dataSources.backups.list |
| backupdr | backupdr/managementservers | 0 | project | create | backupdr:projects.locations.managementServers.list |
| baremetalsolution | baremetalsolution/instances | 0 | project | mutable | baremetalsolution:projects.locations.instances.list |
| baremetalsolution | baremetalsolution/networks | 0 | project | mutable | baremetalsolution:projects.locations.networks.list, baremetalsolution:projects.locations.networks.listNetworkUsage |
| baremetalsolution | baremetalsolution/nfsshares | 0 | project | create | baremetalsolution:projects.locations.nfsShares.list |
| baremetalsolution | baremetalsolution/sshkeys | 0 | project | create | baremetalsolution:projects.locations.sshKeys.list |
| baremetalsolution | baremetalsolution/volumes | 0 | project | mutable | baremetalsolution:projects.locations.volumes.list |
| baremetalsolution | baremetalsolution/volumes/snapshots | 1 | project | create | baremetalsolution:projects.locations.volumes.snapshots.list |
| beyondcorp | beyondcorp/appconnections | 0 | project | create | beyondcorp:projects.locations.appConnections.list, beyondcorp:projects.locations.appConnections.resolve |
| beyondcorp | beyondcorp/appconnectors | 0 | project | create | beyondcorp:projects.locations.appConnectors.list |
| beyondcorp | beyondcorp/appgateways | 0 | project | create | beyondcorp:projects.locations.appGateways.list |
| beyondcorp | beyondcorp/securitygateways | 0 | project | create | beyondcorp:projects.locations.securityGateways.list |
| beyondcorp | beyondcorp/securitygateways/applications | 1 | project | create | beyondcorp:projects.locations.securityGateways.applications.list |
| biglake | biglake/catalogs | 0 | project | create | biglake:projects.locations.catalogs.list |
| biglake | biglake/catalogs/databases | 1 | project | create | biglake:projects.locations.catalogs.databases.list |
| biglake | biglake/catalogs/databases/tables | 2 | project | create | biglake:projects.locations.catalogs.databases.tables.list |
| bigquery | bigquery/jobs | 0 | project | create | bigquery:jobs.list |
| bigquerydatapolicy | bigquerydatapolicy/datapolicies | 0 | project | create | bigquerydatapolicy:projects.locations.dataPolicies.list |
| bigquerydatatransfer | bigquerydatatransfer/transferconfigs | 0 | project | create | bigquerydatatransfer:projects.locations.transferConfigs.list, bigquerydatatransfer:projects.transferConfigs.list |
| bigquerydatatransfer | bigquerydatatransfer/transferconfigs/runs | 1 | project | delete-only | bigquerydatatransfer:projects.locations.transferConfigs.runs.list, bigquerydatatransfer:projects.transferConfigs.runs.list |
| bigqueryreservation | bigqueryreservation/capacitycommitments | 0 | project | create | bigqueryreservation:projects.locations.capacityCommitments.list |
| bigqueryreservation | bigqueryreservation/reservationgroups | 0 | project | create | bigqueryreservation:projects.locations.reservationGroups.list |
| bigqueryreservation | bigqueryreservation/reservations | 0 | project | create | bigqueryreservation:projects.locations.reservations.list |
| bigqueryreservation | bigqueryreservation/reservations/assignments | 1 | project | create | bigqueryreservation:projects.locations.reservations.assignments.list |
| billingbudgets | billingbudgets/budgets | 0 | billing-account | create | billingbudgets:billingAccounts.budgets.list |
| binaryauthorization | binaryauthorization/platforms/policies | 1 | project | create | binaryauthorization:projects.platforms.policies.list |
| blockchainnodeengine | blockchainnodeengine/blockchainnodes | 0 | project | create | blockchainnodeengine:projects.locations.blockchainNodes.list |
| ces | ces/apps | 0 | project | create | ces:projects.locations.apps.list |
| ces | ces/apps/agents | 1 | project | create | ces:projects.locations.apps.agents.list |
| ces | ces/apps/conversations | 1 | project | delete-only | ces:projects.locations.apps.conversations.list |
| ces | ces/apps/deployments | 1 | project | create | ces:projects.locations.apps.deployments.list |
| ces | ces/apps/examples | 1 | project | create | ces:projects.locations.apps.examples.list |
| ces | ces/apps/guardrails | 1 | project | create | ces:projects.locations.apps.guardrails.list |
| ces | ces/apps/tools | 1 | project | create | ces:projects.locations.apps.tools.list |
| ces | ces/apps/toolsets | 1 | project | create | ces:projects.locations.apps.toolsets.list |
| ces | ces/apps/versions | 1 | project | create | ces:projects.locations.apps.versions.list |
| cloudasset | cloudasset/feeds | 0 | unscoped | create | cloudasset:feeds.list |
| cloudasset | cloudasset/savedqueries | 0 | unscoped | create | cloudasset:savedQueries.list |
| cloudbilling | cloudbilling/billingaccounts | 0 | org | create | cloudbilling:billingAccounts.list, cloudbilling:organizations.billingAccounts.list |
| cloudbilling | cloudbilling/billingaccounts/subaccounts | 1 | billing-account | create | cloudbilling:billingAccounts.subAccounts.list |
| cloudbuild | cloudbuild/bitbucketserverconfigs | 0 | project | create | cloudbuild:projects.locations.bitbucketServerConfigs.list |
| cloudbuild | cloudbuild/builds | 0 | project | create | cloudbuild:projects.builds.list, cloudbuild:projects.locations.builds.list |
| cloudbuild | cloudbuild/gitlabconfigs | 0 | project | create | cloudbuild:projects.locations.gitLabConfigs.list |
| cloudcommerceprocurement | cloudcommerceprocurement/providers/entitlements | 1 | global | mutable | cloudcommerceprocurement:providers.entitlements.list |
| cloudcontrolspartner | cloudcontrolspartner/customers | 0 | org | create | cloudcontrolspartner:organizations.locations.customers.list |
| clouddebugger | clouddebugger/debuggees/breakpoints | 1 | global | delete-only | clouddebugger:controller.debuggees.breakpoints.list, clouddebugger:debugger.debuggees.breakpoints.list |
| clouddeploy | clouddeploy/customtargettypes | 0 | project | create | clouddeploy:projects.locations.customTargetTypes.list |
| clouddeploy | clouddeploy/deliverypipelines | 0 | project | create | clouddeploy:projects.locations.deliveryPipelines.list |
| clouddeploy | clouddeploy/deliverypipelines/automations | 1 | project | create | clouddeploy:projects.locations.deliveryPipelines.automations.list |
| clouddeploy | clouddeploy/deliverypipelines/releases | 1 | project | create | clouddeploy:projects.locations.deliveryPipelines.releases.list |
| clouddeploy | clouddeploy/deliverypipelines/releases/rollouts | 2 | project | create | clouddeploy:projects.locations.deliveryPipelines.releases.rollouts.list |
| clouddeploy | clouddeploy/deploypolicies | 0 | project | create | clouddeploy:projects.locations.deployPolicies.list |
| clouddeploy | clouddeploy/targets | 0 | project | create | clouddeploy:projects.locations.targets.list |
| cloudiot | cloudiot/registries | 0 | project | create | cloudiot:projects.locations.registries.list |
| cloudiot | cloudiot/registries/devices | 1 | project | create | cloudiot:projects.locations.registries.devices.list |
| cloudkms | cloudkms/singletenanthsminstances/proposals | 1 | project | create | cloudkms:projects.locations.singleTenantHsmInstances.proposals.list |
| cloudprofiler | cloudprofiler/profiles | 0 | project | create | cloudprofiler:projects.profiles.list |
| cloudscheduler | cloudscheduler/jobs | 0 | project | create | cloudscheduler:projects.locations.jobs.list |
| cloudsupport | cloudsupport/cases | 0 | unscoped | create | cloudsupport:cases.list, cloudsupport:cases.search |
| cloudsupport | cloudsupport/cases/attachments | 1 | unscoped | create | cloudsupport:cases.attachments.list |
| cloudsupport | cloudsupport/cases/comments | 1 | unscoped | create | cloudsupport:cases.comments.list |
| cloudsupport | cloudsupport/supporteventsubscriptions | 0 | org | create | cloudsupport:organizations.supportEventSubscriptions.list |
| cloudtasks | cloudtasks/queues | 0 | project | create | cloudtasks:projects.locations.queues.list |
| cloudtasks | cloudtasks/queues/tasks | 1 | project | create | cloudtasks:projects.locations.queues.tasks.list |
| composer | composer/environments/userworkloadssecrets | 1 | project | create | composer:projects.locations.environments.userWorkloadsSecrets.list |
| compute | compute/firewallpolicies | 0 | global | create | compute:firewallPolicies.list |
| compute | compute/globalvmextensionpolicies | 0 | project | create | compute:globalVmExtensionPolicies.aggregatedList, compute:globalVmExtensionPolicies.list, compute:globalVmExtensionPolicies.listVmExtensions |
| compute | compute/hosts | 0 | project | mutable | compute:hosts.list |
| compute | compute/licenses | 0 | project | create | compute:licenses.list |
| compute | compute/organizationsecuritypolicies | 0 | global | create | compute:organizationSecurityPolicies.list |
| compute | compute/previewfeatures | 0 | project | mutable | compute:previewFeatures.list |
| compute | compute/reservationslots | 3 | project | mutable | compute:reservationSlots.list |
| compute | compute/rolloutplans | 0 | project | create | compute:rolloutPlans.list |
| compute | compute/rollouts | 0 | project | delete-only | compute:rollouts.list |
| compute | compute/zonevmextensionpolicies | 0 | project | create | compute:globalVmExtensionPolicies.aggregatedList, compute:zoneVmExtensionPolicies.list, compute:zoneVmExtensionPolicies.listVmExtensions |
| config | config/deploymentgroups | 0 | project | create | config:projects.locations.deploymentGroups.list |
| config | config/deployments | 0 | project | create | config:projects.locations.deployments.list |
| config | config/previews | 0 | project | create | config:projects.locations.previews.list |
| connectors | connectors/connections | 0 | project | create | connectors:projects.locations.connections.list, connectors:projects.locations.connections.search |
| connectors | connectors/connections/enduserauthentications | 1 | project | create | connectors:projects.locations.connections.endUserAuthentications.list |
| connectors | connectors/connections/entitytypes/entities | 2 | project | create | connectors:projects.locations.connections.entityTypes.entities.list |
| connectors | connectors/connections/eventsubscriptions | 1 | project | create | connectors:projects.locations.connections.eventSubscriptions.list |
| connectors | connectors/customconnectors | 0 | project | create | connectors:projects.locations.global.customConnectors.list |
| connectors | connectors/customconnectors/customconnectorversions | 1 | project | create | connectors:projects.locations.global.customConnectors.customConnectorVersions.list |
| connectors | connectors/endpointattachments | 0 | project | create | connectors:projects.locations.endpointAttachments.list |
| connectors | connectors/managedzones | 0 | project | create | connectors:projects.locations.global.managedZones.list |
| contactcenterinsights | contactcenterinsights/analysisrules | 0 | project | create | contactcenterinsights:projects.locations.analysisRules.list |
| contactcenterinsights | contactcenterinsights/assessmentrules | 0 | project | create | contactcenterinsights:projects.locations.assessmentRules.list |
| contactcenterinsights | contactcenterinsights/assistantsessions | 0 | project | create | contactcenterinsights:projects.locations.assistantSessions.list |
| contactcenterinsights | contactcenterinsights/authorizedviewsets | 0 | project | create | contactcenterinsights:projects.locations.authorizedViewSets.list |
| contactcenterinsights | contactcenterinsights/authorizedviewsets/authorizedviews | 1 | project | create | contactcenterinsights:projects.locations.authorizedViewSets.authorizedViews.list, contactcenterinsights:projects.locations.authorizedViewSets.authorizedViews.search |
| contactcenterinsights | contactcenterinsights/authorizedviewsets/authorizedviews/conversations | 2 | project | delete-only | contactcenterinsights:projects.locations.authorizedViewSets.authorizedViews.conversations.list |
| contactcenterinsights | contactcenterinsights/authorizedviewsets/authorizedviews/conversations/assessments | 3 | project | create | contactcenterinsights:projects.locations.authorizedViewSets.authorizedViews.conversations.assessments.list |
| contactcenterinsights | contactcenterinsights/authorizedviewsets/authorizedviews/conversations/assessments/notes | 4 | project | create | contactcenterinsights:projects.locations.authorizedViewSets.authorizedViews.conversations.assessments.notes.list |
| contactcenterinsights | contactcenterinsights/authorizedviewsets/authorizedviews/conversations/feedbacklabels | 3 | project | create | contactcenterinsights:projects.locations.authorizedViewSets.authorizedViews.conversations.feedbackLabels.list |
| contactcenterinsights | contactcenterinsights/autolabelingrules | 0 | project | create | contactcenterinsights:projects.locations.autoLabelingRules.list |
| contactcenterinsights | contactcenterinsights/conversations | 0 | project | create | contactcenterinsights:projects.locations.conversations.list |
| contactcenterinsights | contactcenterinsights/conversations/analyses | 1 | project | create | contactcenterinsights:projects.locations.conversations.analyses.list |
| contactcenterinsights | contactcenterinsights/conversations/assessments | 1 | project | create | contactcenterinsights:projects.locations.conversations.assessments.list |
| contactcenterinsights | contactcenterinsights/conversations/assessments/notes | 2 | project | create | contactcenterinsights:projects.locations.conversations.assessments.notes.list |
| contactcenterinsights | contactcenterinsights/conversations/feedbacklabels | 1 | project | create | contactcenterinsights:projects.locations.conversations.feedbackLabels.list |
| contactcenterinsights | contactcenterinsights/dashboards | 0 | project | create | contactcenterinsights:projects.locations.dashboards.list |
| contactcenterinsights | contactcenterinsights/dashboards/charts | 1 | project | create | contactcenterinsights:projects.locations.dashboards.charts.list |
| contactcenterinsights | contactcenterinsights/datasets | 0 | project | create | contactcenterinsights:projects.locations.datasets.list |
| contactcenterinsights | contactcenterinsights/datasets/conversations | 1 | project | delete-only | contactcenterinsights:projects.locations.datasets.conversations.list |
| contactcenterinsights | contactcenterinsights/datasets/conversations/feedbacklabels | 2 | project | create | contactcenterinsights:projects.locations.datasets.conversations.feedbackLabels.list |
| contactcenterinsights | contactcenterinsights/issuemodels | 0 | project | create | contactcenterinsights:projects.locations.issueModels.list |
| contactcenterinsights | contactcenterinsights/issuemodels/issues | 1 | project | create | contactcenterinsights:projects.locations.issueModels.issues.list |
| contactcenterinsights | contactcenterinsights/phrasematchers | 0 | project | create | contactcenterinsights:projects.locations.phraseMatchers.list |
| contactcenterinsights | contactcenterinsights/qaquestiontags | 0 | project | create | contactcenterinsights:projects.locations.qaQuestionTags.list |
| contactcenterinsights | contactcenterinsights/qascorecards | 0 | project | create | contactcenterinsights:projects.locations.qaScorecards.list |
| contactcenterinsights | contactcenterinsights/qascorecards/revisions | 1 | project | create | contactcenterinsights:projects.locations.qaScorecards.revisions.list |
| contactcenterinsights | contactcenterinsights/qascorecards/revisions/qaquestions | 2 | project | create | contactcenterinsights:projects.locations.qaScorecards.revisions.qaQuestions.list |
| contactcenterinsights | contactcenterinsights/views | 0 | project | create | contactcenterinsights:projects.locations.views.list |
| containeranalysis | containeranalysis/notes | 0 | project | create | containeranalysis:projects.locations.notes.list, containeranalysis:projects.notes.list, containeranalysis:providers.notes.list |
| containeranalysis | containeranalysis/occurrences | 0 | project | create | containeranalysis:projects.locations.occurrences.list, containeranalysis:projects.occurrences.list |
| contentwarehouse | contentwarehouse/documentschemas | 0 | project | create | contentwarehouse:projects.locations.documentSchemas.list |
| contentwarehouse | contentwarehouse/rulesets | 0 | project | create | contentwarehouse:projects.locations.ruleSets.list |
| contentwarehouse | contentwarehouse/synonymsets | 0 | project | create | contentwarehouse:projects.locations.synonymSets.list |
| datacatalog | datacatalog/entrygroups | 0 | project | create | datacatalog:projects.locations.entryGroups.list |
| datacatalog | datacatalog/entrygroups/entries | 1 | project | create | datacatalog:projects.locations.entryGroups.entries.list |
| datacatalog | datacatalog/entrygroups/entries/tags | 2 | project | create | datacatalog:projects.locations.entryGroups.entries.tags.list |
| datacatalog | datacatalog/entrygroups/tags | 1 | project | create | datacatalog:projects.locations.entryGroups.tags.list |
| datacatalog | datacatalog/taxonomies | 0 | project | create | datacatalog:projects.locations.taxonomies.list |
| datacatalog | datacatalog/taxonomies/policytags | 1 | project | create | datacatalog:projects.locations.taxonomies.policyTags.list |
| dataform | dataform/repositories | 0 | project | create | dataform:projects.locations.repositories.list |
| dataform | dataform/repositories/compilationresults | 1 | project | create | dataform:projects.locations.repositories.compilationResults.list |
| dataform | dataform/repositories/releaseconfigs | 1 | project | create | dataform:projects.locations.repositories.releaseConfigs.list |
| dataform | dataform/repositories/workflowconfigs | 1 | project | create | dataform:projects.locations.repositories.workflowConfigs.list |
| dataform | dataform/repositories/workflowinvocations | 1 | project | create | dataform:projects.locations.repositories.workflowInvocations.list |
| dataform | dataform/repositories/workspaces | 1 | project | create | dataform:projects.locations.repositories.workspaces.list |
| dataform | dataform/teamfolders | 0 | project | create | dataform:projects.locations.teamFolders.search |
| datafusion | datafusion/instances | 0 | project | create | datafusion:projects.locations.instances.list |
| datafusion | datafusion/instances/dnspeerings | 1 | project | create | datafusion:projects.locations.instances.dnsPeerings.list |
| datalineage | datalineage/processes | 0 | project | create | datalineage:projects.locations.processes.list |
| datalineage | datalineage/processes/runs | 1 | project | create | datalineage:projects.locations.processes.runs.list |
| datalineage | datalineage/processes/runs/lineageevents | 2 | project | create | datalineage:projects.locations.processes.runs.lineageEvents.list |
| datamigration | datamigration/connectionprofiles | 0 | project | create | datamigration:projects.locations.connectionProfiles.list |
| datamigration | datamigration/conversionworkspaces | 0 | project | create | datamigration:projects.locations.conversionWorkspaces.list |
| datamigration | datamigration/conversionworkspaces/mappingrules | 1 | project | create | datamigration:projects.locations.conversionWorkspaces.mappingRules.list |
| datamigration | datamigration/migrationjobs | 0 | project | create | datamigration:projects.locations.migrationJobs.list |
| datamigration | datamigration/migrationjobs/objects | 1 | project | create | datamigration:projects.locations.migrationJobs.objects.list |
| datamigration | datamigration/privateconnections | 0 | project | create | datamigration:projects.locations.privateConnections.list |
| datapipelines | datapipelines/pipelines | 0 | project | create | datapipelines:projects.locations.pipelines.list |
| dataplex | dataplex/aspecttypes | 0 | project | create | dataplex:projects.locations.aspectTypes.list |
| dataplex | dataplex/changerequests | 0 | project | delete-only | dataplex:projects.locations.changeRequests.list |
| dataplex | dataplex/dataattributebindings | 0 | project | create | dataplex:projects.locations.dataAttributeBindings.list |
| dataplex | dataplex/datadomains | 0 | project | create | dataplex:projects.locations.dataDomains.list |
| dataplex | dataplex/datadomains/bindings | 1 | project | create | dataplex:projects.locations.dataDomains.bindings.list |
| dataplex | dataplex/dataproducts | 0 | project | create | dataplex:projects.locations.dataProducts.list |
| dataplex | dataplex/dataproducts/dataassets | 1 | project | create | dataplex:projects.locations.dataProducts.dataAssets.list |
| dataplex | dataplex/datascans | 0 | project | create | dataplex:projects.locations.dataScans.list |
| dataplex | dataplex/datataxonomies | 0 | project | create | dataplex:projects.locations.dataTaxonomies.list |
| dataplex | dataplex/datataxonomies/attributes | 1 | project | create | dataplex:projects.locations.dataTaxonomies.attributes.list |
| dataplex | dataplex/encryptionconfigs | 0 | org | create | dataplex:organizations.locations.encryptionConfigs.list |
| dataplex | dataplex/entrygroups | 0 | project | create | dataplex:projects.locations.entryGroups.list |
| dataplex | dataplex/entrygroups/entries | 1 | project | create | dataplex:projects.locations.entryGroups.entries.list |
| dataplex | dataplex/entrytypes | 0 | project | create | dataplex:projects.locations.entryTypes.list |
| dataplex | dataplex/glossaries | 0 | project | create | dataplex:projects.locations.glossaries.list |
| dataplex | dataplex/glossaries/categories | 1 | project | create | dataplex:projects.locations.glossaries.categories.list |
| dataplex | dataplex/glossaries/terms | 1 | project | create | dataplex:projects.locations.glossaries.terms.list |
| dataplex | dataplex/lakes | 0 | project | create | dataplex:projects.locations.lakes.list |
| dataplex | dataplex/lakes/tasks | 1 | project | create | dataplex:projects.locations.lakes.tasks.list |
| dataplex | dataplex/lakes/zones | 1 | project | create | dataplex:projects.locations.lakes.zones.list |
| dataplex | dataplex/lakes/zones/assets | 2 | project | create | dataplex:projects.locations.lakes.zones.assets.list |
| dataplex | dataplex/lakes/zones/entities | 2 | project | create | dataplex:projects.locations.lakes.zones.entities.list |
| dataplex | dataplex/lakes/zones/entities/partitions | 3 | project | create | dataplex:projects.locations.lakes.zones.entities.partitions.list |
| dataplex | dataplex/metadatafeeds | 0 | project | create | dataplex:projects.locations.metadataFeeds.list |
| dataplex | dataplex/metadatajobs | 0 | project | create | dataplex:projects.locations.metadataJobs.list |
| datastore | datastore/indexes | 0 | project | create | datastore:projects.indexes.list |
| datastream | datastream/connectionprofiles | 0 | project | create | datastream:projects.locations.connectionProfiles.list |
| datastream | datastream/privateconnections | 0 | project | create | datastream:projects.locations.privateConnections.list |
| datastream | datastream/privateconnections/routes | 1 | project | create | datastream:projects.locations.privateConnections.routes.list |
| datastream | datastream/streams | 0 | project | create | datastream:projects.locations.streams.list |
| datastream | datastream/streams/objects | 1 | project | create | datastream:projects.locations.streams.objects.list |
| deploymentmanager | deploymentmanager/deployments | 0 | project | create | deploymentmanager:deployments.list |
| developerconnect | developerconnect/accountconnectors | 0 | project | create | developerconnect:projects.locations.accountConnectors.list |
| developerconnect | developerconnect/accountconnectors/users | 1 | project | delete-only | developerconnect:projects.locations.accountConnectors.users.list |
| developerconnect | developerconnect/connections | 0 | project | create | developerconnect:projects.locations.connections.list |
| developerconnect | developerconnect/connections/gitrepositorylinks | 1 | project | create | developerconnect:projects.locations.connections.gitRepositoryLinks.list |
| developerconnect | developerconnect/insightsconfigs | 0 | project | create | developerconnect:projects.locations.insightsConfigs.list |
| dialogflow | dialogflow/agent | 0 | project | create | dialogflow:projects.agent.search, dialogflow:projects.locations.agent.search |
| dialogflow | dialogflow/agent/entitytypes | 0 | project | create | dialogflow:projects.agent.entityTypes.list, dialogflow:projects.locations.agent.entityTypes.list |
| dialogflow | dialogflow/agent/environments | 0 | project | create | dialogflow:projects.agent.environments.list, dialogflow:projects.locations.agent.environments.list |
| dialogflow | dialogflow/agent/environments/users/sessions/contexts | 3 | project | create | dialogflow:projects.agent.environments.users.sessions.contexts.list, dialogflow:projects.agent.sessions.contexts.list, dialogflow:projects.locations.agent.environments.users.sessions.contexts.list, dialogflow:projects.locations.agent.sessions.contexts.list |
| dialogflow | dialogflow/agent/environments/users/sessions/entitytypes | 3 | project | create | dialogflow:projects.agent.environments.users.sessions.entityTypes.list, dialogflow:projects.locations.agent.environments.users.sessions.entityTypes.list |
| dialogflow | dialogflow/agent/intents | 0 | project | create | dialogflow:projects.agent.intents.list, dialogflow:projects.locations.agent.intents.list |
| dialogflow | dialogflow/agent/knowledgebases | 0 | project | create | dialogflow:projects.agent.knowledgeBases.list |
| dialogflow | dialogflow/agent/knowledgebases/documents | 1 | project | create | dialogflow:projects.agent.knowledgeBases.documents.list |
| dialogflow | dialogflow/agent/sessions/entitytypes | 1 | project | create | dialogflow:projects.agent.sessions.entityTypes.list, dialogflow:projects.locations.agent.sessions.entityTypes.list |
| dialogflow | dialogflow/agent/versions | 0 | project | create | dialogflow:projects.agent.versions.list, dialogflow:projects.locations.agent.versions.list |
| dialogflow | dialogflow/agents | 0 | project | create | dialogflow:projects.locations.agents.list |
| dialogflow | dialogflow/agents/entitytypes | 1 | project | create | dialogflow:projects.locations.agents.entityTypes.list |
| dialogflow | dialogflow/agents/environments | 1 | project | create | dialogflow:projects.locations.agents.environments.list |
| dialogflow | dialogflow/agents/environments/experiments | 2 | project | create | dialogflow:projects.locations.agents.environments.experiments.list |
| dialogflow | dialogflow/agents/environments/sessions/entitytypes | 3 | project | create | dialogflow:projects.locations.agents.environments.sessions.entityTypes.list |
| dialogflow | dialogflow/agents/flows | 1 | project | create | dialogflow:projects.locations.agents.flows.list |
| dialogflow | dialogflow/agents/flows/pages | 2 | project | create | dialogflow:projects.locations.agents.flows.pages.list |
| dialogflow | dialogflow/agents/flows/transitionroutegroups | 2 | project | create | dialogflow:projects.locations.agents.flows.transitionRouteGroups.list |
| dialogflow | dialogflow/agents/flows/versions | 2 | project | create | dialogflow:projects.locations.agents.flows.versions.list |
| dialogflow | dialogflow/agents/generators | 1 | project | create | dialogflow:projects.locations.agents.generators.list |
| dialogflow | dialogflow/agents/intents | 1 | project | create | dialogflow:projects.locations.agents.intents.list |
| dialogflow | dialogflow/agents/playbooks | 1 | project | create | dialogflow:projects.locations.agents.playbooks.list |
| dialogflow | dialogflow/agents/playbooks/examples | 2 | project | create | dialogflow:projects.locations.agents.playbooks.examples.list |
| dialogflow | dialogflow/agents/playbooks/versions | 2 | project | create | dialogflow:projects.locations.agents.playbooks.versions.list |
| dialogflow | dialogflow/agents/sessions/entitytypes | 2 | project | create | dialogflow:projects.locations.agents.sessions.entityTypes.list |
| dialogflow | dialogflow/agents/testcases | 1 | project | create | dialogflow:projects.locations.agents.testCases.list |
| dialogflow | dialogflow/agents/tools | 1 | project | create | dialogflow:projects.locations.agents.tools.list |
| dialogflow | dialogflow/agents/tools/versions | 2 | project | create | dialogflow:projects.locations.agents.tools.versions.list |
| dialogflow | dialogflow/agents/transitionroutegroups | 1 | project | create | dialogflow:projects.locations.agents.transitionRouteGroups.list |
| dialogflow | dialogflow/agents/webhooks | 1 | project | create | dialogflow:projects.locations.agents.webhooks.list |
| dialogflow | dialogflow/answerrecords | 0 | project | mutable | dialogflow:projects.answerRecords.list, dialogflow:projects.locations.answerRecords.list |
| dialogflow | dialogflow/conversationdatasets | 0 | project | create | dialogflow:projects.conversationDatasets.list, dialogflow:projects.locations.conversationDatasets.list |
| dialogflow | dialogflow/conversationmodels | 0 | project | create | dialogflow:projects.conversationModels.list, dialogflow:projects.locations.conversationModels.list |
| dialogflow | dialogflow/conversationmodels/evaluations | 1 | project | create | dialogflow:projects.conversationModels.evaluations.list, dialogflow:projects.locations.conversationModels.evaluations.list |
| dialogflow | dialogflow/conversationprofiles | 0 | project | create | dialogflow:projects.conversationProfiles.list, dialogflow:projects.locations.conversationProfiles.list |
| dialogflow | dialogflow/conversations | 0 | project | create | dialogflow:projects.conversations.list, dialogflow:projects.locations.conversations.list |
| dialogflow | dialogflow/conversations/participants | 1 | project | create | dialogflow:projects.conversations.participants.list, dialogflow:projects.locations.conversations.participants.list |
| dialogflow | dialogflow/generators | 0 | project | create | dialogflow:projects.generators.list, dialogflow:projects.locations.generators.list |
| dialogflow | dialogflow/generators/evaluations | 1 | project | create | dialogflow:projects.locations.generators.evaluations.list |
| dialogflow | dialogflow/knowledgebases | 0 | project | create | dialogflow:projects.knowledgeBases.list, dialogflow:projects.locations.knowledgeBases.list |
| dialogflow | dialogflow/knowledgebases/documents | 1 | project | create | dialogflow:projects.knowledgeBases.documents.list, dialogflow:projects.locations.knowledgeBases.documents.list |
| dialogflow | dialogflow/securitysettings | 0 | project | create | dialogflow:projects.locations.securitySettings.list |
| dialogflow | dialogflow/siptrunks | 0 | project | create | dialogflow:projects.locations.sipTrunks.list |
| dialogflow | dialogflow/tools | 0 | project | create | dialogflow:projects.locations.tools.list |
| discoveryengine | discoveryengine/cmekconfigs | 0 | project | delete-only | discoveryengine:projects.locations.cmekConfigs.list |
| discoveryengine | discoveryengine/collections/datastores/branches/documents | 3 | project | create | discoveryengine:projects.locations.collections.dataStores.branches.documents.list |
| discoveryengine | discoveryengine/collections/datastores/controls | 2 | project | create | discoveryengine:projects.locations.collections.dataStores.controls.list |
| discoveryengine | discoveryengine/collections/datastores/conversations | 2 | project | create | discoveryengine:projects.locations.collections.dataStores.conversations.list |
| discoveryengine | discoveryengine/collections/datastores/schemas | 2 | project | create | discoveryengine:projects.locations.collections.dataStores.schemas.list |
| discoveryengine | discoveryengine/collections/datastores/servingconfigs | 2 | project | create | discoveryengine:projects.locations.collections.dataStores.servingConfigs.list |
| discoveryengine | discoveryengine/collections/datastores/sessions | 2 | project | create | discoveryengine:projects.locations.collections.dataStores.sessions.list |
| discoveryengine | discoveryengine/collections/datastores/sitesearchengine/sitemaps | 2 | project | create | discoveryengine:projects.locations.collections.dataStores.siteSearchEngine.sitemaps.fetch |
| discoveryengine | discoveryengine/collections/datastores/sitesearchengine/targetsites | 2 | project | create | discoveryengine:projects.locations.collections.dataStores.siteSearchEngine.targetSites.list |
| discoveryengine | discoveryengine/collections/engines | 1 | project | create | discoveryengine:projects.locations.collections.engines.list |
| discoveryengine | discoveryengine/collections/engines/assistants | 2 | project | create | discoveryengine:projects.locations.collections.engines.assistants.list |
| discoveryengine | discoveryengine/collections/engines/assistants/agents/a2a/v1/tasks/pushnotificationconfigs | 5 | project | create | discoveryengine:projects.locations.collections.engines.assistants.agents.a2a.v1.tasks.pushNotificationConfigs.list |
| discoveryengine | discoveryengine/collections/engines/controls | 2 | project | create | discoveryengine:projects.locations.collections.engines.controls.list |
| discoveryengine | discoveryengine/collections/engines/conversations | 2 | project | create | discoveryengine:projects.locations.collections.engines.conversations.list |
| discoveryengine | discoveryengine/collections/engines/servingconfigs | 2 | project | create | discoveryengine:projects.locations.collections.engines.servingConfigs.list |
| discoveryengine | discoveryengine/collections/engines/sessions | 2 | project | create | discoveryengine:projects.locations.collections.engines.sessions.list |
| discoveryengine | discoveryengine/datastores | 0 | project | create | discoveryengine:projects.locations.collections.dataStores.list, discoveryengine:projects.locations.dataStores.list |
| discoveryengine | discoveryengine/datastores/branches/documents | 2 | project | create | discoveryengine:projects.locations.dataStores.branches.documents.list |
| discoveryengine | discoveryengine/datastores/controls | 1 | project | create | discoveryengine:projects.locations.dataStores.controls.list |
| discoveryengine | discoveryengine/datastores/conversations | 1 | project | create | discoveryengine:projects.locations.dataStores.conversations.list |
| discoveryengine | discoveryengine/datastores/schemas | 1 | project | create | discoveryengine:projects.locations.dataStores.schemas.list |
| discoveryengine | discoveryengine/datastores/servingconfigs | 1 | project | create | discoveryengine:projects.locations.dataStores.servingConfigs.list |
| discoveryengine | discoveryengine/datastores/sessions | 1 | project | create | discoveryengine:projects.locations.dataStores.sessions.list |
| discoveryengine | discoveryengine/datastores/sitesearchengine/sitemaps | 1 | project | create | discoveryengine:projects.locations.dataStores.siteSearchEngine.sitemaps.fetch |
| discoveryengine | discoveryengine/datastores/sitesearchengine/targetsites | 1 | project | create | discoveryengine:projects.locations.dataStores.siteSearchEngine.targetSites.list |
| discoveryengine | discoveryengine/identitymappingstores | 0 | project | create | discoveryengine:projects.locations.identityMappingStores.list |
| dlp | dlp/connections | 0 | project | create | dlp:organizations.locations.connections.list, dlp:organizations.locations.connections.search, dlp:projects.locations.connections.list, dlp:projects.locations.connections.search |
| dlp | dlp/contentpolicies | 0 | project | create | dlp:projects.locations.contentPolicies.list |
| dlp | dlp/deidentifytemplates | 0 | project | create | dlp:organizations.deidentifyTemplates.list, dlp:organizations.locations.deidentifyTemplates.list, dlp:projects.deidentifyTemplates.list, dlp:projects.locations.deidentifyTemplates.list |
| dlp | dlp/discoveryconfigs | 0 | project | create | dlp:organizations.locations.discoveryConfigs.list, dlp:projects.locations.discoveryConfigs.list |
| dlp | dlp/dlpjobs | 0 | project | create | dlp:projects.dlpJobs.list, dlp:projects.locations.dlpJobs.list |
| dlp | dlp/filestoredataprofiles | 0 | project | delete-only | dlp:organizations.locations.fileStoreDataProfiles.list, dlp:projects.locations.fileStoreDataProfiles.list |
| dlp | dlp/inspecttemplates | 0 | project | create | dlp:organizations.inspectTemplates.list, dlp:organizations.locations.inspectTemplates.list, dlp:projects.inspectTemplates.list, dlp:projects.locations.inspectTemplates.list |
| dlp | dlp/jobtriggers | 0 | project | create | dlp:organizations.locations.jobTriggers.list, dlp:projects.jobTriggers.list, dlp:projects.locations.jobTriggers.list |
| dlp | dlp/storedinfotypes | 0 | project | create | dlp:organizations.locations.storedInfoTypes.list, dlp:organizations.storedInfoTypes.list, dlp:projects.locations.storedInfoTypes.list, dlp:projects.storedInfoTypes.list |
| dlp | dlp/tabledataprofiles | 0 | project | delete-only | dlp:organizations.locations.tableDataProfiles.list, dlp:projects.locations.tableDataProfiles.list |
| dns | dns/changes | 1 | project | create | dns:changes.list |
| documentai | documentai/processors | 0 | project | create | documentai:projects.locations.processors.list |
| documentai | documentai/processors/processorversions | 1 | project | delete-only | documentai:projects.locations.processors.processorVersions.list |
| documentai | documentai/schemas | 0 | project | create | documentai:projects.locations.schemas.list |
| documentai | documentai/schemas/schemaversions | 1 | project | create | documentai:projects.locations.schemas.schemaVersions.list |
| domains | domains/registrations | 0 | project | delete-only | domains:projects.locations.registrations.list |
| essentialcontacts | essentialcontacts/contacts | 0 | project | create | essentialcontacts:folders.contacts.compute, essentialcontacts:folders.contacts.list, essentialcontacts:organizations.contacts.compute, essentialcontacts:organizations.contacts.list, essentialcontacts:projects.contacts.compute, essentialcontacts:projects.contacts.list |
| eventarc | eventarc/channelconnections | 0 | project | create | eventarc:projects.locations.channelConnections.list |
| eventarc | eventarc/channels | 0 | project | create | eventarc:projects.locations.channels.list |
| eventarc | eventarc/enrollments | 0 | project | create | eventarc:projects.locations.enrollments.list |
| eventarc | eventarc/googleapisources | 0 | project | create | eventarc:projects.locations.googleApiSources.list |
| eventarc | eventarc/messagebuses | 0 | project | create | eventarc:projects.locations.messageBuses.list |
| eventarc | eventarc/pipelines | 0 | project | create | eventarc:projects.locations.pipelines.list |
| eventarc | eventarc/triggers | 0 | project | create | eventarc:projects.locations.triggers.list |
| file | file/backups | 0 | project | create | file:projects.locations.backups.list |
| file | file/instances | 0 | project | create | file:projects.locations.instances.list |
| file | file/instances/snapshots | 1 | project | create | file:projects.locations.instances.snapshots.list |
| firebaseappcheck | firebaseappcheck/apps/debugtokens | 1 | project | create | firebaseappcheck:projects.apps.debugTokens.list |
| firebaseappcheck | firebaseappcheck/services | 0 | project | mutable | firebaseappcheck:projects.services.list |
| firebaseappcheck | firebaseappcheck/services/resourcepolicies | 1 | project | create | firebaseappcheck:projects.services.resourcePolicies.list |
| firebaseappdistribution | firebaseappdistribution/apps/releases | 1 | project | mutable | firebaseappdistribution:projects.apps.releases.list |
| firebaseappdistribution | firebaseappdistribution/apps/releases/feedbackreports | 2 | project | delete-only | firebaseappdistribution:projects.apps.releases.feedbackReports.list |
| firebaseappdistribution | firebaseappdistribution/groups | 0 | project | create | firebaseappdistribution:projects.groups.list |
| firebaseappdistribution | firebaseappdistribution/testers | 0 | project | mutable | firebaseappdistribution:projects.testers.list |
| firebaseapphosting | firebaseapphosting/backends | 0 | project | create | firebaseapphosting:projects.locations.backends.list |
| firebaseapphosting | firebaseapphosting/backends/builds | 1 | project | create | firebaseapphosting:projects.locations.backends.builds.list |
| firebaseapphosting | firebaseapphosting/backends/domains | 1 | project | create | firebaseapphosting:projects.locations.backends.domains.list |
| firebaseapphosting | firebaseapphosting/backends/rollouts | 1 | project | create | firebaseapphosting:projects.locations.backends.rollouts.list |
| firebasedataconnect | firebasedataconnect/services | 0 | project | create | firebasedataconnect:projects.locations.services.list |
| firebasedataconnect | firebasedataconnect/services/connectors | 1 | project | create | firebasedataconnect:projects.locations.services.connectors.list |
| firebasedataconnect | firebasedataconnect/services/schemas | 1 | project | create | firebasedataconnect:projects.locations.services.schemas.list |
| firebaserules | firebaserules/releases | 0 | project | create | firebaserules:projects.releases.list |
| firebaserules | firebaserules/rulesets | 0 | project | create | firebaserules:projects.rulesets.list |
| firestore | firestore/databases/collectiongroups/fields | 2 | project | mutable | firestore:projects.databases.collectionGroups.fields.list |
| firestore | firestore/databases/collectiongroups/indexes | 2 | project | create | firestore:projects.databases.collectionGroups.indexes.list |
| gkebackup | gkebackup/backupchannels | 0 | project | create | gkebackup:projects.locations.backupChannels.list |
| gkebackup | gkebackup/backupplans | 0 | project | create | gkebackup:projects.locations.backupPlans.list |
| gkebackup | gkebackup/backupplans/backups | 1 | project | create | gkebackup:projects.locations.backupPlans.backups.list |
| gkebackup | gkebackup/restorechannels | 0 | project | create | gkebackup:projects.locations.restoreChannels.list |
| gkebackup | gkebackup/restoreplans | 0 | project | create | gkebackup:projects.locations.restorePlans.list |
| gkebackup | gkebackup/restoreplans/restores | 1 | project | create | gkebackup:projects.locations.restorePlans.restores.list |
| gkehub | gkehub/features | 0 | project | create | gkehub:projects.locations.features.list |
| gkehub | gkehub/fleets | 0 | project | create | gkehub:projects.locations.fleets.list |
| gkehub | gkehub/memberships | 0 | project | create | gkehub:projects.locations.memberships.list, gkehub:projects.locations.memberships.listAdmin |
| gkehub | gkehub/memberships/bindings | 1 | project | create | gkehub:projects.locations.memberships.bindings.list |
| gkehub | gkehub/memberships/features | 1 | project | create | gkehub:projects.locations.memberships.features.list |
| gkehub | gkehub/memberships/rbacrolebindings | 1 | project | create | gkehub:projects.locations.memberships.rbacrolebindings.list |
| gkehub | gkehub/rollouts | 0 | project | delete-only | gkehub:projects.locations.rollouts.list |
| gkehub | gkehub/rolloutsequences | 0 | project | create | gkehub:projects.locations.rolloutSequences.list |
| gkehub | gkehub/scopes | 0 | project | create | gkehub:projects.locations.scopes.list, gkehub:projects.locations.scopes.listPermitted |
| gkehub | gkehub/scopes/namespaces | 1 | project | create | gkehub:projects.locations.scopes.namespaces.list |
| gkehub | gkehub/scopes/rbacrolebindings | 1 | project | create | gkehub:projects.locations.scopes.rbacrolebindings.list |
| gkeonprem | gkeonprem/baremetaladminclusters | 0 | project | create | gkeonprem:projects.locations.bareMetalAdminClusters.list |
| gkeonprem | gkeonprem/baremetalclusters | 0 | project | create | gkeonprem:projects.locations.bareMetalClusters.list |
| gkeonprem | gkeonprem/baremetalclusters/baremetalnodepools | 1 | project | create | gkeonprem:projects.locations.bareMetalClusters.bareMetalNodePools.list |
| gkeonprem | gkeonprem/vmwareadminclusters | 0 | project | create | gkeonprem:projects.locations.vmwareAdminClusters.list |
| gkeonprem | gkeonprem/vmwareclusters | 0 | project | create | gkeonprem:projects.locations.vmwareClusters.list |
| gkeonprem | gkeonprem/vmwareclusters/vmwarenodepools | 1 | project | create | gkeonprem:projects.locations.vmwareClusters.vmwareNodePools.list |
| health | health/subscribers | 0 | project | create | health:projects.subscribers.list |
| health | health/subscribers/subscriptions | 1 | project | create | health:projects.subscribers.subscriptions.list |
| health | health/users/datatypes/datapoints | 2 | global | create | health:users.dataTypes.dataPoints.list |
| healthcare | healthcare/datasets | 0 | project | create | healthcare:projects.locations.datasets.list |
| healthcare | healthcare/datasets/consentstores | 1 | project | create | healthcare:projects.locations.datasets.consentStores.list |
| healthcare | healthcare/datasets/consentstores/attributedefinitions | 2 | project | create | healthcare:projects.locations.datasets.consentStores.attributeDefinitions.list |
| healthcare | healthcare/datasets/consentstores/consentartifacts | 2 | project | create | healthcare:projects.locations.datasets.consentStores.consentArtifacts.list |
| healthcare | healthcare/datasets/consentstores/consents | 2 | project | create | healthcare:projects.locations.datasets.consentStores.consents.list |
| healthcare | healthcare/datasets/consentstores/userdatamappings | 2 | project | create | healthcare:projects.locations.datasets.consentStores.userDataMappings.list |
| healthcare | healthcare/datasets/dicomstores | 1 | project | create | healthcare:projects.locations.datasets.dicomStores.list |
| healthcare | healthcare/datasets/fhirstores | 1 | project | create | healthcare:projects.locations.datasets.fhirStores.list |
| healthcare | healthcare/datasets/hl7v2stores | 1 | project | create | healthcare:projects.locations.datasets.hl7V2Stores.list |
| healthcare | healthcare/datasets/hl7v2stores/messages | 2 | project | create | healthcare:projects.locations.datasets.hl7V2Stores.messages.batchGet, healthcare:projects.locations.datasets.hl7V2Stores.messages.list |
| hypercomputecluster | hypercomputecluster/clusters | 0 | project | create | hypercomputecluster:projects.locations.clusters.list |
| iam | iam/workforcepools/providers/keys | 2 | global | create | iam:locations.workforcePools.providers.keys.list |
| iam | iam/workforcepools/providers/scimtenants/tokens | 3 | global | create | iam:locations.workforcePools.providers.scimTenants.tokens.list |
| iam | iam/workloadidentitypools/providers/keys | 2 | project | create | iam:projects.locations.workloadIdentityPools.providers.keys.list |
| iap | iap/brands | 0 | project | create | iap:projects.brands.list |
| iap | iap/brands/identityawareproxyclients | 1 | project | create | iap:projects.brands.identityAwareProxyClients.list |
| iap | iap/iap_tunnel/destgroups | 0 | project | create | iap:projects.iap_tunnel.locations.destGroups.list |
| identitytoolkit | identitytoolkit/defaultsupportedidpconfigs | 0 | project | create | identitytoolkit:projects.defaultSupportedIdpConfigs.list |
| identitytoolkit | identitytoolkit/inboundsamlconfigs | 0 | project | create | identitytoolkit:projects.inboundSamlConfigs.list |
| identitytoolkit | identitytoolkit/oauthidpconfigs | 0 | project | create | identitytoolkit:projects.oauthIdpConfigs.list |
| identitytoolkit | identitytoolkit/tenants | 0 | project | create | identitytoolkit:projects.tenants.list |
| identitytoolkit | identitytoolkit/tenants/defaultsupportedidpconfigs | 1 | project | create | identitytoolkit:projects.tenants.defaultSupportedIdpConfigs.list |
| identitytoolkit | identitytoolkit/tenants/inboundsamlconfigs | 1 | project | create | identitytoolkit:projects.tenants.inboundSamlConfigs.list |
| identitytoolkit | identitytoolkit/tenants/oauthidpconfigs | 1 | project | create | identitytoolkit:projects.tenants.oauthIdpConfigs.list |
| ids | ids/endpoints | 0 | project | create | ids:projects.locations.endpoints.list |
| integrations | integrations/authconfigs | 0 | project | create | integrations:projects.locations.authConfigs.list, integrations:projects.locations.products.authConfigs.list |
| integrations | integrations/certificates | 0 | project | create | integrations:projects.locations.certificates.list, integrations:projects.locations.products.certificates.list |
| integrations | integrations/integrations | 0 | project | delete-only | integrations:projects.locations.integrations.list, integrations:projects.locations.products.integrations.list |
| integrations | integrations/integrations/versions | 1 | project | create | integrations:projects.locations.integrations.versions.list |
| integrations | integrations/products/integrations/versions | 2 | project | create | integrations:projects.locations.products.integrations.versions.list |
| integrations | integrations/products/sfdcinstances/sfdcchannels | 2 | project | create | integrations:projects.locations.products.sfdcInstances.sfdcChannels.list |
| integrations | integrations/sfdcinstances | 0 | project | create | integrations:projects.locations.products.sfdcInstances.list, integrations:projects.locations.sfdcInstances.list |
| integrations | integrations/sfdcinstances/sfdcchannels | 1 | project | create | integrations:projects.locations.sfdcInstances.sfdcChannels.list |
| jobs | jobs/companies | 0 | project | create | jobs:companies.list, jobs:projects.companies.list |
| jobs | jobs/jobs | 0 | project | create | jobs:jobs.list, jobs:projects.jobs.list |
| jobs | jobs/tenants | 0 | project | create | jobs:projects.tenants.list |
| jobs | jobs/tenants/companies | 1 | project | create | jobs:projects.tenants.companies.list |
| jobs | jobs/tenants/jobs | 1 | project | create | jobs:projects.tenants.jobs.list |
| looker | looker/instances | 0 | project | create | looker:projects.locations.instances.list |
| looker | looker/instances/backups | 1 | project | create | looker:projects.locations.instances.backups.list |
| managedidentities | managedidentities/domains | 0 | project | create | managedidentities:projects.locations.global.domains.list |
| managedidentities | managedidentities/domains/backups | 1 | project | create | managedidentities:projects.locations.global.domains.backups.list |
| managedidentities | managedidentities/peerings | 0 | project | create | managedidentities:projects.locations.global.peerings.list |
| managedkafka | managedkafka/clusters | 0 | project | create | managedkafka:projects.locations.clusters.list |
| managedkafka | managedkafka/clusters/acls | 1 | project | create | managedkafka:projects.locations.clusters.acls.list |
| managedkafka | managedkafka/clusters/consumergroups | 1 | project | delete-only | managedkafka:projects.locations.clusters.consumerGroups.list |
| managedkafka | managedkafka/clusters/topics | 1 | project | create | managedkafka:projects.locations.clusters.topics.list |
| managedkafka | managedkafka/connectclusters | 0 | project | create | managedkafka:projects.locations.connectClusters.list |
| managedkafka | managedkafka/connectclusters/connectors | 1 | project | create | managedkafka:projects.locations.connectClusters.connectors.list |
| managedkafka | managedkafka/schemaregistries | 0 | project | create | managedkafka:projects.locations.schemaRegistries.list |
| memcache | memcache/instances | 0 | project | create | memcache:projects.locations.instances.list |
| metastore | metastore/federations | 0 | project | create | metastore:projects.locations.federations.list |
| metastore | metastore/services | 0 | project | create | metastore:projects.locations.services.list |
| metastore | metastore/services/backups | 1 | project | create | metastore:projects.locations.services.backups.list |
| metastore | metastore/services/metadataimports | 1 | project | create | metastore:projects.locations.services.metadataImports.list |
| metastore | metastore/services/migrationexecutions | 1 | project | delete-only | metastore:projects.locations.services.migrationExecutions.list |
| migrationcenter | migrationcenter/assets | 0 | project | delete-only | migrationcenter:projects.locations.assets.list |
| migrationcenter | migrationcenter/assetsexportjobs | 0 | project | create | migrationcenter:projects.locations.assetsExportJobs.list |
| migrationcenter | migrationcenter/discoveryclients | 0 | project | create | migrationcenter:projects.locations.discoveryClients.list |
| migrationcenter | migrationcenter/groups | 0 | project | create | migrationcenter:projects.locations.groups.list |
| migrationcenter | migrationcenter/importjobs | 0 | project | create | migrationcenter:projects.locations.importJobs.list |
| migrationcenter | migrationcenter/importjobs/importdatafiles | 1 | project | create | migrationcenter:projects.locations.importJobs.importDataFiles.list |
| migrationcenter | migrationcenter/preferencesets | 0 | project | create | migrationcenter:projects.locations.preferenceSets.list |
| migrationcenter | migrationcenter/reportconfigs | 0 | project | create | migrationcenter:projects.locations.reportConfigs.list |
| migrationcenter | migrationcenter/reportconfigs/reports | 1 | project | create | migrationcenter:projects.locations.reportConfigs.reports.list |
| migrationcenter | migrationcenter/sources | 0 | project | create | migrationcenter:projects.locations.sources.list |
| ml | ml/jobs | 0 | project | create | ml:projects.jobs.list |
| ml | ml/models | 0 | project | create | ml:projects.models.list |
| ml | ml/models/versions | 1 | project | create | ml:projects.models.versions.list |
| ml | ml/studies | 0 | project | create | ml:projects.locations.studies.list |
| ml | ml/studies/trials | 1 | project | create | ml:projects.locations.studies.trials.list |
| monitoring | monitoring/metricdescriptors | 0 | project | create | monitoring:projects.metricDescriptors.list |
| netapp | netapp/activedirectories | 0 | project | create | netapp:projects.locations.activeDirectories.list |
| netapp | netapp/backuppolicies | 0 | project | create | netapp:projects.locations.backupPolicies.list |
| netapp | netapp/backupvaults | 0 | project | create | netapp:projects.locations.backupVaults.list |
| netapp | netapp/backupvaults/backups | 1 | project | create | netapp:projects.locations.backupVaults.backups.list |
| netapp | netapp/hostgroups | 0 | project | create | netapp:projects.locations.hostGroups.list |
| netapp | netapp/kmsconfigs | 0 | project | create | netapp:projects.locations.kmsConfigs.list |
| netapp | netapp/storagepools | 0 | project | create | netapp:projects.locations.storagePools.list |
| netapp | netapp/volumes | 0 | project | create | netapp:projects.locations.volumes.list |
| netapp | netapp/volumes/quotarules | 1 | project | create | netapp:projects.locations.volumes.quotaRules.list |
| netapp | netapp/volumes/replications | 1 | project | create | netapp:projects.locations.volumes.replications.list |
| netapp | netapp/volumes/snapshots | 1 | project | create | netapp:projects.locations.volumes.snapshots.list |
| networkconnectivity | networkconnectivity/automateddnsrecords | 0 | project | create | networkconnectivity:projects.locations.automatedDnsRecords.list |
| networkconnectivity | networkconnectivity/hubs | 0 | project | create | networkconnectivity:projects.locations.global.hubs.list |
| networkconnectivity | networkconnectivity/hubs/groups | 1 | project | mutable | networkconnectivity:projects.locations.global.hubs.groups.list |
| networkconnectivity | networkconnectivity/internalranges | 0 | project | create | networkconnectivity:projects.locations.internalRanges.list |
| networkconnectivity | networkconnectivity/multiclouddatatransferconfigs | 0 | project | create | networkconnectivity:projects.locations.multicloudDataTransferConfigs.list |
| networkconnectivity | networkconnectivity/multiclouddatatransferconfigs/destinations | 1 | project | create | networkconnectivity:projects.locations.multicloudDataTransferConfigs.destinations.list |
| networkconnectivity | networkconnectivity/policybasedroutes | 0 | project | create | networkconnectivity:projects.locations.global.policyBasedRoutes.list |
| networkconnectivity | networkconnectivity/pscauthorizationpolicies | 0 | project | create | networkconnectivity:projects.locations.pscAuthorizationPolicies.list |
| networkconnectivity | networkconnectivity/regionalendpoints | 0 | project | create | networkconnectivity:projects.locations.regionalEndpoints.list |
| networkconnectivity | networkconnectivity/serviceclasses | 0 | project | delete-only | networkconnectivity:projects.locations.serviceClasses.list |
| networkconnectivity | networkconnectivity/serviceconnectionmaps | 0 | project | create | networkconnectivity:projects.locations.serviceConnectionMaps.list |
| networkconnectivity | networkconnectivity/serviceconnectionpolicies | 0 | project | create | networkconnectivity:projects.locations.serviceConnectionPolicies.list |
| networkconnectivity | networkconnectivity/serviceconnectiontokens | 0 | project | create | networkconnectivity:projects.locations.serviceConnectionTokens.list |
| networkconnectivity | networkconnectivity/spokes | 0 | project | create | networkconnectivity:projects.locations.spokes.list |
| networkconnectivity | networkconnectivity/spokes/gatewayadvertisedroutes | 1 | project | create | networkconnectivity:projects.locations.spokes.gatewayAdvertisedRoutes.list |
| networkconnectivity | networkconnectivity/transports | 0 | project | create | networkconnectivity:projects.locations.transports.list |
| networkmanagement | networkmanagement/connectivitytests | 0 | project | create | networkmanagement:projects.locations.global.connectivityTests.list |
| networkmanagement | networkmanagement/networkmonitoringproviders | 0 | project | create | networkmanagement:projects.locations.networkMonitoringProviders.list |
| networkmanagement | networkmanagement/vpcflowlogsconfigs | 0 | project | create | networkmanagement:organizations.locations.vpcFlowLogsConfigs.list, networkmanagement:projects.locations.vpcFlowLogsConfigs.list, networkmanagement:projects.locations.vpcFlowLogsConfigs.queryOrgVpcFlowLogsConfigs |
| networksecurity | networksecurity/addressgroups | 0 | project | create | networksecurity:organizations.locations.addressGroups.list, networksecurity:projects.locations.addressGroups.list |
| networksecurity | networksecurity/authorizationpolicies | 0 | project | create | networksecurity:projects.locations.authorizationPolicies.list |
| networksecurity | networksecurity/authzpolicies | 0 | project | create | networksecurity:projects.locations.authzPolicies.list |
| networksecurity | networksecurity/backendauthenticationconfigs | 0 | project | create | networksecurity:projects.locations.backendAuthenticationConfigs.list |
| networksecurity | networksecurity/clienttlspolicies | 0 | project | create | networksecurity:projects.locations.clientTlsPolicies.list |
| networksecurity | networksecurity/dnsthreatdetectors | 0 | project | create | networksecurity:projects.locations.dnsThreatDetectors.list |
| networksecurity | networksecurity/firewallendpointassociations | 0 | project | create | networksecurity:projects.locations.firewallEndpointAssociations.list |
| networksecurity | networksecurity/firewallendpoints | 0 | project | create | networksecurity:organizations.locations.firewallEndpoints.list, networksecurity:projects.locations.firewallEndpoints.list |
| networksecurity | networksecurity/gatewaysecuritypolicies | 0 | project | create | networksecurity:projects.locations.gatewaySecurityPolicies.list |
| networksecurity | networksecurity/gatewaysecuritypolicies/rules | 1 | project | create | networksecurity:projects.locations.gatewaySecurityPolicies.rules.list |
| networksecurity | networksecurity/interceptdeploymentgroups | 0 | project | create | networksecurity:projects.locations.interceptDeploymentGroups.list |
| networksecurity | networksecurity/interceptdeployments | 0 | project | create | networksecurity:projects.locations.interceptDeployments.list |
| networksecurity | networksecurity/interceptendpointgroupassociations | 0 | project | create | networksecurity:projects.locations.interceptEndpointGroupAssociations.list |
| networksecurity | networksecurity/interceptendpointgroups | 0 | project | create | networksecurity:projects.locations.interceptEndpointGroups.list |
| networksecurity | networksecurity/mirroringdeploymentgroups | 0 | project | create | networksecurity:projects.locations.mirroringDeploymentGroups.list |
| networksecurity | networksecurity/mirroringdeployments | 0 | project | create | networksecurity:projects.locations.mirroringDeployments.list |
| networksecurity | networksecurity/mirroringendpointgroupassociations | 0 | project | create | networksecurity:projects.locations.mirroringEndpointGroupAssociations.list |
| networksecurity | networksecurity/mirroringendpointgroups | 0 | project | create | networksecurity:projects.locations.mirroringEndpointGroups.list |
| networksecurity | networksecurity/sacattachments | 0 | project | create | networksecurity:projects.locations.sacAttachments.list |
| networksecurity | networksecurity/sacrealms | 0 | project | create | networksecurity:projects.locations.sacRealms.list |
| networksecurity | networksecurity/securityprofilegroups | 0 | project | create | networksecurity:organizations.locations.securityProfileGroups.list, networksecurity:projects.locations.securityProfileGroups.list |
| networksecurity | networksecurity/securityprofiles | 0 | project | create | networksecurity:organizations.locations.securityProfiles.list, networksecurity:projects.locations.securityProfiles.list |
| networksecurity | networksecurity/servertlspolicies | 0 | project | create | networksecurity:projects.locations.serverTlsPolicies.list |
| networksecurity | networksecurity/tlsinspectionpolicies | 0 | project | create | networksecurity:projects.locations.tlsInspectionPolicies.list |
| networksecurity | networksecurity/urllists | 0 | project | create | networksecurity:projects.locations.urlLists.list |
| networkservices | networkservices/agentconnectivitytemplates | 0 | project | create | networkservices:projects.locations.agentConnectivityTemplates.list |
| networkservices | networkservices/agentgateways | 0 | project | create | networkservices:projects.locations.agentGateways.list |
| networkservices | networkservices/authzextensions | 0 | project | create | networkservices:projects.locations.authzExtensions.list |
| networkservices | networkservices/endpointpolicies | 0 | project | create | networkservices:projects.locations.endpointPolicies.list |
| networkservices | networkservices/gateways | 0 | project | create | networkservices:projects.locations.gateways.list |
| networkservices | networkservices/grpcroutes | 0 | project | create | networkservices:projects.locations.grpcRoutes.list |
| networkservices | networkservices/httproutes | 0 | project | create | networkservices:projects.locations.httpRoutes.list |
| networkservices | networkservices/lbedgeextensions | 0 | project | create | networkservices:projects.locations.lbEdgeExtensions.list |
| networkservices | networkservices/lbrouteextensions | 0 | project | create | networkservices:projects.locations.lbRouteExtensions.list |
| networkservices | networkservices/lbtrafficextensions | 0 | project | create | networkservices:projects.locations.lbTrafficExtensions.list |
| networkservices | networkservices/meshes | 0 | project | create | networkservices:projects.locations.meshes.list |
| networkservices | networkservices/multicastconsumerassociations | 0 | project | create | networkservices:projects.locations.multicastConsumerAssociations.list |
| networkservices | networkservices/multicastgroupconsumeractivations | 0 | project | create | networkservices:projects.locations.multicastGroupConsumerActivations.list |
| networkservices | networkservices/servicebindings | 0 | project | create | networkservices:projects.locations.serviceBindings.list |
| networkservices | networkservices/servicelbpolicies | 0 | project | create | networkservices:projects.locations.serviceLbPolicies.list |
| networkservices | networkservices/tcproutes | 0 | project | create | networkservices:projects.locations.tcpRoutes.list |
| networkservices | networkservices/tlsroutes | 0 | project | create | networkservices:projects.locations.tlsRoutes.list |
| networkservices | networkservices/wasmplugins | 0 | project | create | networkservices:projects.locations.wasmPlugins.list |
| networkservices | networkservices/wasmplugins/versions | 1 | project | create | networkservices:projects.locations.wasmPlugins.versions.list |
| notebooks | notebooks/environments | 0 | project | create | notebooks:projects.locations.environments.list |
| notebooks | notebooks/executions | 0 | project | create | notebooks:projects.locations.executions.list |
| notebooks | notebooks/instances | 0 | project | create | notebooks:projects.locations.instances.list |
| notebooks | notebooks/runtimes | 0 | project | create | notebooks:projects.locations.runtimes.list |
| notebooks | notebooks/schedules | 0 | project | create | notebooks:projects.locations.schedules.list |
| observability | observability/buckets/datasets/links | 2 | project | create | observability:projects.locations.buckets.datasets.links.list |
| observability | observability/tracescopes | 0 | project | create | observability:projects.locations.traceScopes.list |
| oracledatabase | oracledatabase/autonomousdatabases | 0 | project | create | oracledatabase:projects.locations.autonomousDatabases.list |
| oracledatabase | oracledatabase/cloudexadatainfrastructures | 0 | project | create | oracledatabase:projects.locations.cloudExadataInfrastructures.list |
| oracledatabase | oracledatabase/cloudvmclusters | 0 | project | create | oracledatabase:projects.locations.cloudVmClusters.list |
| oracledatabase | oracledatabase/dbsystems | 0 | project | create | oracledatabase:projects.locations.dbSystems.list |
| oracledatabase | oracledatabase/exadbvmclusters | 0 | project | create | oracledatabase:projects.locations.exadbVmClusters.list |
| oracledatabase | oracledatabase/exascaledbstoragevaults | 0 | project | create | oracledatabase:projects.locations.exascaleDbStorageVaults.list |
| oracledatabase | oracledatabase/goldengateconnectionassignments | 0 | project | create | oracledatabase:projects.locations.goldengateConnectionAssignments.list |
| oracledatabase | oracledatabase/goldengateconnections | 0 | project | create | oracledatabase:projects.locations.goldengateConnections.list |
| oracledatabase | oracledatabase/goldengatedeployments | 0 | project | create | oracledatabase:projects.locations.goldengateDeployments.list |
| oracledatabase | oracledatabase/odbnetworks | 0 | project | create | oracledatabase:projects.locations.odbNetworks.list |
| oracledatabase | oracledatabase/odbnetworks/odbsubnets | 1 | project | create | oracledatabase:projects.locations.odbNetworks.odbSubnets.list |
| orgpolicy | orgpolicy/customconstraints | 0 | org | create | orgpolicy:organizations.customConstraints.list |
| orgpolicy | orgpolicy/policies | 0 | project | create | orgpolicy:folders.policies.list, orgpolicy:organizations.policies.list, orgpolicy:projects.policies.list |
| osconfig | osconfig/ospolicyassignments | 0 | project | create | osconfig:projects.locations.osPolicyAssignments.list |
| osconfig | osconfig/patchdeployments | 0 | project | create | osconfig:projects.patchDeployments.list |
| osconfig | osconfig/patchjobs | 0 | project | create | osconfig:projects.patchJobs.list |
| osconfig | osconfig/policyorchestrators | 0 | project | create | osconfig:folders.locations.global.policyOrchestrators.list, osconfig:organizations.locations.global.policyOrchestrators.list, osconfig:projects.locations.global.policyOrchestrators.list |
| parallelstore | parallelstore/instances | 0 | project | create | parallelstore:projects.locations.instances.list |
| parametermanager | parametermanager/parameters | 0 | project | create | parametermanager:projects.locations.parameters.list |
| parametermanager | parametermanager/parameters/versions | 1 | project | create | parametermanager:projects.locations.parameters.versions.list |
| parametermanager | parametermanager/templates | 0 | project | create | parametermanager:projects.locations.templates.list |
| parametermanager | parametermanager/templates/versions | 1 | project | create | parametermanager:projects.locations.templates.versions.list |
| policysimulator | policysimulator/orgpolicyviolationspreviews | 0 | org | create | policysimulator:organizations.locations.orgPolicyViolationsPreviews.list |
| privateca | privateca/capools | 0 | project | create | privateca:projects.locations.caPools.list |
| privateca | privateca/capools/certificateauthorities | 1 | project | create | privateca:projects.locations.caPools.certificateAuthorities.list |
| privateca | privateca/capools/certificateauthorities/certificaterevocationlists | 2 | project | mutable | privateca:projects.locations.caPools.certificateAuthorities.certificateRevocationLists.list |
| privateca | privateca/capools/certificates | 1 | project | create | privateca:projects.locations.caPools.certificates.list |
| privateca | privateca/certificatetemplates | 0 | project | create | privateca:projects.locations.certificateTemplates.list |
| pubsublite | pubsublite/reservations | 0 | project | create | pubsublite:admin.projects.locations.reservations.list |
| pubsublite | pubsublite/subscriptions | 0 | project | create | pubsublite:admin.projects.locations.subscriptions.list |
| pubsublite | pubsublite/topics | 0 | project | create | pubsublite:admin.projects.locations.topics.list |
| rapidmigrationassessment | rapidmigrationassessment/collectors | 0 | project | create | rapidmigrationassessment:projects.locations.collectors.list |
| recaptchaenterprise | recaptchaenterprise/firewallpolicies | 0 | project | create | recaptchaenterprise:projects.firewallpolicies.list |
| recaptchaenterprise | recaptchaenterprise/keys | 0 | project | create | recaptchaenterprise:projects.keys.list |
| redis | redis/aclpolicies | 0 | project | create | redis:projects.locations.aclPolicies.list |
| redis | redis/backupcollections/backups | 1 | project | delete-only | redis:projects.locations.backupCollections.backups.list |
| redis | redis/clusters | 0 | project | create | redis:projects.locations.clusters.list |
| redis | redis/clusters/tokenauthusers | 1 | project | delete-only | redis:projects.locations.clusters.tokenAuthUsers.list |
| redis | redis/clusters/tokenauthusers/authtokens | 2 | project | delete-only | redis:projects.locations.clusters.tokenAuthUsers.authTokens.list |
| redis | redis/instances | 0 | project | create | redis:projects.locations.instances.list |
| resourcesettings | resourcesettings/settings | 0 | project | mutable | resourcesettings:folders.settings.list, resourcesettings:organizations.settings.list, resourcesettings:projects.settings.list |
| retail | retail/catalogs | 0 | project | mutable | retail:projects.locations.catalogs.list |
| retail | retail/catalogs/branches/products | 2 | project | create | retail:projects.locations.catalogs.branches.products.list |
| retail | retail/catalogs/controls | 1 | project | create | retail:projects.locations.catalogs.controls.list |
| retail | retail/catalogs/models | 1 | project | create | retail:projects.locations.catalogs.models.list |
| retail | retail/catalogs/servingconfigs | 1 | project | create | retail:projects.locations.catalogs.servingConfigs.list |
| run | run/workerpools/revisions | 1 | project | delete-only | run:projects.locations.workerPools.revisions.list |
| saasservicemgmt | saasservicemgmt/releases | 0 | project | create | saasservicemgmt:projects.locations.releases.list |
| saasservicemgmt | saasservicemgmt/rolloutkinds | 0 | project | create | saasservicemgmt:projects.locations.rolloutKinds.list |
| saasservicemgmt | saasservicemgmt/rollouts | 0 | project | create | saasservicemgmt:projects.locations.rollouts.list |
| saasservicemgmt | saasservicemgmt/saas | 0 | project | create | saasservicemgmt:projects.locations.saas.list |
| saasservicemgmt | saasservicemgmt/tenants | 0 | project | create | saasservicemgmt:projects.locations.tenants.list |
| saasservicemgmt | saasservicemgmt/unitkinds | 0 | project | create | saasservicemgmt:projects.locations.unitKinds.list |
| saasservicemgmt | saasservicemgmt/unitoperations | 0 | project | create | saasservicemgmt:projects.locations.unitOperations.list |
| saasservicemgmt | saasservicemgmt/units | 0 | project | create | saasservicemgmt:projects.locations.units.list |
| securesourcemanager | securesourcemanager/instances | 0 | project | create | securesourcemanager:projects.locations.instances.list |
| securesourcemanager | securesourcemanager/repositories | 0 | project | create | securesourcemanager:projects.locations.repositories.list |
| securesourcemanager | securesourcemanager/repositories/branchrules | 1 | project | create | securesourcemanager:projects.locations.repositories.branchRules.list |
| securesourcemanager | securesourcemanager/repositories/hooks | 1 | project | create | securesourcemanager:projects.locations.repositories.hooks.list |
| securesourcemanager | securesourcemanager/repositories/issues | 1 | project | create | securesourcemanager:projects.locations.repositories.issues.list |
| securesourcemanager | securesourcemanager/repositories/issues/issuecomments | 2 | project | create | securesourcemanager:projects.locations.repositories.issues.issueComments.list |
| securesourcemanager | securesourcemanager/repositories/pullrequests | 1 | project | create | securesourcemanager:projects.locations.repositories.pullRequests.list |
| securesourcemanager | securesourcemanager/repositories/pullrequests/pullrequestcomments | 2 | project | create | securesourcemanager:projects.locations.repositories.pullRequests.pullRequestComments.list |
| securitycenter | securitycenter/bigqueryexports | 0 | project | create | securitycenter:folders.bigQueryExports.list, securitycenter:organizations.bigQueryExports.list, securitycenter:projects.bigQueryExports.list |
| securitycenter | securitycenter/eventthreatdetectionsettings/custommodules | 0 | project | create | securitycenter:folders.eventThreatDetectionSettings.customModules.list, securitycenter:folders.eventThreatDetectionSettings.customModules.listDescendant, securitycenter:organizations.eventThreatDetectionSettings.customModules.list, securitycenter:organizations.eventThreatDetectionSettings.customModules.listDescendant, securitycenter:projects.eventThreatDetectionSettings.customModules.list, securitycenter:projects.eventThreatDetectionSettings.customModules.listDescendant |
| securitycenter | securitycenter/muteconfigs | 0 | project | create | securitycenter:folders.muteConfigs.list, securitycenter:organizations.muteConfigs.list, securitycenter:projects.muteConfigs.list |
| securitycenter | securitycenter/notificationconfigs | 0 | project | create | securitycenter:folders.notificationConfigs.list, securitycenter:organizations.notificationConfigs.list, securitycenter:projects.notificationConfigs.list |
| securitycenter | securitycenter/resourcevalueconfigs | 0 | org | delete-only | securitycenter:organizations.resourceValueConfigs.list |
| securitycenter | securitycenter/securityhealthanalyticssettings/custommodules | 0 | project | create | securitycenter:folders.securityHealthAnalyticsSettings.customModules.list, securitycenter:folders.securityHealthAnalyticsSettings.customModules.listDescendant, securitycenter:organizations.securityHealthAnalyticsSettings.customModules.list, securitycenter:organizations.securityHealthAnalyticsSettings.customModules.listDescendant, securitycenter:projects.securityHealthAnalyticsSettings.customModules.list, securitycenter:projects.securityHealthAnalyticsSettings.customModules.listDescendant |
| securitycenter | securitycenter/sources | 0 | org | create | securitycenter:organizations.sources.list |
| securityposture | securityposture/posturedeployments | 0 | org | create | securityposture:organizations.locations.postureDeployments.list |
| securityposture | securityposture/postures | 0 | org | create | securityposture:organizations.locations.postures.list |
| serviceconsumermanagement | serviceconsumermanagement/services/tenancyunits | 1 | global | create | serviceconsumermanagement:services.tenancyUnits.list |
| servicedirectory | servicedirectory/namespaces | 0 | project | create | servicedirectory:projects.locations.namespaces.list |
| servicedirectory | servicedirectory/namespaces/services | 1 | project | create | servicedirectory:projects.locations.namespaces.services.list |
| servicedirectory | servicedirectory/namespaces/services/endpoints | 2 | project | create | servicedirectory:projects.locations.namespaces.services.endpoints.list |
| servicemanagement | servicemanagement/services | 0 | global | create | servicemanagement:services.list |
| servicemanagement | servicemanagement/services/configs | 1 | global | create | servicemanagement:services.configs.list |
| servicemanagement | servicemanagement/services/rollouts | 1 | global | create | servicemanagement:services.rollouts.list |
| servicenetworking | servicenetworking/services/connections | 1 | global | create | servicenetworking:services.connections.list |
| servicenetworking | servicenetworking/services/global/networks/peereddnsdomains | 2 | global | create | servicenetworking:services.projects.global.networks.peeredDnsDomains.list |
| sourcerepo | sourcerepo/repos | 0 | project | create | sourcerepo:projects.repos.list |
| spanner | spanner/instances/databases/sessions | 2 | project | create | spanner:projects.instances.databases.sessions.list |
| speech | speech/customclasses | 0 | project | create | speech:projects.locations.customClasses.list |
| speech | speech/phrasesets | 0 | project | create | speech:projects.locations.phraseSets.list |
| sqladmin | sqladmin/backups | 0 | project | create | sqladmin:Backups.ListBackups, sqladmin:backups.listBackups |
| storage | storage/objectaccesscontrols | 2 | global | create | storage:objectAccessControls.list |
| storage | storage/objects | 1 | global | create | storage:objects.list |
| storagebatchoperations | storagebatchoperations/jobs | 0 | project | create | storagebatchoperations:projects.locations.jobs.list |
| storagetransfer | storagetransfer/agentpools | 0 | project | create | storagetransfer:projects.agentPools.list |
| storagetransfer | storagetransfer/transferjobs | 0 | global | create | storagetransfer:transferJobs.list |
| testing | testing/devicesessions | 0 | project | create | testing:projects.deviceSessions.list |
| tpu | tpu/nodes | 0 | project | create | tpu:projects.locations.nodes.list |
| tpu | tpu/queuedresources | 0 | project | create | tpu:projects.locations.queuedResources.list |
| transcoder | transcoder/jobs | 0 | project | create | transcoder:projects.locations.jobs.list |
| transcoder | transcoder/jobtemplates | 0 | project | create | transcoder:projects.locations.jobTemplates.list |
| translate | translate/adaptivemtdatasets | 0 | project | create | translate:projects.locations.adaptiveMtDatasets.list |
| translate | translate/adaptivemtdatasets/adaptivemtfiles | 1 | project | delete-only | translate:projects.locations.adaptiveMtDatasets.adaptiveMtFiles.list |
| translate | translate/datasets | 0 | project | create | translate:projects.locations.datasets.list |
| translate | translate/glossaries | 0 | project | create | translate:projects.locations.glossaries.list |
| translate | translate/glossaries/glossaryentries | 1 | project | create | translate:projects.locations.glossaries.glossaryEntries.list |
| translate | translate/models | 0 | project | create | translate:projects.locations.models.list |
| vision | vision/products | 0 | project | create | vision:projects.locations.products.list |
| vision | vision/products/referenceimages | 1 | project | create | vision:projects.locations.products.referenceImages.list |
| vision | vision/productsets | 0 | project | create | vision:projects.locations.productSets.list |
| vmmigration | vmmigration/groups | 0 | project | create | vmmigration:projects.locations.groups.list |
| vmmigration | vmmigration/imageimports | 0 | project | create | vmmigration:projects.locations.imageImports.list |
| vmmigration | vmmigration/sources | 0 | project | create | vmmigration:projects.locations.sources.list |
| vmmigration | vmmigration/sources/datacenterconnectors | 1 | project | create | vmmigration:projects.locations.sources.datacenterConnectors.list |
| vmmigration | vmmigration/sources/diskmigrationjobs | 1 | project | create | vmmigration:projects.locations.sources.diskMigrationJobs.list |
| vmmigration | vmmigration/sources/migratingvms | 1 | project | create | vmmigration:projects.locations.sources.migratingVms.list |
| vmmigration | vmmigration/sources/migratingvms/clonejobs | 2 | project | create | vmmigration:projects.locations.sources.migratingVms.cloneJobs.list |
| vmmigration | vmmigration/sources/migratingvms/cutoverjobs | 2 | project | create | vmmigration:projects.locations.sources.migratingVms.cutoverJobs.list |
| vmmigration | vmmigration/sources/utilizationreports | 1 | project | create | vmmigration:projects.locations.sources.utilizationReports.list |
| vmmigration | vmmigration/targetprojects | 0 | project | create | vmmigration:projects.locations.targetProjects.list |
| vmwareengine | vmwareengine/datastores | 0 | project | create | vmwareengine:projects.locations.datastores.list |
| vmwareengine | vmwareengine/networkpeerings | 0 | project | create | vmwareengine:projects.locations.networkPeerings.list |
| vmwareengine | vmwareengine/networkpolicies | 0 | project | create | vmwareengine:projects.locations.networkPolicies.list |
| vmwareengine | vmwareengine/networkpolicies/externalaccessrules | 1 | project | create | vmwareengine:projects.locations.networkPolicies.externalAccessRules.list |
| vmwareengine | vmwareengine/privateclouds | 0 | project | create | vmwareengine:projects.locations.privateClouds.list |
| vmwareengine | vmwareengine/privateclouds/clusters | 1 | project | create | vmwareengine:projects.locations.privateClouds.clusters.list |
| vmwareengine | vmwareengine/privateclouds/externaladdresses | 1 | project | create | vmwareengine:projects.locations.privateClouds.externalAddresses.list |
| vmwareengine | vmwareengine/privateclouds/hcxactivationkeys | 1 | project | create | vmwareengine:projects.locations.privateClouds.hcxActivationKeys.list |
| vmwareengine | vmwareengine/privateclouds/loggingservers | 1 | project | create | vmwareengine:projects.locations.privateClouds.loggingServers.list |
| vmwareengine | vmwareengine/privateclouds/managementdnszonebindings | 1 | project | create | vmwareengine:projects.locations.privateClouds.managementDnsZoneBindings.list |
| vmwareengine | vmwareengine/privateclouds/subnets | 1 | project | mutable | vmwareengine:projects.locations.privateClouds.subnets.list |
| vmwareengine | vmwareengine/privateclouds/upgrades | 1 | project | mutable | vmwareengine:projects.locations.privateClouds.upgrades.list |
| vmwareengine | vmwareengine/privateconnections | 0 | project | create | vmwareengine:projects.locations.privateConnections.list |
| vmwareengine | vmwareengine/vmwareenginenetworks | 0 | project | create | vmwareengine:projects.locations.vmwareEngineNetworks.list |
| vpcaccess | vpcaccess/connectors | 0 | project | create | vpcaccess:projects.locations.connectors.list |
| websecurityscanner | websecurityscanner/scanconfigs | 0 | project | create | websecurityscanner:projects.scanConfigs.list |
| websecurityscanner | websecurityscanner/scanconfigs/scanruns | 1 | project | created-elsewhere | websecurityscanner:projects.scanConfigs.scanRuns.list |
| workflowexecutions | workflowexecutions/workflows/executions | 1 | project | create | workflowexecutions:projects.locations.workflows.executions.list |
| workflows | workflows/workflows | 0 | project | create | workflows:projects.locations.workflows.list |
| workloadmanager | workloadmanager/deployments | 0 | project | create | workloadmanager:projects.locations.deployments.list |
| workloadmanager | workloadmanager/deployments/actuations | 1 | project | create | workloadmanager:projects.locations.deployments.actuations.list |
| workloadmanager | workloadmanager/evaluations | 0 | project | create | workloadmanager:projects.locations.evaluations.list |
| workloadmanager | workloadmanager/evaluations/executions | 1 | project | delete-only | workloadmanager:projects.locations.evaluations.executions.list |
| workstations | workstations/workstationclusters | 0 | project | create | workstations:projects.locations.workstationClusters.list |
| workstations | workstations/workstationclusters/workstationconfigs | 1 | project | create | workstations:projects.locations.workstationClusters.workstationConfigs.list, workstations:projects.locations.workstationClusters.workstationConfigs.listUsable |
| workstations | workstations/workstationclusters/workstationconfigs/workstations | 2 | project | create | workstations:projects.locations.workstationClusters.workstationConfigs.workstations.list, workstations:projects.locations.workstationClusters.workstationConfigs.workstations.listUsable |

