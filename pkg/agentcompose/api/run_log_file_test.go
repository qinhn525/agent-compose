package api

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	domain "agent-compose/pkg/model"
	agentcomposev2 "agent-compose/proto/agentcompose/v2"
)

func TestReadRunLogChunkFromOffsetKeepsFramesBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	want := strings.Repeat("a", runLogFileChunkBytes*2+137)
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	var got strings.Builder
	var offset uint64
	for {
		chunk, next, atEnd, err := readRunLogChunkFromOffset(path, offset)
		if err != nil {
			t.Fatalf("read chunk at %d: %v", offset, err)
		}
		if len(chunk) > runLogFileChunkBytes || next < offset {
			t.Fatalf("chunk length/next = %d/%d at %d", len(chunk), next, offset)
		}
		got.WriteString(chunk)
		offset = next
		if atEnd {
			break
		}
	}
	if got.String() != want {
		t.Fatalf("reassembled log length = %d, want %d", got.Len(), len(want))
	}
}

func TestReadRunLogChunkFromOffsetKeepsUTF8CodePointsIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	want := strings.Repeat("a", runLogFileChunkBytes-1) + "界" + strings.Repeat("b", 32)
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	first, offset, atEnd, err := readRunLogChunkFromOffset(path, 0)
	if err != nil {
		t.Fatalf("read first chunk: %v", err)
	}
	if atEnd || !strings.HasSuffix(first, "a") || len(first) != runLogFileChunkBytes-1 {
		t.Fatalf("first chunk end/length = %t/%d", atEnd, len(first))
	}
	second, _, atEnd, err := readRunLogChunkFromOffset(path, offset)
	if err != nil {
		t.Fatalf("read second chunk: %v", err)
	}
	if !atEnd || first+second != want {
		t.Fatalf("reassembled UTF-8 log end/length = %t/%d, want true/%d", atEnd, len(first+second), len(want))
	}
}

func TestTailRunLogOffsetScansBackwardsInBoundedChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	prefix := strings.Repeat("x", runLogFileChunkBytes+17)
	data := prefix + "\nlast-one\nlast-two\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	offset, err := tailRunLogOffset(path, 2)
	if err != nil {
		t.Fatalf("tail offset: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(int64(offset), io.SeekStart); err != nil {
		t.Fatalf("seek tail: %v", err)
	}
	tail, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if string(tail) != "last-one\nlast-two\n" {
		t.Fatalf("tail = %q", tail)
	}
}

func TestFollowRunLogsStreamsMetadataAndBoundedFrames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	want := strings.Repeat("log-line\n", runLogFileChunkBytes/4)
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	store := &apiProjectRunStore{runs: map[string]domain.ProjectRunRecord{
		"run-large": {RunID: "run-large", ProjectID: "project-1", AgentName: "reviewer", Prompt: "inspect the repository", Status: domain.ProjectRunStatusSucceeded, LogsPath: path},
	}}
	client, closeServer := newRunHandlerTestClient(t, NewRunHandler(nil, store))
	defer closeServer()
	stream, err := client.FollowRunLogs(context.Background(), connect.NewRequest(&agentcomposev2.FollowRunLogsRequest{
		ProjectId: "project-1", RunId: "run-large", IncludeMetadata: true,
	}))
	if err != nil {
		t.Fatalf("FollowRunLogs returned error: %v", err)
	}
	var data strings.Builder
	var metadata, final bool
	for stream.Receive() {
		chunk := stream.Msg()
		if chunk.GetRun() != nil && chunk.GetPrompt() == "inspect the repository" {
			metadata = true
		}
		if len(chunk.GetData()) > runLogFileChunkBytes {
			t.Fatalf("streamed chunk length = %d", len(chunk.GetData()))
		}
		data.WriteString(chunk.GetData())
		final = final || chunk.GetIsFinal()
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("FollowRunLogs receive error: %v", err)
	}
	if !metadata || !final || data.String() != want {
		t.Fatalf("metadata/final/data length = %t/%t/%d, want true/true/%d", metadata, final, data.Len(), len(want))
	}
	assertSchedulerStreamHeaders(t, stream.ResponseHeader())
}

func TestFollowRunLogsExplicitZeroTailSkipsExistingOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	if err := os.WriteFile(path, []byte("existing output\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	store := &apiProjectRunStore{runs: map[string]domain.ProjectRunRecord{
		"run-tail-zero": {RunID: "run-tail-zero", ProjectID: "project-1", AgentName: "reviewer", Prompt: "prompt", Status: domain.ProjectRunStatusSucceeded, LogsPath: path},
	}}
	client, closeServer := newRunHandlerTestClient(t, NewRunHandler(nil, store))
	defer closeServer()
	stream, err := client.FollowRunLogs(context.Background(), connect.NewRequest(&agentcomposev2.FollowRunLogsRequest{
		ProjectId: "project-1", RunId: "run-tail-zero", TailSet: true, IncludeMetadata: true,
	}))
	if err != nil {
		t.Fatalf("FollowRunLogs returned error: %v", err)
	}
	var data strings.Builder
	var metadata, final bool
	for stream.Receive() {
		chunk := stream.Msg()
		data.WriteString(chunk.GetData())
		metadata = metadata || chunk.GetRun() != nil
		final = final || chunk.GetIsFinal()
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("FollowRunLogs receive error: %v", err)
	}
	if data.Len() != 0 || !metadata || !final {
		t.Fatalf("zero-tail data/metadata/final = %q/%t/%t", data.String(), metadata, final)
	}
}
