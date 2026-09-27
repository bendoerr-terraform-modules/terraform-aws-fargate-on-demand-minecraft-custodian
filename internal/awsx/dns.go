package awsx

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// Route53API is the subset of *route53.Client the custodian uses.
type Route53API interface {
	ChangeResourceRecordSets(
		ctx context.Context, in *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options),
	) (*route53.ChangeResourceRecordSetsOutput, error)
}

// DNS upserts the server's A record.
type DNS struct {
	client Route53API
	zoneID string
	record string
	ttl    int64
}

// NewDNS returns a DNS adapter for one record.
func NewDNS(client Route53API, zoneID, record string, ttl int64) *DNS {
	return &DNS{client: client, zoneID: zoneID, record: record, ttl: ttl}
}

// Upsert points the record at ip.
func (d *DNS) Upsert(ctx context.Context, ip string) error {
	_, err := d.client.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(d.zoneID),
		ChangeBatch: &r53types.ChangeBatch{
			Comment: aws.String("minecraft custodian"),
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name:            aws.String(d.record),
					Type:            r53types.RRTypeA,
					TTL:             aws.Int64(d.ttl),
					ResourceRecords: []r53types.ResourceRecord{{Value: aws.String(ip)}},
				},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("upsert %s A %s: %w", d.record, ip, err)
	}
	return nil
}
