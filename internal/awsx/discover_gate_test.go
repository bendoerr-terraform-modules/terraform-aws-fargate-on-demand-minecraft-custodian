package awsx_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/awsx"
	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/platform"
)

const (
	selfARN  = "arn:aws:ecs:us-east-1:1:task/shanecraft/self"
	otherARN = "arn:aws:ecs:us-east-1:1:task/shanecraft/other"
)

type fakeMeta struct {
	task platform.Task
	err  error
}

func (f fakeMeta) Task(context.Context) (platform.Task, error) { return f.task, f.err }

func taskWithENI(eni string) ecstypes.Task {
	return ecstypes.Task{
		TaskArn: aws.String(selfARN),
		Attachments: []ecstypes.Attachment{{
			Type: aws.String("ElasticNetworkInterface"),
			Details: []ecstypes.KeyValuePair{
				{Name: aws.String("subnetId"), Value: aws.String("subnet-1")},
				{Name: aws.String("networkInterfaceId"), Value: aws.String(eni)},
			},
		}},
	}
}

func eniWithIP(ip string) *ec2.DescribeNetworkInterfacesOutput {
	ni := ec2types.NetworkInterface{}
	if ip != "" {
		ni.Association = &ec2types.NetworkInterfaceAssociation{PublicIp: aws.String(ip)}
	}
	return &ec2.DescribeNetworkInterfacesOutput{NetworkInterfaces: []ec2types.NetworkInterface{ni}}
}

func describeReturning(tasks ...ecstypes.Task) func(*ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error) {
	return func(*ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error) {
		return &ecs.DescribeTasksOutput{Tasks: tasks}, nil
	}
}

func TestDiscover(t *testing.T) {
	e := &fakeECS{describe: describeReturning(taskWithENI("eni-123"))}
	c2 := &fakeEC2{out: eniWithIP("203.0.113.10")}
	d := awsx.NewDiscoverer(fakeMeta{task: platform.Task{TaskARN: selfARN}}, e, c2, "shanecraft")
	self, err := d.Discover(t.Context())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if self.TaskARN != selfARN || self.PublicIP != "203.0.113.10" {
		t.Errorf("Discover() = %+v", self)
	}
	if aws.ToString(e.describeIn[0].Cluster) != "shanecraft" || !slices.Equal(e.describeIn[0].Tasks, []string{selfARN}) {
		t.Errorf("DescribeTasks input = %+v", e.describeIn[0])
	}
	if !slices.Equal(c2.in[0].NetworkInterfaceIds, []string{"eni-123"}) {
		t.Errorf("DescribeNetworkInterfaces input = %+v", c2.in[0])
	}
}

func TestDiscoverErrors(t *testing.T) {
	noENI := ecstypes.Task{TaskArn: aws.String(selfARN)}
	tests := []struct {
		name    string
		meta    fakeMeta
		ecs     *fakeECS
		ec2     *fakeEC2
		wantMsg string
	}{
		{"metadata fails", fakeMeta{err: errors.New("no endpoint")},
			&fakeECS{}, &fakeEC2{}, "no endpoint"},
		{"task not found", fakeMeta{task: platform.Task{TaskARN: selfARN}},
			&fakeECS{describe: describeReturning()}, &fakeEC2{}, "not found"},
		{"no eni attachment", fakeMeta{task: platform.Task{TaskARN: selfARN}},
			&fakeECS{describe: describeReturning(noENI)}, &fakeEC2{}, "network interface"},
		{"no public ip", fakeMeta{task: platform.Task{TaskARN: selfARN}},
			&fakeECS{describe: describeReturning(taskWithENI("eni-1"))}, &fakeEC2{out: eniWithIP("")},
			"assign_public_ip"},
		{"ec2 fails", fakeMeta{task: platform.Task{TaskARN: selfARN}},
			&fakeECS{describe: describeReturning(taskWithENI("eni-1"))}, &fakeEC2{err: errors.New("denied")},
			"denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := awsx.NewDiscoverer(tt.meta, tt.ecs, tt.ec2, "shanecraft").Discover(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("Discover() error = %v; want it to mention %q", err, tt.wantMsg)
			}
		})
	}
}

func task(arn, lastStatus string) ecstypes.Task {
	return ecstypes.Task{TaskArn: aws.String(arn), LastStatus: aws.String(lastStatus)}
}

// listing returns the given ARNs for RUNNING and STOPPED desired-status queries.
func listing(running, stopped []string) func(*ecs.ListTasksInput) (*ecs.ListTasksOutput, error) {
	return func(in *ecs.ListTasksInput) (*ecs.ListTasksOutput, error) {
		if in.DesiredStatus == ecstypes.DesiredStatusStopped {
			return &ecs.ListTasksOutput{TaskArns: stopped}, nil
		}
		return &ecs.ListTasksOutput{TaskArns: running}, nil
	}
}

func TestGateNoOtherTasks(t *testing.T) {
	e := &fakeECS{list: listing([]string{selfARN}, nil)}
	blocking, err := awsx.NewGate(e, "shanecraft", "minecraft").Blocking(t.Context(), selfARN)
	if err != nil || len(blocking) != 0 {
		t.Fatalf("Blocking() = %v, %v; want none", blocking, err)
	}
	if len(e.describeIn) != 0 {
		t.Errorf("DescribeTasks called %d times; want 0 when only self is listed", len(e.describeIn))
	}
	var statuses []ecstypes.DesiredStatus
	for _, in := range e.listIn {
		statuses = append(statuses, in.DesiredStatus)
		if aws.ToString(in.Cluster) != "shanecraft" || aws.ToString(in.ServiceName) != "minecraft" {
			t.Errorf("ListTasks input = %+v", in)
		}
	}
	want := []ecstypes.DesiredStatus{ecstypes.DesiredStatusRunning, ecstypes.DesiredStatusStopped}
	if !slices.Equal(statuses, want) {
		t.Errorf("ListTasks desired statuses = %v; want %v", statuses, want)
	}
}

func TestGateBlockingStatuses(t *testing.T) {
	for _, status := range []string{"PROVISIONING", "PENDING", "ACTIVATING", "RUNNING", "DEACTIVATING", "STOPPING"} {
		e := &fakeECS{list: listing([]string{selfARN}, []string{otherARN}), describe: describeReturning(task(otherARN, status))}
		blocking, err := awsx.NewGate(e, "c", "s").Blocking(t.Context(), selfARN)
		if err != nil || !slices.Equal(blocking, []string{otherARN}) {
			t.Errorf("status %s: Blocking() = %v, %v; want [%s]", status, blocking, err, otherARN)
		}
	}
	for _, status := range []string{"DEPROVISIONING", "STOPPED"} {
		e := &fakeECS{list: listing(nil, []string{otherARN}), describe: describeReturning(task(otherARN, status))}
		blocking, err := awsx.NewGate(e, "c", "s").Blocking(t.Context(), selfARN)
		if err != nil || len(blocking) != 0 {
			t.Errorf("status %s: Blocking() = %v, %v; want none", status, blocking, err)
		}
	}
}

func TestGateDeduplicatesAndPaginates(t *testing.T) {
	e := &fakeECS{
		list: func(in *ecs.ListTasksInput) (*ecs.ListTasksOutput, error) {
			if in.DesiredStatus == ecstypes.DesiredStatusStopped {
				return &ecs.ListTasksOutput{TaskArns: []string{otherARN}}, nil
			}
			if in.NextToken == nil {
				return &ecs.ListTasksOutput{TaskArns: []string{selfARN}, NextToken: aws.String("page2")}, nil
			}
			return &ecs.ListTasksOutput{TaskArns: []string{otherARN}}, nil
		},
		describe: describeReturning(task(otherARN, "STOPPING")),
	}
	if _, err := awsx.NewGate(e, "c", "s").Blocking(t.Context(), selfARN); err != nil {
		t.Fatalf("Blocking() error = %v", err)
	}
	if len(e.describeIn) != 1 || !slices.Equal(e.describeIn[0].Tasks, []string{otherARN}) {
		t.Errorf("DescribeTasks inputs = %+v; want one call for [%s]", e.describeIn, otherARN)
	}
}

// Review Focus 5: a listed task that DescribeTasks no longer finds must not block.
func TestGateIgnoresVanishedTasks(t *testing.T) {
	e := &fakeECS{
		list: listing(nil, []string{otherARN}),
		describe: func(*ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error) {
			return &ecs.DescribeTasksOutput{Failures: []ecstypes.Failure{{Arn: aws.String(otherARN), Reason: aws.String("MISSING")}}}, nil
		},
	}
	blocking, err := awsx.NewGate(e, "c", "s").Blocking(t.Context(), selfARN)
	if err != nil || len(blocking) != 0 {
		t.Errorf("Blocking() = %v, %v; want none", blocking, err)
	}
}

func TestGateErrors(t *testing.T) {
	boom := errors.New("throttled")
	listFails := &fakeECS{list: func(*ecs.ListTasksInput) (*ecs.ListTasksOutput, error) { return nil, boom }}
	if _, err := awsx.NewGate(listFails, "c", "s").Blocking(t.Context(), selfARN); !errors.Is(err, boom) {
		t.Errorf("list failure: error = %v; want %v", err, boom)
	}
	describeFails := &fakeECS{
		list:     listing([]string{otherARN}, nil),
		describe: func(*ecs.DescribeTasksInput) (*ecs.DescribeTasksOutput, error) { return nil, boom },
	}
	if _, err := awsx.NewGate(describeFails, "c", "s").Blocking(t.Context(), selfARN); !errors.Is(err, boom) {
		t.Errorf("describe failure: error = %v; want %v", err, boom)
	}
}
