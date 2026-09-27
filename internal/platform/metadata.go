// Package platform reads the ECS task metadata endpoint v4.
package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Task is the subset of GET ${ECS_CONTAINER_METADATA_URI_V4}/task the custodian uses.
type Task struct {
	TaskARN string `json:"TaskARN"`
	Cluster string `json:"Cluster"`
}

// Client reads task metadata.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New returns a Client for baseURL (the value of ECS_CONTAINER_METADATA_URI_V4).
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, httpClient: httpClient}
}

// Task fetches the running task's metadata.
func (c *Client) Task(ctx context.Context) (Task, error) {
	if c.baseURL == "" {
		return Task{}, errors.New("platform: ECS_CONTAINER_METADATA_URI_V4 is not set")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/task", nil)
	if err != nil {
		return Task{}, fmt.Errorf("platform: build request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Task{}, fmt.Errorf("platform: get task metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Task{}, fmt.Errorf("platform: task metadata status %d", resp.StatusCode)
	}
	var task Task
	if err = json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return Task{}, fmt.Errorf("platform: decode task metadata: %w", err)
	}
	if task.TaskARN == "" {
		return Task{}, errors.New("platform: task metadata has no TaskARN")
	}
	return task, nil
}
