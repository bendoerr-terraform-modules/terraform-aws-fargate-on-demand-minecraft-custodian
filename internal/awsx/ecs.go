// Package awsx adapts AWS SDK v2 clients to the lifecycle interfaces.
package awsx

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
)

// ECSAPI is the subset of *ecs.Client the custodian uses.
type ECSAPI interface {
	UpdateService(
		ctx context.Context,
		in *ecs.UpdateServiceInput,
		optFns ...func(*ecs.Options),
	) (*ecs.UpdateServiceOutput, error)
	ListTasks(ctx context.Context, in *ecs.ListTasksInput, optFns ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	DescribeTasks(
		ctx context.Context,
		in *ecs.DescribeTasksInput,
		optFns ...func(*ecs.Options),
	) (*ecs.DescribeTasksOutput, error)
}

// Reaper sets the service's desired count to 0.
type Reaper struct {
	client  ECSAPI
	cluster string
	service string
}

// NewReaper returns a Reaper for cluster/service.
func NewReaper(client ECSAPI, cluster, service string) *Reaper {
	return &Reaper{client: client, cluster: cluster, service: service}
}

// Reap sets desired count to 0.
func (r *Reaper) Reap(ctx context.Context) error {
	_, err := r.client.UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:      aws.String(r.cluster),
		Service:      aws.String(r.service),
		DesiredCount: aws.Int32(0),
	})
	if err != nil {
		return fmt.Errorf("reap %s/%s: %w", r.cluster, r.service, err)
	}
	return nil
}
