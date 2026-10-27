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

// parseARN parses s as an ARN, and returns false when s is not an ARN or names
// a partition the pinned SDK does not know. Matchers of ARNs read from SDK
// responses and policy documents use it instead of an "arn:aws:" prefix, so an
// ARN matches in every partition and an unknown partition matches nothing.
func parseARN(s string) (arn.ARN, bool) {
	a, err := arn.Parse(s)
	if err != nil {
		return arn.ARN{}, false
	}
	if _, ok := lookupPartition(a.Partition); !ok {
		return arn.ARN{}, false
	}
	return a, true
}

// isARNOf reports whether s is an ARN of service in a partition the pinned SDK
// knows. It checks only the partition and the service segment; a caller that
// needs one resource type checks the resource segment itself.
func isARNOf(s, service string) bool {
	a, ok := parseARN(s)
	return ok && a.Service == service
}

// s3BucketARNFromLocation returns the bucket ARN that an S3 bucket or object
// ARN (arn:{p}:s3:::bucket[/key]) names, and "" for any other input, including
// an access-point ARN, which carries a region and an account.
func s3BucketARNFromLocation(s string) string {
	a, ok := parseARN(s)
	if !ok || a.Service != "s3" || a.Region != "" || a.AccountID != "" {
		return ""
	}
	bucket, _, _ := strings.Cut(a.Resource, "/")
	if bucket == "" {
		return ""
	}
	return s3BucketARN(a.Partition, bucket)
}

// partitionOf returns the partition segment of an ARN, and false when s is not
// an ARN or names a partition the pinned SDK does not know.
func partitionOf(s string) (string, bool) {
	a, ok := parseARN(s)
	return a.Partition, ok
}

// dnsSuffix returns the partition's service DNS suffix (amazonaws.com,
// amazonaws.com.cn, …), and false for a partition the pinned SDK does not know.
func dnsSuffix(partition string) (string, bool) {
	p, ok := lookupPartition(partition)
	return p.DNSSuffix, ok
}

// hostRegion returns the region of a regional endpoint host,
// {prefix}{region}.{dnsSuffix(partition)}, and false for any other shape. A
// prefix ending in "." also matches its FIPS form ("sqs." → "sqs-fips."), the
// endpoint GovCloud workloads normally use. The region must belong to
// partition: no resource is reachable across partitions, so a host of another
// partition names nothing the account can reference, and under aws-us-gov,
// which shares the amazonaws.com suffix, the region is what tells a commercial
// host apart.
func hostRegion(partition, host, prefix string) (string, bool) {
	suffix, ok := dnsSuffix(partition)
	if !ok {
		return "", false
	}
	rest, ok := strings.CutPrefix(host, prefix)
	if !ok && strings.HasSuffix(prefix, ".") {
		rest, ok = strings.CutPrefix(host, strings.TrimSuffix(prefix, ".")+"-fips.")
	}
	if !ok {
		return "", false
	}
	region, ok := strings.CutSuffix(rest, "."+suffix)
	if !ok || region == "" || strings.Contains(region, ".") || !regionInPartition(partition, region) {
		return "", false
	}
	return region, true
}

// regionInPartition reports whether region belongs to partition.
func regionInPartition(partition, region string) bool {
	p, ok := awsregions.PartitionForRegion(region)
	return ok && p == partition
}

// s3AcceleratePartition is the one partition whose S3 Transfer Acceleration
// hosts, {bucket}.s3-accelerate[.dualstack[.{region}]].{suffix}, name a bucket.
// It is hand-coded because the SDK cannot be the source: the S3 endpoint rules
// (service/s3 endpoints.go) build accelerate hosts in every partition except
// aws-cn, which is more permissive than the service. GovCloud documents "Amazon
// S3 Transfer Acceleration is not available"
// (https://docs.aws.amazon.com/govcloud-us/latest/UserGuide/govcloud-s3.html),
// and nothing documents it for the other partitions, so an accelerate host
// names no bucket there.
const s3AcceleratePartition = "aws"

// s3HostBucket splits an S3 host into the bucket it names and reports whether
// it is a bucket endpoint of partition; bucket is "" for a path-style host. The
// host is lowercased first, and a ":port" and one trailing "." are dropped; an
// empty label then fails.
//
// The endpoint starts at the last label beginning with "s3", so a bucket named
// "x.s3data" survives, and ends at dnsSuffix(partition). It must be one of
// (s3EndpointShape; {region} is a region of partition, so a region of another
// partition or an unknown label such as fips-us-gov-west-1 fails):
//
//	s3[.dualstack].{region}       s3-fips[.dualstack].{region}
//	s3[-fips].{region}.vpce       s3-website.{region}
//	s3-{region}                   s3-website-{region}
//	s3-accelerate[.dualstack[.{region}]]   (s3AcceleratePartition only)
//
// or a region-less endpoint the S3 SDK's endpoint table lists for partition
// (awsregions.S3RegionlessHosts). A vpce endpoint is an interface endpoint:
// the label before it must be vpce-{id}, which is not part of the bucket, and
// accesspoint.vpce-{id} or control.vpce-{id} names no bucket.
func s3HostBucket(partition, host string) (string, bool) {
	suffix, ok := dnsSuffix(partition)
	if !ok {
		return "", false
	}
	host = strings.ToLower(host)
	if i := strings.LastIndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimSuffix(host, ".")
	name, ok := strings.CutSuffix(host, "."+suffix)
	if !ok {
		return "", false
	}
	labels := strings.Split(name, ".")
	if slices.Contains(labels, "") {
		return "", false
	}
	i := len(labels) - 1
	for i >= 0 && !strings.HasPrefix(labels[i], "s3") {
		i--
	}
	if i < 0 {
		return "", false
	}
	bucket := labels[:i]
	if slices.Contains(awsregions.S3RegionlessHosts[partition], strings.Join(labels[i:], ".")+"."+suffix) {
		return strings.Join(bucket, "."), true
	}
	vpce, ok := s3EndpointShape(partition, labels[i], labels[i+1:])
	if !ok {
		return "", false
	}
	if vpce {
		if len(bucket) == 0 || !strings.HasPrefix(bucket[len(bucket)-1], "vpce-") {
			return "", false
		}
		bucket = bucket[:len(bucket)-1]
		if slices.Equal(bucket, []string{"accesspoint"}) || slices.Equal(bucket, []string{"control"}) {
			return "", false
		}
	}
	return strings.Join(bucket, "."), true
}

// s3EndpointShape reports whether endpoint followed by rest is one of the
// regional endpoint forms s3HostBucket lists, and whether it is the vpce form.
// rest is read in AWS's order: an optional "dualstack", an optional region of
// partition, an optional "vpce", and nothing else.
func s3EndpointShape(partition, endpoint string, rest []string) (vpce, ok bool) {
	next := func(match func(string) bool) bool {
		if len(rest) > 0 && match(rest[0]) {
			rest = rest[1:]
			return true
		}
		return false
	}
	dualstack := next(func(l string) bool { return l == "dualstack" })
	region := next(func(l string) bool { return regionInPartition(partition, l) })
	vpce = next(func(l string) bool { return l == "vpce" })
	if len(rest) > 0 {
		return false, false
	}
	switch endpoint {
	case "s3", "s3-fips":
		return vpce, region && (!vpce || !dualstack)
	case "s3-website":
		return false, region && !dualstack && !vpce
	case "s3-accelerate":
		// The SDK builds a regional accelerate host only with dualstack.
		return false, partition == s3AcceleratePartition && !vpce && (dualstack || !region)
	}
	// s3-{region} and s3-website-{region} carry the region; nothing follows.
	inLabel, found := strings.CutPrefix(endpoint, "s3-website-")
	if !found {
		inLabel, found = strings.CutPrefix(endpoint, "s3-")
	}
	return false, found && regionInPartition(partition, inLabel) && !dualstack && !region && !vpce
}

// sqsQueueARNFromURL parses an SQS queue URL,
// https://sqs[-fips].{region}.{dnsSuffix}/{acct}/{name}, into the queue ARN in
// partition. Returns "" for any other shape, including a host of another
// partition (hostRegion).
func sqsQueueARNFromURL(partition, u string) string {
	rest, ok := strings.CutPrefix(u, "https://")
	if !ok {
		return ""
	}
	host, path, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	region, ok := hostRegion(partition, host, "sqs.")
	if !ok {
		return ""
	}
	acctID, name, ok := strings.Cut(path, "/")
	if !ok || acctID == "" || name == "" {
		return ""
	}
	return arnFor(partition, "sqs", region, acctID, name)
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

// kmsKeyARN builds a KMS key's ARN from its bare key id: resolvers expand
// key-id and alias-target references to the NativeID the key scanner stores.
func kmsKeyARN(partition, region, accountID, keyID string) string {
	return arnFor(partition, "kms", region, accountID, "key/"+keyID)
}

// iamRoleARN and iamInstanceProfileARN build the ARN of a role or instance
// profile in accountID from the bare name another resource references.
func iamRoleARN(partition, accountID, name string) string {
	return arnFor(partition, "iam", "", accountID, "role/"+name)
}

func iamInstanceProfileARN(partition, accountID, name string) string {
	return arnFor(partition, "iam", "", accountID, "instance-profile/"+name)
}

// kmsKeyRefARN returns the key ARN a key reference names: an ARN as given, or
// a bare key id expanded in the given partition, region and account.
func kmsKeyRefARN(partition, region, accountID, ref string) string {
	if strings.HasPrefix(ref, "arn:") {
		return ref
	}
	return kmsKeyARN(partition, region, accountID, ref)
}

// eventBusARN builds an EventBridge bus ARN from the bus name a rule, an
// integration or a CloudFormation stack references.
func eventBusARN(partition, region, accountID, name string) string {
	return arnFor(partition, "events", region, accountID, "event-bus/"+name)
}

// efsFileSystemARN builds an EFS file system's ARN from its fs- id, the form
// the file-system scanner stores and the mount-target NativeID extends.
func efsFileSystemARN(partition, region, accountID, fsID string) string {
	return arnFor(partition, "elasticfilesystem", region, accountID, "file-system/"+fsID)
}

// ecrRepositoryARN builds the ARN of the ECR repository name, the NativeID
// the ECR scanner stores (RepositoryArn).
func ecrRepositoryARN(partition, region, accountID, name string) string {
	return arnFor(partition, "ecr", region, accountID, "repository/"+name)
}

// ecrImageRepoARN returns the repository ARN of a private ECR image URI,
// {acct}.dkr.ecr[-fips].{region}.{dnsSuffix}/{repo}[:tag|@digest], in
// partition. It returns "" for public ECR (public.ecr.aws), other registries,
// and a host of another partition (hostRegion). Repository names may contain
// "/" but never ":" or "@", so the first of those starts the tag or digest.
func ecrImageRepoARN(partition, image string) string {
	host, path, ok := strings.Cut(image, "/")
	if !ok {
		return ""
	}
	acct, rest, ok := strings.Cut(host, ".")
	if !ok || acct == "" {
		return ""
	}
	region, ok := hostRegion(partition, rest, "dkr.ecr.")
	if !ok {
		return ""
	}
	repo, _, _ := strings.Cut(path, "@")
	repo, _, _ = strings.Cut(repo, ":")
	if repo == "" {
		return ""
	}
	return ecrRepositoryARN(partition, region, acct, repo)
}

// kinesisStreamARNFromName and firehoseDeliveryStreamARNFromName build the
// stream ARN another service references by name.
func kinesisStreamARNFromName(partition, region, accountID, name string) string {
	return arnFor(partition, "kinesis", region, accountID, "stream/"+name)
}

func firehoseDeliveryStreamARNFromName(partition, region, accountID, name string) string {
	return arnFor(partition, "firehose", region, accountID, "deliverystream/"+name)
}
