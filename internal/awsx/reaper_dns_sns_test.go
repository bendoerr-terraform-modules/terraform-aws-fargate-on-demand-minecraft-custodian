package awsx_test

import (
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/awsx"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
)

func TestReaperSetsDesiredCountZero(t *testing.T) {
	f := &fakeECS{}
	if err := awsx.NewReaper(f, "shanecraft", "minecraft").Reap(t.Context()); err != nil {
		t.Fatalf("Reap() error = %v", err)
	}
	if len(f.updateIn) != 1 {
		t.Fatalf("UpdateService calls = %d; want 1", len(f.updateIn))
	}
	in := f.updateIn[0]
	if aws.ToString(in.Cluster) != "shanecraft" || aws.ToString(in.Service) != "minecraft" ||
		in.DesiredCount == nil || *in.DesiredCount != 0 {
		t.Errorf("UpdateService input = cluster %q service %q desired %v; want shanecraft/minecraft/0",
			aws.ToString(in.Cluster), aws.ToString(in.Service), in.DesiredCount)
	}
}

func TestReaperWrapsError(t *testing.T) {
	boom := errors.New("throttled")
	err := awsx.NewReaper(&fakeECS{updateErr: boom}, "c", "s").Reap(t.Context())
	if !errors.Is(err, boom) {
		t.Errorf("Reap() error = %v; want it to wrap %v", err, boom)
	}
}

func TestDNSUpsertsARecord(t *testing.T) {
	f := &fakeRoute53{}
	if err := awsx.NewDNS(f, "Z0123", "mc.example.com", 30).Upsert(t.Context(), "203.0.113.10"); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if len(f.in) != 1 || len(f.in[0].ChangeBatch.Changes) != 1 {
		t.Fatalf("ChangeResourceRecordSets input = %+v; want one call with one change", f.in)
	}
	in := f.in[0]
	ch := in.ChangeBatch.Changes[0]
	rrs := ch.ResourceRecordSet
	if aws.ToString(in.HostedZoneId) != "Z0123" || ch.Action != r53types.ChangeActionUpsert ||
		aws.ToString(rrs.Name) != "mc.example.com" || rrs.Type != r53types.RRTypeA ||
		aws.ToInt64(rrs.TTL) != 30 || len(rrs.ResourceRecords) != 1 ||
		aws.ToString(rrs.ResourceRecords[0].Value) != "203.0.113.10" {
		t.Errorf("unexpected change: zone %q action %s name %q type %s ttl %d records %+v",
			aws.ToString(in.HostedZoneId), ch.Action, aws.ToString(rrs.Name), rrs.Type, aws.ToInt64(rrs.TTL),
			rrs.ResourceRecords)
	}
}

func TestDNSWrapsError(t *testing.T) {
	boom := errors.New("denied")
	if err := awsx.NewDNS(&fakeRoute53{err: boom}, "Z", "r", 30).Upsert(t.Context(), "1.2.3.4"); !errors.Is(err, boom) {
		t.Errorf("Upsert() error = %v; want it to wrap %v", err, boom)
	}
}

func TestNotifierMessageContract(t *testing.T) {
	f := &fakeSNS{}
	const topic = "arn:aws:sns:us-east-1:123456789012:events"
	n := awsx.NewNotifier(f, topic, "shanecraft", "minecraft")
	if err := n.Notify(t.Context(), lifecycle.EventStart); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(f.in) != 1 {
		t.Fatalf("Publish calls = %d; want 1", len(f.in))
	}
	const want = `{"Cluster":"shanecraft","Service":"minecraft","Event":"start",` +
		`"Topic":"arn:aws:sns:us-east-1:123456789012:events"}`
	if got := aws.ToString(f.in[0].Message); got != want {
		t.Errorf("message = %s; want %s", got, want)
	}
	if aws.ToString(f.in[0].TopicArn) != topic {
		t.Errorf("topic = %q; want %q", aws.ToString(f.in[0].TopicArn), topic)
	}
}

func TestNotifierWithoutTopicIsNoop(t *testing.T) {
	f := &fakeSNS{}
	if err := awsx.NewNotifier(f, "", "c", "s").Notify(t.Context(), lifecycle.EventStop); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	if len(f.in) != 0 {
		t.Errorf("Publish calls = %d; want 0 without a topic", len(f.in))
	}
}

func TestNotifierWrapsError(t *testing.T) {
	boom := errors.New("kms")
	err := awsx.NewNotifier(&fakeSNS{err: boom}, "arn:t", "c", "s").Notify(t.Context(), lifecycle.EventStop)
	if !errors.Is(err, boom) {
		t.Errorf("Notify() error = %v; want it to wrap %v", err, boom)
	}
}
