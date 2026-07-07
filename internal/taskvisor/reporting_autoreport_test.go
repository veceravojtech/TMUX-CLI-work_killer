package taskvisor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/console/tmux-cli/internal/producer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the taskvisor.auto_report gate at the single submit seam
// (submitReport). Unlike the reporting_contract_test.go tests — which swap
// submitReportFn and therefore BYPASS the gate — these use a REAL, non-nil
// producer pointed at an httptest server so they observe whether an actual POST
// reaches the backend. This is the only way to prove the gate (`!d.autoReport`)
// no-ops with a producer wired (api.enabled on) but auto-reporting off.

// newCountingProducer builds a real *producer.Client (via the test seam) that
// POSTs to an httptest server, returning the client and a counter incremented
// once per received request. The server is torn down on cleanup.
func newCountingProducer(t *testing.T) (*producer.Client, *int64) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	var count int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&count, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"task-1","status":"queued"}`)
	}))
	t.Cleanup(srv.Close)

	client := producer.NewClientForTest(srv.URL, priv, srv.Client())
	require.NotNil(t, client)
	return client, &count
}

func sampleReportRequest() producer.TaskRequest {
	return producer.TaskRequest{
		Category:           "general",
		Severity:           "info",
		Title:              "t",
		Description:        "d",
		ProposedFix:        "f",
		ExpectedGreenState: "g",
	}
}

// TestSubmitReport_AutoReportOff_NoSubmit: with a wired producer but
// d.autoReport=false, submitReport must take the synchronous no-op path — no
// goroutine, no POST — while still invoking onResult(nil) so dedup marks are kept.
func TestSubmitReport_AutoReportOff_NoSubmit(t *testing.T) {
	client, count := newCountingProducer(t)
	d := &Daemon{producer: client, autoReport: false, ctx: context.Background()}

	called := false
	require.NotPanics(t, func() {
		d.submitReport(sampleReportRequest(), func(error) { called = true })
	})

	// The off-path is synchronous: onResult fires before submitReport returns and
	// no goroutine exists to POST later.
	assert.True(t, called, "onResult must fire synchronously on the no-op path")
	assert.Equal(t, int64(0), atomic.LoadInt64(count), "no POST may reach the backend when auto_report is off")
}

// TestSubmitReport_AutoReportOn_Submits: with a wired producer and
// d.autoReport=true, submitReport reaches d.producer.SubmitTask — exactly one
// POST hits the backend.
func TestSubmitReport_AutoReportOn_Submits(t *testing.T) {
	client, count := newCountingProducer(t)
	d := &Daemon{producer: client, autoReport: true, ctx: context.Background()}

	done := make(chan error, 1)
	d.submitReport(sampleReportRequest(), func(err error) { done <- err })

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("submitReport goroutine did not complete")
	}
	assert.Equal(t, int64(1), atomic.LoadInt64(count), "exactly one POST must reach the backend when auto_report is on")
}

// TestReportFailure_AutoReportOff_NoOp: the production reportFailure path (routed
// through the real submitReportFn seam) files nothing when auto_report is off.
func TestReportFailure_AutoReportOff_NoOp(t *testing.T) {
	client, count := newCountingProducer(t)
	d := &Daemon{producer: client, autoReport: false, ctx: context.Background()}

	require.NotPanics(t, func() {
		d.reportFailure("execute", "warning", "some failure", "desc", nil)
	})
	assert.Equal(t, int64(0), atomic.LoadInt64(count), "reportFailure must no-op when auto_report is off")
}
