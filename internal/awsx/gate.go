package awsx

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

const describeTasksMax = 100

// Gate finds other tasks of the service that may still hold the world (spec §4 step 3).
type Gate struct {
	client  ECSAPI
	cluster string
	service string
}

// NewGate returns a Gate for cluster/service.
func NewGate(client ECSAPI, cluster, service string) *Gate {
	return &Gate{client: client, cluster: cluster, service: service}
}

// Blocking returns the ARNs of other tasks whose containers may still be running.
func (g *Gate) Blocking(ctx context.Context, selfTaskARN string) ([]string, error) {
	var arns []string
	for _, desired := range []ecstypes.DesiredStatus{ecstypes.DesiredStatusRunning, ecstypes.DesiredStatusStopped} {
		pages := ecs.NewListTasksPaginator(g.client, &ecs.ListTasksInput{
			Cluster:       aws.String(g.cluster),
			ServiceName:   aws.String(g.service),
			DesiredStatus: desired,
		})
		for pages.HasMorePages() {
			page, err := pages.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("gate: list %s tasks: %w", desired, err)
			}
			arns = append(arns, page.TaskArns...)
		}
	}

	arns = slices.DeleteFunc(arns, func(arn string) bool { return arn == selfTaskARN })
	slices.Sort(arns)
	arns = slices.Compact(arns)
	if len(arns) == 0 {
		return nil, nil
	}

	var blocking []string
	for chunk := range slices.Chunk(arns, describeTasksMax) {
		out, err := g.client.DescribeTasks(ctx, &ecs.DescribeTasksInput{Cluster: aws.String(g.cluster), Tasks: chunk})
		if err != nil {
			return nil, fmt.Errorf("gate: describe tasks: %w", err)
		}
		for _, f := range out.Failures {
			// MISSING means the task is simply gone (not blocking); any other reason
			// (e.g. throttling, access denied) must fail closed so the caller retries
			// instead of treating a possibly-still-running task as vanished.
			if aws.ToString(f.Reason) != "MISSING" {
				return nil, fmt.Errorf("gate: describe task %s: %s", aws.ToString(f.Arn), aws.ToString(f.Reason))
			}
		}
		for _, t := range out.Tasks {
			if holdsWorld(aws.ToString(t.LastStatus)) {
				blocking = append(blocking, aws.ToString(t.TaskArn))
			}
		}
	}
	return blocking, nil
}

// holdsWorld is false only once a task's containers have exited; unknown statuses block, to be safe.
func holdsWorld(lastStatus string) bool {
	switch lastStatus {
	case "DEPROVISIONING", "STOPPED":
		return false
	default:
		return true
	}
}
