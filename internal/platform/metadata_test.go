package platform_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bendoerr-terraform-modules/terraform-aws-fargate-on-demand-minecraft-custodian/internal/platform"
)

func server(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/task" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestTask(t *testing.T) {
	url := server(t, http.StatusOK, `{"Cluster":"arn:aws:ecs:us-east-1:1:cluster/shanecraft",`+
		`"TaskARN":"arn:aws:ecs:us-east-1:1:task/shanecraft/abc","Family":"minecraft"}`)
	task, err := platform.New(url, http.DefaultClient).Task(t.Context())
	if err != nil {
		t.Fatalf("Task() error = %v", err)
	}
	if task.TaskARN != "arn:aws:ecs:us-east-1:1:task/shanecraft/abc" ||
		task.Cluster != "arn:aws:ecs:us-east-1:1:cluster/shanecraft" {
		t.Errorf("Task() = %+v", task)
	}
}

func TestTaskErrors(t *testing.T) {
	tests := map[string]string{
		"server error":    server(t, http.StatusInternalServerError, `oops`),
		"bad json":        server(t, http.StatusOK, `{"TaskARN":`),
		"missing taskarn": server(t, http.StatusOK, `{"Cluster":"c"}`),
		"unset base url":  "",
	}
	for name, url := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := platform.New(url, http.DefaultClient).Task(t.Context()); err == nil {
				t.Error("Task() error = nil; want error")
			}
		})
	}
}
