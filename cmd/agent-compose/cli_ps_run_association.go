package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
	"agent-compose/proto/agentcompose/v2/agentcomposev2connect"
)

func latestSchedulerRunsBySandbox(ctx context.Context, clients cliServiceClients, project *agentcomposev2.Project, sessions []*agentcomposev2.Sandbox) (map[string]composeSchedulerRunItem, error) {
	projectID := strings.TrimSpace(project.GetSummary().GetProjectId())
	schedulerAgents := make(map[string]bool)
	targetSandboxIDs := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if sandboxID := strings.TrimSpace(session.GetSandboxId()); sandboxID != "" {
			targetSandboxIDs[sandboxID] = struct{}{}
		}
		tags := sessionTagsMap(session.GetTags())
		if tags["origin"] == "scheduler" && tags["project_id"] == projectID && tags["agent"] != "" {
			schedulerAgents[tags["agent"]] = true
			continue
		}
		if agentName := legacySchedulerAgentForProject(tags, project); agentName != "" {
			schedulerAgents[agentName] = true
		}
	}
	result := make(map[string]composeSchedulerRunItem)
	for _, scheduler := range project.GetSchedulers() {
		agentName := strings.TrimSpace(scheduler.GetAgentName())
		if !schedulerAgents[agentName] {
			continue
		}
		err := visitSchedulerRuntimeRuns(ctx, clients, projectID, agentName, func(run *agentcomposev2.SchedulerRun) error {
			var item composeSchedulerRunItem
			mapped := false
			for _, sandboxID := range run.GetSandboxIds() {
				if _, ok := targetSandboxIDs[sandboxID]; !ok {
					continue
				}
				if !mapped {
					item = schedulerRunAssociationItem(run)
					mapped = true
				}
				current, ok := result[sandboxID]
				if !ok || runTimestampAfter(schedulerRunSortTime(item), schedulerRunSortTime(current)) {
					result[sandboxID] = item
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

const maxSchedulerQueryRuns = uint64(maxSchedulerQueryPages) * uint64(schedulerQueryPageSize)

func schedulerRunAssociationItem(run *agentcomposev2.SchedulerRun) composeSchedulerRunItem {
	return composeSchedulerRunItem{
		RunID: run.GetRunId(), AgentName: run.GetAgentName(),
		StartedAt: formatProtoTimestamp(run.GetStartedAt()), CompletedAt: formatProtoTimestamp(run.GetCompletedAt()),
	}
}

func visitSchedulerRuntimeRuns(ctx context.Context, clients cliServiceClients, projectID, agentName string, visit func(*agentcomposev2.SchedulerRun) error) error {
	if clients.projectStream == nil {
		return visitSchedulerRuntimeRunsFallback(ctx, clients.project, projectID, agentName, visit)
	}
	stream, err := clients.projectStream.StreamSchedulerRuns(ctx, connect.NewRequest(&agentcomposev2.StreamSchedulerRunsRequest{
		Project: &agentcomposev2.ProjectRef{ProjectId: projectID}, AgentName: agentName,
		BatchSize: schedulerCLIBatchSize, Limit: uint32(maxSchedulerQueryRuns),
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnimplemented {
			return visitSchedulerRuntimeRunsFallback(ctx, clients.project, projectID, agentName, visit)
		}
		return commandExitErrorForConnect(fmt.Errorf("stream scheduler runs for agent %s: %w", agentName, err))
	}
	received := false
	completed := false
	truncated := false
	var receivedRuns uint64
	for stream.Receive() {
		received = true
		if completed {
			return fmt.Errorf("daemon sent scheduler run frames after stream completion")
		}
		frame := stream.Msg()
		if uint64(len(frame.GetRuns())) > maxSchedulerQueryRuns-receivedRuns {
			return fmt.Errorf("daemon returned more than %d scheduler runs for agent %s", maxSchedulerQueryRuns, agentName)
		}
		receivedRuns += uint64(len(frame.GetRuns()))
		for _, run := range frame.GetRuns() {
			if strings.TrimSpace(run.GetTriggerId()) == "" {
				continue
			}
			if err := visit(run); err != nil {
				return err
			}
		}
		if frame.GetComplete() {
			completed = true
			truncated = frame.GetTruncated()
		}
	}
	if err := stream.Err(); err != nil {
		if !received && connect.CodeOf(err) == connect.CodeUnimplemented {
			return visitSchedulerRuntimeRunsFallback(ctx, clients.project, projectID, agentName, visit)
		}
		return commandExitErrorForConnect(fmt.Errorf("stream scheduler runs for agent %s: %w", agentName, err))
	}
	if !completed {
		return fmt.Errorf("stream scheduler runs for agent %s: daemon closed the stream before completion", agentName)
	}
	if truncated {
		return fmt.Errorf("daemon returned more than %d scheduler runs for agent %s", maxSchedulerQueryRuns, agentName)
	}
	return nil
}

func visitSchedulerRuntimeRunsFallback(ctx context.Context, client agentcomposev2connect.ProjectServiceClient, projectID, agentName string, visit func(*agentcomposev2.SchedulerRun) error) error {
	err := visitSchedulerRunsFromAPI(ctx, client, projectID, agentName, visit)
	if err != nil && connect.CodeOf(err) == connect.CodeUnimplemented {
		return commandExitError{Code: exitCodeUnsupported, Err: fmt.Errorf("daemon does not support scheduler run queries; upgrade the daemon")}
	}
	if err != nil {
		return commandExitErrorForConnect(fmt.Errorf("list scheduler runs for agent %s: %w", agentName, err))
	}
	return nil
}

func legacySchedulerSandboxBelongsToProject(tags map[string]string, project *agentcomposev2.Project) bool {
	return legacySchedulerAgentForProject(tags, project) != ""
}

func legacySchedulerAgentForProject(tags map[string]string, project *agentcomposev2.Project) string {
	if tags["project_id"] != "" || tags["origin"] != "loader" {
		return ""
	}
	loaderID := strings.TrimSpace(tags["loader_id"])
	projectID := strings.TrimSpace(project.GetSummary().GetProjectId())
	if loaderID == "" || projectID == "" {
		return ""
	}
	for _, scheduler := range project.GetSchedulers() {
		managedLoaderID, err := domain.StableManagedLoaderID(projectID, scheduler.GetAgentName(), "")
		if err == nil && loaderID == managedLoaderID {
			return strings.TrimSpace(scheduler.GetAgentName())
		}
	}
	return ""
}

func schedulerRunIsNewer(schedulerRun composeSchedulerRunItem, projectRun *agentcomposev2.RunSummary) bool {
	if strings.TrimSpace(schedulerRun.RunID) == "" {
		return false
	}
	if projectRun == nil {
		return true
	}
	return runTimestampAfter(schedulerRunSortTime(schedulerRun), projectRunAssociationSortTime(projectRun))
}

func schedulerRunSortTime(run composeSchedulerRunItem) string {
	return firstNonEmptyString(run.CompletedAt, run.StartedAt)
}

func projectRunAssociationSortTime(run *agentcomposev2.RunSummary) string {
	return firstNonEmptyString(run.GetUpdatedAt(), run.GetCompletedAt(), run.GetStartedAt(), run.GetCreatedAt())
}

func runTimestampAfter(candidate, current string) bool {
	candidateTime, candidateErr := time.Parse(time.RFC3339Nano, candidate)
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	if candidateErr == nil && currentErr == nil {
		return candidateTime.After(currentTime)
	}
	return candidate > current
}
