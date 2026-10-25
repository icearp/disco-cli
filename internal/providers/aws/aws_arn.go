package aws

import (
	"regexp"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/arn"

	"github.com/icearp/disco-cli/internal/providers/aws/awsregions"
)

// ARN and synthetic-NativeID builders shared across services. Each helper
// is called from at least one resolver/scanner outside its owning service,
// so centralising the shape keeps scanner and resolver in sync — a shape
// bug fixes in one place instead of drifting silently.
//
// Conventions per service ({p} is the partition: aws, aws-cn, aws-us-gov, …):
//   - ec2ARN: arn:{p}:ec2:{region}:{account}:{kind}/{id} — slash separator.
//   - rdsARN: arn:{p}:rds:{region}:{account}:{kind}:{id} — colon separator.
//   - apigatewayARN: arn:{p}:apigateway:{region}::/{path...} — empty
//     account segment, leading slash, variadic path components joined "/".
//   - logGroupNativeIDFromName: arn:{p}:logs:{region}:{account}:log-group:{name}.
//     SDK ARNs come back with a trailing ":*"; the NativeID strips it.
//   - macieSessionNativeID: arn:{p}:macie2:{region}:{account}:session — the
//     Macie API exposes no session ARN; the synthetic form mirrors the
//     canonical macie2 shape so rescans dedupe.
//   - ssoAssignmentNativeID: {psArn}/account/{acct}/{principalType}/{principalID}.
//     AWS issues no canonical ARN for assignments; permission-set ARN already
//     embeds the instance id, so extending it carries enough context.
//   - identityStoreUserNativeID / identityStoreGroupNativeID:
//     arn:{p}:identitystore::{ownerAcct}:{user|group}/{storeID}/{id}.
//     Identity Store APIs return no ARNs.
//   - cloudhsmClusterARN: arn:{p}:cloudhsm:{region}:{account}:cluster/{id} —
//     DescribeClusters returns no ARN field.
//
// Each builder has a partition-taking core named *In. The forms without a
// partition build the aws-partition value and stay until their callers pass
// the account's partition.

// arnFor builds arn:{partition}:{service}:{region}:{account}:{resource}.
func arnFor(partition, service, region, account, resource string) string {
	return "arn:" + partition + ":" + service + ":" + region + ":" + account + ":" + resource
}

// partitionOf returns the partition segment of an ARN, and false when s is not
// an ARN or names a partition the pinned SDK does not know.
func partitionOf(s string) (string, bool) {
	a, err := arn.Parse(s)
	if err != nil {
		return "", false
	}
	if _, ok := lookupPartition(a.Partition); !ok {
		return "", false
	}
	return a.Partition, true
}

// dnsSuffix returns the partition's service DNS suffix (amazonaws.com,
// amazonaws.com.cn, …), and false for a partition the pinned SDK does not know.
func dnsSuffix(partition string) (string, bool) {
	p, ok := lookupPartition(partition)
	return p.DNSSuffix, ok
}

// partitionForRegion returns the partition a region belongs to, and false
// when no partition lists it or matches it.
//
// The order mirrors the SDK's own lookup (awsrulesfn.GetPartition): a region a
// partition lists by name wins over every regex, and only then are the regexes
// tried in table order. Unlike the SDK, an unmatched region is not assigned to
// the commercial partition.
func partitionForRegion(region string) (string, bool) {
	for _, p := range awsregions.Partitions {
		if slices.Contains(p.Regions, region) {
			return p.ID, true
		}
	}
	for i, re := range partitionRegexps {
		if re.MatchString(region) {
			return awsregions.Partitions[i].ID, true
		}
	}
	return "", false
}

// partitionRegexps holds each partition's compiled regionRegex, index-aligned
// with awsregions.Partitions. The generator rejects a pattern that does not
// compile, so MustCompile cannot fire on a committed table.
var partitionRegexps = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(awsregions.Partitions))
	for i, p := range awsregions.Partitions {
		out[i] = regexp.MustCompile(p.RegionRegex)
	}
	return out
}()

func lookupPartition(id string) (awsregions.Partition, bool) {
	i := slices.IndexFunc(awsregions.Partitions, func(p awsregions.Partition) bool { return p.ID == id })
	if i < 0 {
		return awsregions.Partition{}, false
	}
	return awsregions.Partitions[i], true
}

// cloudhsmClusterARN builds the NativeID scanCloudHSMClusters gives a cluster.
func cloudhsmClusterARN(region, accountID, clusterID string) string {
	return cloudhsmClusterARNIn("aws", region, accountID, clusterID)
}

func cloudhsmClusterARNIn(partition, region, accountID, clusterID string) string {
	return arnFor(partition, "cloudhsm", region, accountID, "cluster/"+clusterID)
}

// ec2ARN builds a standard EC2 ARN: arn:aws:ec2:{region}:{account}:{type}/{id}.
func ec2ARN(region, accountID, resourceType, id string) string {
	return ec2ARNIn("aws", region, accountID, resourceType, id)
}

func ec2ARNIn(partition, region, accountID, resourceType, id string) string {
	return arnFor(partition, "ec2", region, accountID, resourceType+"/"+id)
}

// rdsARN builds a standard RDS ARN; unlike EC2's "/" separator, RDS uses ":"
// (e.g. arn:aws:rds:us-east-1:123456789012:cluster:my-cluster).
func rdsARN(region, accountID, resource, id string) string {
	return rdsARNIn("aws", region, accountID, resource, id)
}

func rdsARNIn(partition, region, accountID, resource, id string) string {
	return arnFor(partition, "rds", region, accountID, resource+":"+id)
}

// apigatewayARN builds an API Gateway ARN: arn:aws:apigateway:{region}::/p1/p2/....
// The account segment is always empty (REST and HTTP/WebSocket v2 share this shape).
func apigatewayARN(region string, path ...string) string {
	return apigatewayARNIn("aws", region, path...)
}

func apigatewayARNIn(partition, region string, path ...string) string {
	return arnFor(partition, "apigateway", region, "", "/"+strings.Join(path, "/"))
}

// logGroupNativeIDFromName reconstructs the log group ARN (NativeID) from its
// name: arn:aws:logs:{region}:{account}:log-group:{name}. The SDK sometimes
// appends ":*" to log-group ARNs; NativeID is the clean form without it.
func logGroupNativeIDFromName(accountID, region, name string) string {
	return logGroupARNIn("aws", accountID, region, name)
}

// logGroupARNIn is logGroupNativeIDFromName in partition. The log-group
// scanner stores the ARN DescribeLogGroups returns, so outside the aws
// partition only this form matches it.
func logGroupARNIn(partition, accountID, region, name string) string {
	return arnFor(partition, "logs", region, accountID, "log-group:"+name)
}

// macieSessionNativeID synthesises an ARN-shaped identifier for the per-region
// Macie session. The Macie API exposes no session ARN; the synthetic form
// matches the canonical macie2 ARN shape so rescans dedupe.
func macieSessionNativeID(accountID, region string) string {
	return macieSessionNativeIDIn("aws", accountID, region)
}

func macieSessionNativeIDIn(partition, accountID, region string) string {
	return arnFor(partition, "macie2", region, accountID, "session")
}

// ssoAssignmentNativeID synthesises a stable identifier for an account
// assignment: AWS issues no canonical ARN for assignments, so the
// permission-set ARN (already carrying the instance id) is extended with
// the account, principal type, and principal id.
func ssoAssignmentNativeID(psArn, accountID, principalType, principalID string) string {
	return psArn + "/account/" + accountID + "/" + principalType + "/" + principalID
}

// identityStoreUserNativeID and identityStoreGroupNativeID synthesise
// ARN-shaped IDs scoped by the instance owner account and identity-store
// id. Identity Store APIs do not return ARNs.
func identityStoreUserNativeID(ownerAccountID, identityStoreID, userID string) string {
	return identityStoreUserNativeIDIn("aws", ownerAccountID, identityStoreID, userID)
}

func identityStoreGroupNativeID(ownerAccountID, identityStoreID, groupID string) string {
	return identityStoreGroupNativeIDIn("aws", ownerAccountID, identityStoreID, groupID)
}

func identityStoreUserNativeIDIn(partition, ownerAccountID, identityStoreID, userID string) string {
	return arnFor(partition, "identitystore", "", ownerAccountID, "user/"+identityStoreID+"/"+userID)
}

func identityStoreGroupNativeIDIn(partition, ownerAccountID, identityStoreID, groupID string) string {
	return arnFor(partition, "identitystore", "", ownerAccountID, "group/"+identityStoreID+"/"+groupID)
}
