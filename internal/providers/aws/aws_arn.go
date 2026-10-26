package aws

import (
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
//   - ec2ARNIn: arn:{p}:ec2:{region}:{account}:{kind}/{id} — slash separator.
//   - rdsARNIn: arn:{p}:rds:{region}:{account}:{kind}:{id} — colon separator.
//   - apigatewayARNIn: arn:{p}:apigateway:{region}::/{path...} — empty
//     account segment, leading slash, variadic path components joined "/".
//   - logGroupARNIn: arn:{p}:logs:{region}:{account}:log-group:{name}.
//     SDK ARNs come back with a trailing ":*"; the NativeID strips it.
//   - macieSessionNativeIDIn: arn:{p}:macie2:{region}:{account}:session — the
//     Macie API exposes no session ARN; the synthetic form mirrors the
//     canonical macie2 shape so rescans dedupe.
//   - ssoAssignmentNativeID: {psArn}/account/{acct}/{principalType}/{principalID}.
//     AWS issues no canonical ARN for assignments; permission-set ARN already
//     embeds the instance id, so extending it carries enough context.
//   - identityStoreUserNativeIDIn / identityStoreGroupNativeIDIn:
//     arn:{p}:identitystore::{ownerAcct}:{user|group}/{storeID}/{id}.
//     Identity Store APIs return no ARNs.
//   - cloudhsmClusterARNIn: arn:{p}:cloudhsm:{region}:{account}:cluster/{id} —
//     DescribeClusters returns no ARN field.
//
// Each builder takes the partition first. Scanners and resolvers pass
// acct.partition(): an account lies in one partition, and the target's own
// scanner keys it with that partition. Don't read the partition from a source
// row's NativeID: it adds nothing, and a synthetic NativeID can carry a
// partition that differs from the one the target's scanner used.

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

func lookupPartition(id string) (awsregions.Partition, bool) {
	i := slices.IndexFunc(awsregions.Partitions, func(p awsregions.Partition) bool { return p.ID == id })
	if i < 0 {
		return awsregions.Partition{}, false
	}
	return awsregions.Partitions[i], true
}

// cloudhsmClusterARNIn builds the NativeID scanCloudHSMClusters gives a cluster.
func cloudhsmClusterARNIn(partition, region, accountID, clusterID string) string {
	return arnFor(partition, "cloudhsm", region, accountID, "cluster/"+clusterID)
}

// ec2ARNIn builds a standard EC2 ARN: arn:{p}:ec2:{region}:{account}:{type}/{id}.
func ec2ARNIn(partition, region, accountID, resourceType, id string) string {
	return arnFor(partition, "ec2", region, accountID, resourceType+"/"+id)
}

// rdsARNIn builds a standard RDS ARN; unlike EC2's "/" separator, RDS uses ":"
// (e.g. arn:aws:rds:us-east-1:123456789012:cluster:my-cluster).
func rdsARNIn(partition, region, accountID, resource, id string) string {
	return arnFor(partition, "rds", region, accountID, resource+":"+id)
}

// apigatewayARNIn builds an API Gateway ARN: arn:{p}:apigateway:{region}::/p1/p2/....
// The account segment is always empty (REST and HTTP/WebSocket v2 share this shape).
func apigatewayARNIn(partition, region string, path ...string) string {
	return arnFor(partition, "apigateway", region, "", "/"+strings.Join(path, "/"))
}

// logGroupARNIn reconstructs the log group ARN (NativeID) from its name:
// arn:{p}:logs:{region}:{account}:log-group:{name}. The SDK sometimes appends
// ":*" to log-group ARNs; NativeID is the clean form without it.
func logGroupARNIn(partition, accountID, region, name string) string {
	return arnFor(partition, "logs", region, accountID, "log-group:"+name)
}

// macieSessionNativeIDIn synthesises an ARN-shaped identifier for the
// per-region Macie session. The Macie API exposes no session ARN; the
// synthetic form matches the canonical macie2 ARN shape so rescans dedupe.
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

// identityStoreUserNativeIDIn and identityStoreGroupNativeIDIn synthesise
// ARN-shaped IDs scoped by the instance owner account and identity-store
// id. Identity Store APIs do not return ARNs.
func identityStoreUserNativeIDIn(partition, ownerAccountID, identityStoreID, userID string) string {
	return arnFor(partition, "identitystore", "", ownerAccountID, "user/"+identityStoreID+"/"+userID)
}

func identityStoreGroupNativeIDIn(partition, ownerAccountID, identityStoreID, groupID string) string {
	return arnFor(partition, "identitystore", "", ownerAccountID, "group/"+identityStoreID+"/"+groupID)
}
