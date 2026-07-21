package ionet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ListContainers
// ---------------------------------------------------------------------------

func TestListContainers_EmptyDeploymentID(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.ListContainers("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment ID cannot be empty")
}

func TestListContainers_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"data":{"total":1,"workers":[{"container_id":"c1","created_at":"2023-06-15T12:30:45"}]}}`), nil)
	list, err := c.ListContainers("dep1")
	require.NoError(t, err)
	assert.Equal(t, "/deployment/dep1/containers", requestPath(t, stub))
	require.Len(t, list.Workers, 1)
	assert.Equal(t, "c1", list.Workers[0].ContainerID)
	assert.Equal(t, 2023, list.Workers[0].CreatedAt.Year())
}

func TestListContainers_TransportErrorWrapped(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.ListContainers("dep1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list containers")
}

func TestListContainers_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("not json"), nil)
	_, err := c.ListContainers("dep1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse containers list")
}

// ---------------------------------------------------------------------------
// GetContainerDetails
// ---------------------------------------------------------------------------

func TestGetContainerDetails_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetContainerDetails("", "c1")
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
	_, err = c.GetContainerDetails("d1", "")
	assert.ErrorContains(t, err, "container ID cannot be empty")
}

func TestGetContainerDetails_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"container_id":"c1","status":"running","created_at":"2023-06-15T12:30:45"}`), nil)
	got, err := c.GetContainerDetails("d1", "c1")
	require.NoError(t, err)
	assert.Equal(t, "/deployment/d1/container/c1", requestPath(t, stub))
	assert.Equal(t, "running", got.Status)
}

func TestGetContainerDetails_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetContainerDetails("d1", "c1")
	assert.ErrorContains(t, err, "failed to get container details")
}

func TestGetContainerDetails_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("<xml/>"), nil)
	_, err := c.GetContainerDetails("d1", "c1")
	assert.ErrorContains(t, err, "failed to parse container details")
}

// ---------------------------------------------------------------------------
// GetContainerJobs
// ---------------------------------------------------------------------------

func TestGetContainerJobs_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.GetContainerJobs("", "c1")
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
	_, err = c.GetContainerJobs("d1", "")
	assert.ErrorContains(t, err, "container ID cannot be empty")
}

func TestGetContainerJobs_Success(t *testing.T) {
	c, stub := newStubClient(okResp(`{"data":{"total":0,"workers":[]}}`), nil)
	list, err := c.GetContainerJobs("d1", "c1")
	require.NoError(t, err)
	assert.Equal(t, "/deployment/d1/containers-jobs/c1", requestPath(t, stub))
	assert.Empty(t, list.Workers)
}

func TestGetContainerJobs_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetContainerJobs("d1", "c1")
	assert.ErrorContains(t, err, "failed to get container jobs")
}

func TestGetContainerJobs_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("nope"), nil)
	_, err := c.GetContainerJobs("d1", "c1")
	assert.ErrorContains(t, err, "failed to parse container jobs")
}

// ---------------------------------------------------------------------------
// buildLogEndpoint
// ---------------------------------------------------------------------------

func TestBuildLogEndpoint_Validation(t *testing.T) {
	_, err := buildLogEndpoint("", "c1", nil)
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
	_, err = buildLogEndpoint("d1", "", nil)
	assert.ErrorContains(t, err, "container ID cannot be empty")
}

func TestBuildLogEndpoint_NilOpts(t *testing.T) {
	ep, err := buildLogEndpoint("d1", "c1", nil)
	require.NoError(t, err)
	assert.Equal(t, "/deployment/d1/log/c1", ep)
}

func TestBuildLogEndpoint_AllOptions(t *testing.T) {
	opts := &GetLogsOptions{
		Level:  "error",
		Stream: "stdout",
		Limit:  10,
		Cursor: "cur1",
		Follow: true,
	}
	ep, err := buildLogEndpoint("d1", "c1", opts)
	require.NoError(t, err)
	assert.Contains(t, ep, "/deployment/d1/log/c1?")
	assert.Contains(t, ep, "level=error")
	assert.Contains(t, ep, "stream=stdout")
	assert.Contains(t, ep, "limit=10")
	assert.Contains(t, ep, "cursor=cur1")
	assert.Contains(t, ep, "follow=true")
}

func TestBuildLogEndpoint_ZeroLimitAndEmptyStringsSkipped(t *testing.T) {
	// Limit=0 (boundary) and empty strings must be omitted; Follow=false omitted.
	opts := &GetLogsOptions{Limit: 0, Level: "", Stream: "", Cursor: "", Follow: false}
	ep, err := buildLogEndpoint("d1", "c1", opts)
	require.NoError(t, err)
	assert.Equal(t, "/deployment/d1/log/c1", ep)
}

func TestBuildLogEndpoint_StartEndTime(t *testing.T) {
	tm := mustTime("2023-06-15T12:00:00Z")
	opts := &GetLogsOptions{StartTime: &tm, EndTime: &tm}
	ep, err := buildLogEndpoint("d1", "c1", opts)
	require.NoError(t, err)
	assert.Contains(t, ep, "start_time=")
	assert.Contains(t, ep, "end_time=")
}

// ---------------------------------------------------------------------------
// GetContainerLogsRaw
// ---------------------------------------------------------------------------

func TestGetContainerLogsRaw_Validation(t *testing.T) {
	c, _ := newStubClient(okResp(""), nil)
	_, err := c.GetContainerLogsRaw("", "c1", nil)
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
}

func TestGetContainerLogsRaw_Success(t *testing.T) {
	c, _ := newStubClient(okResp("line1\nline2"), nil)
	raw, err := c.GetContainerLogsRaw("d1", "c1", nil)
	require.NoError(t, err)
	assert.Equal(t, "line1\nline2", raw)
}

func TestGetContainerLogsRaw_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.GetContainerLogsRaw("d1", "c1", nil)
	assert.ErrorContains(t, err, "failed to get container logs")
}

// ---------------------------------------------------------------------------
// GetContainerLogs (normalization)
// ---------------------------------------------------------------------------

func TestGetContainerLogs_EmptyBodyReturnsEmptyLogs(t *testing.T) {
	c, _ := newStubClient(okResp(""), nil)
	logs, err := c.GetContainerLogs("d1", "c1", nil)
	require.NoError(t, err)
	assert.Equal(t, "c1", logs.ContainerID)
	assert.Empty(t, logs.Logs)
}

func TestGetContainerLogs_NormalizesCRLFAndSkipsBlank(t *testing.T) {
	c, _ := newStubClient(okResp("a\r\n\r\nb\n   \nc"), nil)
	logs, err := c.GetContainerLogs("d1", "c1", nil)
	require.NoError(t, err)
	require.Len(t, logs.Logs, 3)
	assert.Equal(t, "a", logs.Logs[0].Message)
	assert.Equal(t, "b", logs.Logs[1].Message)
	assert.Equal(t, "c", logs.Logs[2].Message)
}

func TestGetContainerLogs_ValidationErrorPropagates(t *testing.T) {
	c, _ := newStubClient(okResp(""), nil)
	_, err := c.GetContainerLogs("", "c1", nil)
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
}

// ---------------------------------------------------------------------------
// StreamContainerLogs
// ---------------------------------------------------------------------------

func TestStreamContainerLogs_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	err := c.StreamContainerLogs("", "c1", nil, func(*LogEntry) error { return nil })
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
	err = c.StreamContainerLogs("d1", "", nil, func(*LogEntry) error { return nil })
	assert.ErrorContains(t, err, "container ID cannot be empty")
	err = c.StreamContainerLogs("d1", "c1", nil, nil)
	assert.ErrorContains(t, err, "callback function cannot be nil")
}

func TestStreamContainerLogs_SingleBatchNoMore(t *testing.T) {
	// HasMore=false and NextCursor="" → loop breaks after one pass (no sleep).
	c, _ := newStubClient(okResp(`{"logs":[{"message":"m1"},{"message":"m2"}],"has_more":false}`), nil)
	var got []string
	err := c.StreamContainerLogs("d1", "c1", nil, func(e *LogEntry) error {
		got = append(got, e.Message)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"m1", "m2"}, got)
}

func TestStreamContainerLogs_NilOptsInitialized(t *testing.T) {
	// nil opts must be initialized internally with Follow=true; no panic.
	c, _ := newStubClient(okResp(`{"logs":[],"has_more":false}`), nil)
	err := c.StreamContainerLogs("d1", "c1", nil, func(*LogEntry) error { return nil })
	require.NoError(t, err)
}

func TestStreamContainerLogs_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	err := c.StreamContainerLogs("d1", "c1", &GetLogsOptions{}, func(*LogEntry) error { return nil })
	assert.ErrorContains(t, err, "failed to stream container logs")
}

func TestStreamContainerLogs_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("garbage"), nil)
	err := c.StreamContainerLogs("d1", "c1", &GetLogsOptions{}, func(*LogEntry) error { return nil })
	assert.ErrorContains(t, err, "failed to parse container logs")
}

func TestStreamContainerLogs_CallbackErrorStops(t *testing.T) {
	c, _ := newStubClient(okResp(`{"logs":[{"message":"m1"}],"has_more":false}`), nil)
	err := c.StreamContainerLogs("d1", "c1", &GetLogsOptions{}, func(*LogEntry) error {
		return assert.AnError
	})
	assert.ErrorContains(t, err, "callback error")
}

// ---------------------------------------------------------------------------
// RestartContainer / StopContainer
// ---------------------------------------------------------------------------

func TestRestartContainer_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	assert.ErrorContains(t, c.RestartContainer("", "c1"), "deployment ID cannot be empty")
	assert.ErrorContains(t, c.RestartContainer("d1", ""), "container ID cannot be empty")
}

func TestRestartContainer_Success(t *testing.T) {
	c, stub := newStubClient(okResp("{}"), nil)
	require.NoError(t, c.RestartContainer("d1", "c1"))
	assert.Equal(t, "/deployment/d1/container/c1/restart", requestPath(t, stub))
	assert.Equal(t, "POST", stub.last.Method)
}

func TestRestartContainer_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	assert.ErrorContains(t, c.RestartContainer("d1", "c1"), "failed to restart container")
}

func TestStopContainer_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	assert.ErrorContains(t, c.StopContainer("", "c1"), "deployment ID cannot be empty")
	assert.ErrorContains(t, c.StopContainer("d1", ""), "container ID cannot be empty")
}

func TestStopContainer_Success(t *testing.T) {
	c, stub := newStubClient(okResp("{}"), nil)
	require.NoError(t, c.StopContainer("d1", "c1"))
	assert.Equal(t, "/deployment/d1/container/c1/stop", requestPath(t, stub))
}

func TestStopContainer_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	assert.ErrorContains(t, c.StopContainer("d1", "c1"), "failed to stop container")
}

// ---------------------------------------------------------------------------
// ExecuteInContainer
// ---------------------------------------------------------------------------

func TestExecuteInContainer_Validation(t *testing.T) {
	c, _ := newStubClient(okResp("{}"), nil)
	_, err := c.ExecuteInContainer("", "c1", []string{"ls"})
	assert.ErrorContains(t, err, "deployment ID cannot be empty")
	_, err = c.ExecuteInContainer("d1", "", []string{"ls"})
	assert.ErrorContains(t, err, "container ID cannot be empty")
	_, err = c.ExecuteInContainer("d1", "c1", nil)
	assert.ErrorContains(t, err, "command cannot be empty")
	_, err = c.ExecuteInContainer("d1", "c1", []string{})
	assert.ErrorContains(t, err, "command cannot be empty")
}

func TestExecuteInContainer_OutputField(t *testing.T) {
	c, stub := newStubClient(okResp(`{"output":"hello"}`), nil)
	out, err := c.ExecuteInContainer("d1", "c1", []string{"echo", "hi"})
	require.NoError(t, err)
	assert.Equal(t, "hello", out)
	assert.Equal(t, "/deployment/d1/container/c1/exec", requestPath(t, stub))
	// verify command body was marshaled
	assert.Contains(t, string(stub.last.Body), `"command":["echo","hi"]`)
}

func TestExecuteInContainer_NoOutputFieldReturnsRawBody(t *testing.T) {
	c, _ := newStubClient(okResp(`{"result":123}`), nil)
	out, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	require.NoError(t, err)
	assert.Equal(t, `{"result":123}`, out)
}

func TestExecuteInContainer_OutputNotStringReturnsRawBody(t *testing.T) {
	// "output" present but not a string → type assertion fails → raw body.
	c, _ := newStubClient(okResp(`{"output":42}`), nil)
	out, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	require.NoError(t, err)
	assert.Equal(t, `{"output":42}`, out)
}

func TestExecuteInContainer_DecodeError(t *testing.T) {
	c, _ := newStubClient(okResp("not json"), nil)
	_, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	assert.ErrorContains(t, err, "failed to parse execution result")
}

func TestExecuteInContainer_TransportError(t *testing.T) {
	c, _ := newStubClient(nil, assert.AnError)
	_, err := c.ExecuteInContainer("d1", "c1", []string{"ls"})
	assert.ErrorContains(t, err, "failed to execute command in container")
}
