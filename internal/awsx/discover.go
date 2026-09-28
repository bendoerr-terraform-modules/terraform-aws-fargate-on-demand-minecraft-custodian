package awsx

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/platform"
)

const (
	attachmentTypeENI  = "ElasticNetworkInterface"
	detailENIID        = "networkInterfaceId"
	serviceGroupPrefix = "service:"
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

// Discoverer resolves the running task's ARN and public IP (spec §4 step 2), and verifies that the
// configured cluster and service are the ones actually running this task.
type Discoverer struct {
	meta    TaskMetadata
	ecs     ECSAPI
	ec2     EC2API
	cluster string
	service string
}

// NewDiscoverer returns a Discoverer for the configured cluster (name or ARN) and service name.
func NewDiscoverer(meta TaskMetadata, ecsClient ECSAPI, ec2Client EC2API, cluster, service string) *Discoverer {
	return &Discoverer{meta: meta, ecs: ecsClient, ec2: ec2Client, cluster: cluster, service: service}
}

// Discover returns the task ARN and its public IPv4.
func (d *Discoverer) Discover(ctx context.Context) (lifecycle.Self, error) {
	task, err := d.meta.Task(ctx)
	if err != nil {
		return lifecycle.Self{}, fmt.Errorf("discover: %w", err)
	}
	if !sameCluster(task.Cluster, d.cluster) {
		return lifecycle.Self{}, fmt.Errorf("discover: %w: task runs in cluster %q, configured %q",
			lifecycle.ErrIdentityMismatch, task.Cluster, d.cluster)
	}
	eni, err := d.eniID(ctx, task)
	if err != nil {
		return lifecycle.Self{}, err
	}
	ip, err := d.publicIP(ctx, eni)
	if err != nil {
		return lifecycle.Self{}, err
	}
	return lifecycle.Self{TaskARN: task.TaskARN, PublicIP: ip}, nil
}

// sameCluster reports whether configured (a cluster name or ARN) names the metadata cluster ARN.
func sameCluster(metadataARN, configured string) bool {
	return metadataARN == configured || strings.HasSuffix(metadataARN, ":cluster/"+configured)
}

// eniID describes the task in the cluster it actually runs in, checks that it belongs to the configured
// service, and returns its ENI ID.
func (d *Discoverer) eniID(ctx context.Context, task platform.Task) (string, error) {
	out, err := d.ecs.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(task.Cluster),
		Tasks:   []string{task.TaskARN},
	})
	if err != nil {
		return "", fmt.Errorf("discover: describe task %s: %w", task.TaskARN, err)
	}
	if len(out.Tasks) == 0 {
		return "", fmt.Errorf("discover: task %s not found in cluster %s", task.TaskARN, task.Cluster)
	}
	if group := aws.ToString(out.Tasks[0].Group); group != serviceGroupPrefix+d.service {
		return "", fmt.Errorf("discover: %w: task group is %q, configured service %q",
			lifecycle.ErrIdentityMismatch, group, d.service)
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
	return "", fmt.Errorf("discover: task %s has no network interface attachment", task.TaskARN)
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
