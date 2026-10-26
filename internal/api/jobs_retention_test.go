package api

import (
	"io"
	"log/slog"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Async job pruning must reclaim finished work without dropping a job a client
// may still be polling. The clock is supplied to pruneLocked so these cases do
// not depend on sleeping; only the in-memory manager is involved.

func TestPruneLockedDropsExpiredFinishedJobs(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	m := newJobManager(time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))

	oldSucceeded := now.Add(-2 * time.Minute)
	oldFailed := now.Add(-90 * time.Second)
	recent := now.Add(-30 * time.Second)

	m.jobs["old-succeeded"] = &Job{
		ID: "old-succeeded", State: JobSucceeded, FinishedAt: &oldSucceeded, Result: "done",
	}
	m.jobs["old-failed"] = &Job{
		ID: "old-failed", State: JobFailed, FinishedAt: &oldFailed, Error: "boom",
	}
	m.jobs["recent"] = &Job{
		ID: "recent", State: JobSucceeded, FinishedAt: &recent, Result: "still here",
	}

	m.pruneLocked(now)

	_, ok := m.jobs["old-succeeded"]
	assert.False(t, ok, "a succeeded job past the retention window is pruned")
	_, ok = m.jobs["old-failed"]
	assert.False(t, ok, "a failed job past the retention window is pruned too")

	kept, ok := m.jobs["recent"]
	require.True(t, ok, "a job inside the retention window is kept")
	assert.Equal(t, JobSucceeded, kept.State)
	assert.Equal(t, "still here", kept.Result)
}

func TestPruneLockedNeverDropsUnfinishedJobs(t *testing.T) {
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	m := newJobManager(time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// A queued job has no FinishedAt, and a running job that has been running
	// far longer than the retention window is still unfinished work.
	veryOld := now.Add(-24 * time.Hour)
	m.jobs["queued"] = &Job{
		ID: "queued", State: JobQueued, CreatedAt: veryOld,
	}
	m.jobs["running"] = &Job{
		ID: "running", State: JobRunning, CreatedAt: veryOld, StartedAt: &veryOld,
	}

	m.pruneLocked(now)

	queued, ok := m.jobs["queued"]
	require.True(t, ok, "a queued job is never pruned")
	assert.Equal(t, JobQueued, queued.State)

	running, ok := m.jobs["running"]
	require.True(t, ok, "a running job is never pruned")
	assert.Equal(t, JobRunning, running.State)
}

func TestJobInsideRetentionIsStillReturnedByGet(t *testing.T) {
	m := newJobManager(time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))

	finished := time.Now().UTC().Add(-30 * time.Minute)
	m.jobs["recent"] = &Job{
		ID: "recent", State: JobSucceeded, Alias: "counter",
		FinishedAt: &finished, Result: "deployed",
	}

	job, ok := m.get("recent")
	require.True(t, ok, "a client must still be able to poll a recent result")
	assert.Equal(t, "recent", job.ID)
	assert.Equal(t, JobSucceeded, job.State)
	assert.Equal(t, "deployed", job.Result)
	assert.Equal(t, "counter", job.Alias)
}

func TestNewJobIDIsUniqueAndURLSafe(t *testing.T) {
	const count = 5000

	seen := make(map[string]struct{}, count)
	for range count {
		id, err := newJobID()
		require.NoError(t, err)
		require.NotEmpty(t, id)

		assert.Equal(t, id, url.PathEscape(id), "job ID %q is not safe in a URL path", id)
		assert.Equal(t, id, url.QueryEscape(id), "job ID %q is not safe in a URL query", id)

		_, duplicate := seen[id]
		require.False(t, duplicate, "duplicate job ID generated: %q", id)
		seen[id] = struct{}{}
	}

	assert.Len(t, seen, count, "every generated ID is distinct")
}
