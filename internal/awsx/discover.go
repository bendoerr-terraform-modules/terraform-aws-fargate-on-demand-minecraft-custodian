package awsx

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/platform"
)

const (
	attachmentTypeENI = "ElasticNetworkInterface"
	detailENIID       = "networkInterfaceId"
)

// TaskMetadata reads the running task's metadata.
type TaskMetadata interface {
	Task(ctx context.Context) (platform.Task, error)
}

// EC2API is the subset of *ec2.Client the custodian uses.
type EC2API interface {
	DescribeNetworkInterfaces(
		ctx context.Context, in *ec2.DescribeNetworkInterfacesInput, optFns ...func(*ec2.Options),
	) (*ec2.DescribeNetworkInterfacesOutput, error)
}

// Discoverer resolves the running task's ARN and public IP (spec §4 step 2).
type Discoverer struct {
	meta    TaskMetadata
	ecs     ECSAPI
	ec2     EC2API
	cluster string
}

// NewDiscoverer returns a Discoverer.
func NewDiscoverer(meta TaskMetadata, ecsClient ECSAPI, ec2Client EC2API, cluster string) *Discoverer {
	return &Discoverer{meta: meta, ecs: ecsClient, ec2: ec2Client, cluster: cluster}
}

// Discover returns the task ARN and its public IPv4.
func (d *Discoverer) Discover(ctx context.Context) (lifecycle.Self, error) {
	task, err := d.meta.Task(ctx)
	if err != nil {
		return lifecycle.Self{}, fmt.Errorf("discover: %w", err)
	}
	eni, err := d.eniID(ctx, task.TaskARN)
	if err != nil {
		return lifecycle.Self{}, err
	}
	ip, err := d.publicIP(ctx, eni)
	if err != nil {
		return lifecycle.Self{}, err
	}
	return lifecycle.Self{TaskARN: task.TaskARN, PublicIP: ip}, nil
}

func (d *Discoverer) eniID(ctx context.Context, taskARN string) (string, error) {
	out, err := d.ecs.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(d.cluster),
		Tasks:   []string{taskARN},
	})
	if err != nil {
		return "", fmt.Errorf("discover: describe task %s: %w", taskARN, err)
	}
	if len(out.Tasks) == 0 {
		return "", fmt.Errorf("discover: task %s not found in cluster %s", taskARN, d.cluster)
	}
	for _, att := range out.Tasks[0].Attachments {
		if aws.ToString(att.Type) != attachmentTypeENI {
			continue
		}
		for _, kv := range att.Details {
			if aws.ToString(kv.Name) == detailENIID && aws.ToString(kv.Value) != "" {
				return aws.ToString(kv.Value), nil
			}
		}
	}
	return "", fmt.Errorf("discover: task %s has no network interface attachment", taskARN)
}

func (d *Discoverer) publicIP(ctx context.Context, eni string) (string, error) {
	out, err := d.ec2.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eni},
	})
	if err != nil {
		return "", fmt.Errorf("discover: describe network interface %s: %w", eni, err)
	}
	if len(out.NetworkInterfaces) == 0 || out.NetworkInterfaces[0].Association == nil ||
		aws.ToString(out.NetworkInterfaces[0].Association.PublicIp) == "" {
		return "", fmt.Errorf("discover: network interface %s has no public IP (is assign_public_ip enabled?)", eni)
	}
	return aws.ToString(out.NetworkInterfaces[0].Association.PublicIp), nil
}
