package aws

import (
	acmtypes "github.com/aws/aws-sdk-go-v2/service/acm/types"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	cloudfronttypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	elasticachetypes "github.com/aws/aws-sdk-go-v2/service/elasticache/types"
	firehosetypes "github.com/aws/aws-sdk-go-v2/service/firehose/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	kinesistypes "github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	rdstypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// awsTag is the set of AWS SDK tag types that carry Key and Value string pointers.
type awsTag interface {
	acmtypes.Tag | cfntypes.Tag | cloudfronttypes.Tag | ec2types.Tag | ecrtypes.Tag | ecstypes.Tag | elasticachetypes.Tag | firehosetypes.Tag | iamtypes.Tag | kinesistypes.Tag | rdstypes.Tag | route53types.Tag
}

// awsTagsJSON converts any AWS SDK tag slice to a JSON-encoded {key:value} map.
// Returns nil when the slice is empty.
func awsTagsJSON[T awsTag](tags []T) *string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		var k, v *string
		switch tt := any(t).(type) {
		case acmtypes.Tag:
			k, v = tt.Key, tt.Value
		case cfntypes.Tag:
			k, v = tt.Key, tt.Value
		case cloudfronttypes.Tag:
			k, v = tt.Key, tt.Value
		case ec2types.Tag:
			k, v = tt.Key, tt.Value
		case ecrtypes.Tag:
			k, v = tt.Key, tt.Value
		case ecstypes.Tag:
			k, v = tt.Key, tt.Value
		case elasticachetypes.Tag:
			k, v = tt.Key, tt.Value
		case firehosetypes.Tag:
			k, v = tt.Key, tt.Value
		case iamtypes.Tag:
			k, v = tt.Key, tt.Value
		case kinesistypes.Tag:
			k, v = tt.Key, tt.Value
		case rdstypes.Tag:
			k, v = tt.Key, tt.Value
		case route53types.Tag:
			k, v = tt.Key, tt.Value
		}
		if k != nil && v != nil {
			m[*k] = *v
		}
	}
	s := mustJSON(m)
	return &s
}

// mapTagsJSON converts a map[string]string tag map to a JSON-encoded {key:value}
// string pointer. Used by services whose SDK returns tags as a plain map rather
// than a slice of typed Tag structs. Returns nil when the map is empty.
func mapTagsJSON(tags map[string]string) *string {
	if len(tags) == 0 {
		return nil
	}
	s := mustJSON(tags)
	return &s
}
