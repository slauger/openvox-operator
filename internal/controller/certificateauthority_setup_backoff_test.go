package controller

import (
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

const setupJobName = "test-ca-setup"

// failedSetupJob builds a setup Job the API server has given up on, which is
// what a bad image reference or an unschedulable pod eventually produces.
func failedSetupJob(message string) *batchv1.Job {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: setupJobName, Namespace: testNamespace},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "setup", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{{
				Type:    batchv1.JobFailed,
				Status:  corev1.ConditionTrue,
				Reason:  "BackoffLimitExceeded",
				Message: message,
			}},
		},
	}
	return job
}

// reloadCA re-reads the CertificateAuthority, the way each reconcile does.
func reloadCA(t *testing.T, c client.Client, name string) *openvoxv1alpha1.CertificateAuthority {
	t.Helper()
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: name, Namespace: testNamespace}, ca); err != nil {
		t.Fatalf("reading CertificateAuthority %s: %v", name, err)
	}
	return ca
}

// TestSetupJob_StopsRecreatingAfterRepeatedFailures is the point of the change:
// a Job that cannot succeed used to be deleted and recreated roughly every 15
// seconds forever.
func TestSetupJob_StopsRecreatingAfterRepeatedFailures(t *testing.T) {
	c := setupTestClient(newCertificateAuthority("test-ca"), caPrereqs("test-ca"))
	r := newCertificateAuthorityReconciler(c)

	for attempt := 1; attempt <= maxSetupAttempts; attempt++ {
		// Each round the controller finds a fresh Job that has failed again.
		job := failedSetupJob("pod has unbound immediate PersistentVolumeClaims")
		if err := c.Create(testCtx(), job); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("seeding the failed job for attempt %d: %v", attempt, err)
		}

		ca := reloadCA(t, c, "test-ca")
		result, err := r.reconcileJob(testCtx(), ca, setupJobName, job.DeepCopy(), "test-ca-ca")
		if err != nil {
			t.Fatalf("reconcileJob attempt %d: %v", attempt, err)
		}

		err = c.Get(testCtx(), types.NamespacedName{Name: setupJobName, Namespace: testNamespace}, &batchv1.Job{})
		deleted := apierrors.IsNotFound(err)

		if attempt < maxSetupAttempts {
			if !deleted {
				t.Fatalf("attempt %d should still retry, but the job was kept", attempt)
			}
			if result.RequeueAfter != RequeueIntervalMedium {
				t.Errorf("attempt %d: expected a retry requeue, got %v", attempt, result.RequeueAfter)
			}
			continue
		}

		// The budget is spent.
		if deleted {
			t.Error("the failed job must be left in place once the controller gives up, its logs are the only evidence")
		}
		if result.RequeueAfter != 0 {
			t.Errorf("giving up must not keep polling, got RequeueAfter %v", result.RequeueAfter)
		}
	}
}

// TestSetupJob_ReportsFailureInCondition covers the second half of the problem:
// the CA sat in Initializing with no indication of why.
func TestSetupJob_ReportsFailureInCondition(t *testing.T) {
	c := setupTestClient(newCertificateAuthority("test-ca"), caPrereqs("test-ca"))
	r := newCertificateAuthorityReconciler(c)

	ca := reloadCA(t, c, "test-ca")
	ca.Annotations = map[string]string{AnnotationSetupAttempts: "4"}
	if err := c.Update(testCtx(), ca); err != nil {
		t.Fatalf("seeding the attempt counter: %v", err)
	}

	job := failedSetupJob("Job has reached the specified backoff limit")
	if err := c.Create(testCtx(), job); err != nil {
		t.Fatalf("seeding the failed job: %v", err)
	}

	if _, err := r.reconcileJob(testCtx(), reloadCA(t, c, "test-ca"), setupJobName, job.DeepCopy(), "test-ca-ca"); err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}

	got := reloadCA(t, c, "test-ca")
	if got.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseError {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificateAuthorityPhaseError, got.Status.Phase)
	}

	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionCAReady)
	if cond == nil {
		t.Fatal("expected a CAReady condition reporting the failure")
	}
	if cond.Status != metav1.ConditionFalse || cond.Reason != "SetupFailed" {
		t.Errorf("expected CAReady=False/SetupFailed, got %s/%s", cond.Status, cond.Reason)
	}
	if !strings.Contains(cond.Message, "backoff limit") {
		t.Errorf("the condition must carry the job's own failure message, got %q", cond.Message)
	}
}

// TestSetupJob_CounterDoesNotGrowOnceTerminal keeps repeated reconciles from
// inflating the counter and re-emitting the event.
func TestSetupJob_CounterDoesNotGrowOnceTerminal(t *testing.T) {
	c := setupTestClient(newCertificateAuthority("test-ca"), caPrereqs("test-ca"))
	r := newCertificateAuthorityReconciler(c)
	rec := events.NewFakeRecorder(100)
	r.Recorder = rec

	ca := reloadCA(t, c, "test-ca")
	ca.Annotations = map[string]string{AnnotationSetupAttempts: "4"}
	if err := c.Update(testCtx(), ca); err != nil {
		t.Fatalf("seeding the attempt counter: %v", err)
	}

	job := failedSetupJob("Job has reached the specified backoff limit")
	if err := c.Create(testCtx(), job); err != nil {
		t.Fatalf("seeding the failed job: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := r.reconcileJob(testCtx(), reloadCA(t, c, "test-ca"), setupJobName, job.DeepCopy(), "test-ca-ca"); err != nil {
			t.Fatalf("reconcileJob %d: %v", i, err)
		}
	}

	if n := setupAttempts(reloadCA(t, c, "test-ca")); n != maxSetupAttempts {
		t.Errorf("the counter must stop at %d, got %d", maxSetupAttempts, n)
	}
	failures := 0
	for drained := false; !drained; {
		select {
		case e := <-rec.Events:
			if strings.Contains(e, EventReasonCASetupFailed) {
				failures++
			}
		default:
			drained = true
		}
	}
	if failures != 1 {
		t.Errorf("expected one failure event across three reconciles, got %d", failures)
	}
}

// TestSetupJob_SuccessResetsTheBudget makes the failure budget per incident
// rather than per CA lifetime.
func TestSetupJob_SuccessResetsTheBudget(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Annotations = map[string]string{AnnotationSetupAttempts: "3"}
	caSecret := newSecret("test-ca-ca", map[string][]byte{"ca_crt.pem": []byte("cert")})

	job := failedSetupJob("")
	job.Status = batchv1.JobStatus{Succeeded: 1}

	c := setupTestClient(ca, caPrereqs("test-ca"), caSecret, job)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.reconcileJob(testCtx(), reloadCA(t, c, "test-ca"), setupJobName, job.DeepCopy(), "test-ca-ca"); err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}

	if n := setupAttempts(reloadCA(t, c, "test-ca")); n != 0 {
		t.Errorf("a successful job must clear the counter, got %d", n)
	}
}

// TestSetupJob_ImageChangeResetsTheBudget is how a user recovers: correcting
// the image the Job runs has to give it another chance.
func TestSetupJob_ImageChangeResetsTheBudget(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Annotations = map[string]string{AnnotationSetupAttempts: "5"}

	job := failedSetupJob("Job has reached the specified backoff limit")

	c := setupTestClient(ca, caPrereqs("test-ca"), job)
	r := newCertificateAuthorityReconciler(c)

	desired := job.DeepCopy()
	desired.Spec.Template.Spec.Containers[0].Image = "corrected:1.2.3"

	res, err := r.reconcileJob(testCtx(), reloadCA(t, c, "test-ca"), setupJobName, desired, "test-ca-ca")
	if err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}
	if res.RequeueAfter != RequeueIntervalMedium {
		t.Errorf("a corrected image must be retried, got RequeueAfter %v", res.RequeueAfter)
	}
	if n := setupAttempts(reloadCA(t, c, "test-ca")); n != 0 {
		t.Errorf("a corrected image must clear the counter, got %d", n)
	}
	if err := c.Get(testCtx(), types.NamespacedName{Name: setupJobName, Namespace: testNamespace}, &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Error("the stale job must be removed so the corrected one can be created")
	}
}
