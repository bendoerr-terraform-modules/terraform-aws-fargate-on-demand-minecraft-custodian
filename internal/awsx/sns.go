package awsx

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/lifecycle"
)

// SNSAPI is the subset of *sns.Client the custodian uses.
type SNSAPI interface {
	Publish(ctx context.Context, in *sns.PublishInput, optFns ...func(*sns.Options)) (*sns.PublishOutput, error)
}

// Notifier publishes lifecycle events in the format the notice Lambdas expect (spec §3.1).
type Notifier struct {
	client   SNSAPI
	topicARN string
	cluster  string
	service  string
}

// message field order is part of the contract.
type message struct {
	Cluster string `json:"Cluster"`
	Service string `json:"Service"`
	Event   string `json:"Event"`
	Topic   string `json:"Topic"`
}

// NewNotifier returns a Notifier; an empty topicARN disables publishing.
func NewNotifier(client SNSAPI, topicARN, cluster, service string) *Notifier {
	return &Notifier{client: client, topicARN: topicARN, cluster: cluster, service: service}
}

// Notify publishes event, or does nothing without a topic.
func (n *Notifier) Notify(ctx context.Context, event lifecycle.Event) error {
	if n.topicARN == "" {
		return nil
	}
	body, err := json.Marshal(message{Cluster: n.cluster, Service: n.service, Event: string(event), Topic: n.topicARN})
	if err != nil {
		return fmt.Errorf("encode %s event: %w", event, err)
	}
	_, err = n.client.Publish(
		ctx,
		&sns.PublishInput{TopicArn: aws.String(n.topicARN), Message: aws.String(string(body))},
	)
	if err != nil {
		return fmt.Errorf("publish %s event: %w", event, err)
	}
	return nil
}
