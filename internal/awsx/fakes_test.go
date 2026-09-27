package awsx_test

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

type fakeECS struct {
	updateIn   []*ecs.UpdateServiceInput
	updateErr  error
	listIn     []*ecs.ListTasksInput
	list       func(*ecs.ListTasksInput) (*ecs.ListTasksOutput, error)
	describeIn []*ecs.DescribeTasksInput
	describe   func(*ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error)
}

func (f *fakeECS) UpdateService(
	_ context.Context, in *ecs.UpdateServiceInput, _ ...func(*ecs.Options),
) (*ecs.UpdateServiceOutput, error) {
	f.updateIn = append(f.updateIn, in)
	return &ecs.UpdateServiceOutput{}, f.updateErr
}

func (f *fakeECS) ListTasks(
	_ context.Context, in *ecs.ListTasksInput, _ ...func(*ecs.Options),
) (*ecs.ListTasksOutput, error) {
	f.listIn = append(f.listIn, in)
	return f.list(in)
}

func (f *fakeECS) DescribeTasks(
	_ context.Context, in *ecs.DescribeTasksInput, _ ...func(*ecs.Options),
) (*ecs.DescribeTasksOutput, error) {
	f.describeIn = append(f.describeIn, in)
	return f.describe(in)
}

type fakeEC2 struct {
	in  []*ec2.DescribeNetworkInterfacesInput
	out *ec2.DescribeNetworkInterfacesOutput
	err error
}

func (f *fakeEC2) DescribeNetworkInterfaces(
	_ context.Context, in *ec2.DescribeNetworkInterfacesInput, _ ...func(*ec2.Options),
) (*ec2.DescribeNetworkInterfacesOutput, error) {
	f.in = append(f.in, in)
	return f.out, f.err
}

type fakeRoute53 struct {
	in  []*route53.ChangeResourceRecordSetsInput
	err error
}

func (f *fakeRoute53) ChangeResourceRecordSets(
	_ context.Context, in *route53.ChangeResourceRecordSetsInput, _ ...func(*route53.Options),
) (*route53.ChangeResourceRecordSetsOutput, error) {
	f.in = append(f.in, in)
	return &route53.ChangeResourceRecordSetsOutput{}, f.err
}

type fakeSNS struct {
	in  []*sns.PublishInput
	err error
}

func (f *fakeSNS) Publish(_ context.Context, in *sns.PublishInput, _ ...func(*sns.Options)) (*sns.PublishOutput, error) {
	f.in = append(f.in, in)
	return &sns.PublishOutput{}, f.err
}
