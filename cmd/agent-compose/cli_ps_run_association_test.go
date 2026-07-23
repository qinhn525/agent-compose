package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func TestSchedulerRunIsNewer(t *testing.T) {
	tests := []struct {
		name         string
		schedulerRun composeSchedulerRunItem
		projectRun   *agentcomposev2.RunSummary
		want         bool
	}{
		{name: "missing scheduler run", projectRun: &agentcomposev2.RunSummary{RunId: "project-run"}},
		{name: "no project run", schedulerRun: composeSchedulerRunItem{RunID: "scheduler-run"}, want: true},
		{
			name:         "scheduler run is newer",
			schedulerRun: composeSchedulerRunItem{RunID: "scheduler-run", CompletedAt: "2026-07-15T12:00:00.1Z"},
			projectRun:   &agentcomposev2.RunSummary{RunId: "project-run", CompletedAt: "2026-07-15T12:00:00Z"},
			want:         true,
		},
		{
			name:         "project run is newer",
			schedulerRun: composeSchedulerRunItem{RunID: "scheduler-run", CompletedAt: "2026-07-15T11:00:00Z"},
			projectRun:   &agentcomposev2.RunSummary{RunId: "project-run", CreatedAt: "2026-07-15T10:00:00Z", CompletedAt: "2026-07-15T12:00:00Z"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := schedulerRunIsNewer(tt.schedulerRun, tt.projectRun); got != tt.want {
				t.Fatalf("schedulerRunIsNewer() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestLegacySchedulerSandboxBelongsToProject(t *testing.T) {
	project := testCLIProject("project-1", "project-one", "/work/agent-compose.yml")
	loaderID, err := domain.StableManagedLoaderID("project-1", "reviewer", "")
	if err != nil {
		t.Fatalf("build managed loader id: %v", err)
	}
	legacyTags := map[string]string{
		"origin":    "loader",
		"loader_id": loaderID,
	}
	legacySandbox := &agentcomposev2.Sandbox{Tags: []*agentcomposev2.SandboxTag{
		{Name: "origin", Value: "loader"},
		{Name: "loader_id", Value: loaderID},
	}}
	if !composePSSessionBelongsToProject(legacySandbox, project, nil) {
		t.Fatal("ps should include a legacy managed scheduler sandbox in its project")
	}
	if !legacySchedulerSandboxBelongsToProject(legacyTags, project) {
		t.Fatal("legacy managed scheduler sandbox should belong to its project")
	}
	if agentName := legacySchedulerAgentForProject(legacyTags, project); agentName != "reviewer" {
		t.Fatalf("legacy scheduler agent = %q, want reviewer", agentName)
	}
	if legacySchedulerSandboxBelongsToProject(legacyTags, testCLIProject("project-2", "project-two", "/other/agent-compose.yml")) {
		t.Fatal("legacy managed scheduler sandbox should not belong to another project")
	}
	if legacySchedulerSandboxBelongsToProject(map[string]string{"origin": "loader", "loader_id": "standalone-loader"}, project) {
		t.Fatal("standalone loader sandbox should not belong to the project")
	}
	if legacySchedulerSandboxBelongsToProject(map[string]string{"origin": "loader", "loader_id": loaderID, "project_id": "project-2"}, project) {
		t.Fatal("explicit project ownership should not be overridden by legacy inference")
	}
}

func TestIntegrationCLIPSDisplaysLatestSchedulerRunForSandbox(t *testing.T) {
	composePath := writeComposeFile(t, t.TempDir(), `
name: cli-ps-scheduler-run
agents:
  reviewer:
    provider: codex
    scheduler:
      triggers:
        - name: nightly
          cron: "0 2 * * *"
`)
	const sandboxID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	projectRunID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	schedulerRunID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	staleTagRunID := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	projectID := ""
	server := newComposeServiceStubServer(t, composeServiceStubs{
		project: projectServiceStub{
			getProject: func(_ context.Context, req *connect.Request[agentcomposev2.GetProjectRequest]) (*connect.Response[agentcomposev2.GetProjectResponse], error) {
				projectID = req.Msg.GetProject().GetProjectId()
				return connect.NewResponse(&agentcomposev2.GetProjectResponse{Project: testCLIProject(projectID, "cli-ps-scheduler-run", composePath)}), nil
			},
			listSchedulerRuns: func(_ context.Context, req *connect.Request[agentcomposev2.ListSchedulerRunsRequest]) (*connect.Response[agentcomposev2.ListSchedulerRunsResponse], error) {
				if req.Msg.GetAgentName() != "reviewer" {
					t.Fatalf("ListSchedulerRuns agent = %q, want reviewer", req.Msg.GetAgentName())
				}
				return connect.NewResponse(&agentcomposev2.ListSchedulerRunsResponse{Runs: []*agentcomposev2.SchedulerRun{{
					RunId: schedulerRunID, AgentName: "reviewer", TriggerId: "nightly", Status: agentcomposev2.SchedulerRunStatus_SCHEDULER_RUN_STATUS_SUCCEEDED,
					SandboxIds: []string{sandboxID}, CompletedAt: timestamppb.New(time.Date(2026, 7, 15, 12, 1, 0, 0, time.UTC)),
				}}}), nil
			},
		},
		run: runServiceStub{listRuns: func(context.Context, *connect.Request[agentcomposev2.ListRunsRequest]) (*connect.Response[agentcomposev2.ListRunsResponse], error) {
			return connect.NewResponse(&agentcomposev2.ListRunsResponse{Runs: []*agentcomposev2.RunSummary{{
				RunId: projectRunID, ProjectId: projectID, AgentName: "reviewer", SandboxId: sandboxID, CompletedAt: "2026-07-15T11:00:00Z",
			}}}), nil
		}},
		sandbox: sandboxServiceStub{listSandboxes: func(context.Context, *connect.Request[agentcomposev2.ListSandboxesRequest]) (*connect.Response[agentcomposev2.ListSandboxesResponse], error) {
			return connect.NewResponse(&agentcomposev2.ListSandboxesResponse{Sandboxes: []*agentcomposev2.Sandbox{{
				SandboxId: sandboxID,
				Status:    "running",
				Tags: []*agentcomposev2.SandboxTag{
					{Name: "origin", Value: "scheduler"},
					{Name: "project_id", Value: projectID},
					{Name: "agent", Value: "reviewer"},
					{Name: "scheduler_run_id", Value: staleTagRunID},
				},
			}}}), nil
		}},
	})
	defer server.Close()

	stdout, stderr, _, exitCode := executeCLICommand("ps", "--host", server.URL, "--file", composePath)
	if exitCode != 0 || stderr != "" {
		t.Fatalf("ps code/stdout/stderr = %d / %q / %q", exitCode, stdout, stderr)
	}
	if !strings.Contains(stdout, schedulerRunID[:12]) || strings.Contains(stdout, projectRunID[:12]) || strings.Contains(stdout, staleTagRunID[:12]) {
		t.Fatalf("ps output = %q, want latest scheduler run %s", stdout, schedulerRunID[:12])
	}
}

func TestLatestSchedulerRunsBySandboxReadsPastFiveHundredRuns(t *testing.T) {
	project := testCLIProject("project-1", "project-one", "/work/agent-compose.yml")
	const sandboxID = "sandbox-from-older-page"
	requests := 0
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		listSchedulerRuns: func(_ context.Context, req *connect.Request[agentcomposev2.ListSchedulerRunsRequest]) (*connect.Response[agentcomposev2.ListSchedulerRunsResponse], error) {
			requests++
			if req.Msg.GetLimit() != schedulerQueryPageSize {
				t.Fatalf("ListSchedulerRuns limit = %d, want %d", req.Msg.GetLimit(), schedulerQueryPageSize)
			}
			switch req.Msg.GetCursor() {
			case "":
				runs := make([]*agentcomposev2.SchedulerRun, 500)
				for i := range runs {
					runs[i] = &agentcomposev2.SchedulerRun{RunId: fmt.Sprintf("recent-%03d", i), TriggerId: "nightly"}
				}
				return connect.NewResponse(&agentcomposev2.ListSchedulerRunsResponse{Runs: runs, NextCursor: "older"}), nil
			case "older":
				return connect.NewResponse(&agentcomposev2.ListSchedulerRunsResponse{Runs: []*agentcomposev2.SchedulerRun{{
					RunId: "older-associated-run", AgentName: "reviewer", TriggerId: "nightly", SandboxIds: []string{sandboxID},
				}}}), nil
			default:
				t.Fatalf("unexpected cursor %q", req.Msg.GetCursor())
				return nil, nil
			}
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)
	clients := cliServiceClients{project: client, projectStream: client}

	got, err := latestSchedulerRunsBySandbox(t.Context(), clients, project, []*agentcomposev2.Sandbox{{
		SandboxId: sandboxID,
		Tags: []*agentcomposev2.SandboxTag{
			{Name: "origin", Value: "scheduler"},
			{Name: "project_id", Value: "project-1"},
			{Name: "agent", Value: "reviewer"},
		},
	}})
	if err != nil {
		t.Fatalf("latest scheduler runs: %v", err)
	}
	if requests != 2 || got[sandboxID].RunID != "older-associated-run" {
		t.Fatalf("requests/result = %d / %#v, want two pages and older-associated-run", requests, got[sandboxID])
	}
}

func TestVisitSchedulerRunsFromAPIRejectsRepeatedCursor(t *testing.T) {
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		listSchedulerRuns: func(_ context.Context, req *connect.Request[agentcomposev2.ListSchedulerRunsRequest]) (*connect.Response[agentcomposev2.ListSchedulerRunsResponse], error) {
			return connect.NewResponse(&agentcomposev2.ListSchedulerRunsResponse{NextCursor: "repeat"}), nil
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	err := visitSchedulerRunsFromAPI(t.Context(), client, "project-1", "reviewer", func(*agentcomposev2.SchedulerRun) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "repeated scheduler run cursor") {
		t.Fatalf("list all scheduler runs error = %v", err)
	}
}

func TestVisitSchedulerRunsFromAPIRejectsExcessivePages(t *testing.T) {
	requests := 0
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		listSchedulerRuns: func(_ context.Context, _ *connect.Request[agentcomposev2.ListSchedulerRunsRequest]) (*connect.Response[agentcomposev2.ListSchedulerRunsResponse], error) {
			requests++
			return connect.NewResponse(&agentcomposev2.ListSchedulerRunsResponse{NextCursor: fmt.Sprintf("page-%d", requests)}), nil
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	err := visitSchedulerRunsFromAPI(t.Context(), client, "project-1", "reviewer", func(*agentcomposev2.SchedulerRun) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "more than 1000 scheduler run pages") {
		t.Fatalf("list excessive scheduler run pages error = %v", err)
	}
	if requests != maxSchedulerQueryPages {
		t.Fatalf("scheduler run page requests = %d, want %d", requests, maxSchedulerQueryPages)
	}
}

func TestLatestSchedulerRunsBySandboxStreamsAndRetainsOnlyTargets(t *testing.T) {
	project := testCLIProject("project-1", "project-one", "/work/agent-compose.yml")
	const sandboxID = "current-sandbox"
	streamRequests := 0
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		streamSchedulerRuns: func(_ context.Context, req *connect.Request[agentcomposev2.StreamSchedulerRunsRequest], stream *connect.ServerStream[agentcomposev2.StreamSchedulerRunsResponse]) error {
			streamRequests++
			if req.Msg.GetAgentName() != "reviewer" || req.Msg.GetBatchSize() != schedulerCLIBatchSize || req.Msg.GetLimit() != uint32(maxSchedulerQueryRuns) {
				t.Fatalf("stream scheduler runs request = %#v", req.Msg)
			}
			if err := stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Runs: []*agentcomposev2.SchedulerRun{
				{RunId: "newest-target", AgentName: "reviewer", TriggerId: "nightly", SandboxIds: []string{sandboxID}, CompletedAt: timestamppb.New(time.Unix(200, 0)), ResultJson: strings.Repeat("x", 1024)},
				{RunId: "irrelevant", AgentName: "reviewer", TriggerId: "nightly", SandboxIds: []string{"historical-sandbox"}, ResultJson: strings.Repeat("x", 1024)},
			}}); err != nil {
				return err
			}
			if err := stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Runs: []*agentcomposev2.SchedulerRun{{
				RunId: "older-target", AgentName: "reviewer", TriggerId: "nightly", SandboxIds: []string{sandboxID}, CompletedAt: timestamppb.New(time.Unix(100, 0)),
			}}}); err != nil {
				return err
			}
			return stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Complete: true})
		},
		listSchedulerRuns: func(context.Context, *connect.Request[agentcomposev2.ListSchedulerRunsRequest]) (*connect.Response[agentcomposev2.ListSchedulerRunsResponse], error) {
			t.Fatal("stream-capable daemon unexpectedly used unary scheduler run fallback")
			return nil, nil
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	got, err := latestSchedulerRunsBySandbox(t.Context(), cliServiceClients{project: client, projectStream: client}, project, []*agentcomposev2.Sandbox{{
		SandboxId: sandboxID,
		Tags: []*agentcomposev2.SandboxTag{
			{Name: "origin", Value: "scheduler"},
			{Name: "project_id", Value: "project-1"},
			{Name: "agent", Value: "reviewer"},
		},
	}})
	if err != nil {
		t.Fatalf("latest streamed scheduler runs: %v", err)
	}
	if streamRequests != 1 || len(got) != 1 || got[sandboxID].RunID != "newest-target" || got[sandboxID].ResultJSON != "" {
		t.Fatalf("stream requests/result = %d / %#v, want one target association", streamRequests, got)
	}
}

func TestVisitSchedulerRuntimeRunsAcceptsSparseFrames(t *testing.T) {
	const sparseFrames = int(maxSchedulerQueryRuns)/schedulerCLIBatchSize + 2
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		streamSchedulerRuns: func(_ context.Context, _ *connect.Request[agentcomposev2.StreamSchedulerRunsRequest], stream *connect.ServerStream[agentcomposev2.StreamSchedulerRunsResponse]) error {
			for i := range sparseFrames {
				if err := stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Runs: []*agentcomposev2.SchedulerRun{{
					RunId: fmt.Sprintf("run-%d", i), TriggerId: "nightly",
				}}}); err != nil {
					return err
				}
			}
			return stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Complete: true})
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	visited := 0
	err := visitSchedulerRuntimeRuns(t.Context(), cliServiceClients{project: client, projectStream: client}, "project-1", "reviewer", func(*agentcomposev2.SchedulerRun) error {
		visited++
		return nil
	})
	if err != nil {
		t.Fatalf("visit sparse scheduler run stream: %v", err)
	}
	if visited != sparseFrames {
		t.Fatalf("visited scheduler runs = %d, want %d", visited, sparseFrames)
	}
}

func TestVisitSchedulerRuntimeRunsRejectsFramesAfterCompletion(t *testing.T) {
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		streamSchedulerRuns: func(_ context.Context, _ *connect.Request[agentcomposev2.StreamSchedulerRunsRequest], stream *connect.ServerStream[agentcomposev2.StreamSchedulerRunsResponse]) error {
			if err := stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Runs: []*agentcomposev2.SchedulerRun{{
				RunId: "before-completion", TriggerId: "nightly",
			}}}); err != nil {
				return err
			}
			if err := stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Complete: true}); err != nil {
				return err
			}
			return stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Runs: []*agentcomposev2.SchedulerRun{{
				RunId: "after-completion", TriggerId: "nightly",
			}}})
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	visited := 0
	err := visitSchedulerRuntimeRuns(t.Context(), cliServiceClients{project: client, projectStream: client}, "project-1", "reviewer", func(*agentcomposev2.SchedulerRun) error {
		visited++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "after stream completion") {
		t.Fatalf("frames after scheduler run completion error = %v", err)
	}
	if visited != 1 {
		t.Fatalf("visited scheduler runs = %d, want 1", visited)
	}
}

func TestLatestSchedulerRunsBySandboxRejectsIncompleteStream(t *testing.T) {
	project := testCLIProject("project-1", "project-one", "/work/agent-compose.yml")
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		streamSchedulerRuns: func(context.Context, *connect.Request[agentcomposev2.StreamSchedulerRunsRequest], *connect.ServerStream[agentcomposev2.StreamSchedulerRunsResponse]) error {
			return nil
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	_, err := latestSchedulerRunsBySandbox(t.Context(), cliServiceClients{project: client, projectStream: client}, project, []*agentcomposev2.Sandbox{{
		SandboxId: "current-sandbox", Tags: []*agentcomposev2.SandboxTag{{Name: "origin", Value: "scheduler"}, {Name: "project_id", Value: "project-1"}, {Name: "agent", Value: "reviewer"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "before completion") {
		t.Fatalf("incomplete scheduler run stream error = %v", err)
	}
}

func TestLatestSchedulerRunsBySandboxRejectsTruncatedStream(t *testing.T) {
	project := testCLIProject("project-1", "project-one", "/work/agent-compose.yml")
	server := newComposeServiceStubServer(t, composeServiceStubs{project: projectServiceStub{
		streamSchedulerRuns: func(_ context.Context, _ *connect.Request[agentcomposev2.StreamSchedulerRunsRequest], stream *connect.ServerStream[agentcomposev2.StreamSchedulerRunsResponse]) error {
			return stream.Send(&agentcomposev2.StreamSchedulerRunsResponse{Complete: true, Truncated: true})
		},
	}})
	t.Cleanup(server.Close)
	client := agentcomposev2connect.NewProjectServiceClient(server.Client(), server.URL)

	_, err := latestSchedulerRunsBySandbox(t.Context(), cliServiceClients{project: client, projectStream: client}, project, []*agentcomposev2.Sandbox{{
		SandboxId: "current-sandbox", Tags: []*agentcomposev2.SandboxTag{{Name: "origin", Value: "scheduler"}, {Name: "project_id", Value: "project-1"}, {Name: "agent", Value: "reviewer"}},
	}})
	if err == nil || !strings.Contains(err.Error(), "more than 500000 scheduler runs") {
		t.Fatalf("truncated scheduler run stream error = %v", err)
	}
}
