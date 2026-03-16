package controller

import (
	"testing"
)

func TestReportProcessorReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newReportProcessorReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing ReportProcessor")
	}
}

func TestReportProcessorReconcile_BasicReconcile(t *testing.T) {
	rp := newReportProcessor("test-rp", "production", "https://reports.example.com")
	c := setupTestClient(rp)
	r := newReportProcessorReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-rp"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for basic reconcile")
	}
}
