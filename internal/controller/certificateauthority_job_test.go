package controller

import (
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestFindCAServerCert(t *testing.T) {
	t.Run("finds cert from server with ca:true", func(t *testing.T) {
		ca := newCertificateAuthority("myca")
		cfg := newConfig("production", withAuthorityRef("myca"))
		server := newServer("ca-server", withCA(true), withServerRole(true))
		server.Spec.ConfigRef = "production"
		server.Spec.CertificateRef = "ca-cert"

		cert1 := newCertificate("other-cert", "myca", openvoxv1alpha1.CertificatePhasePending)
		cert2 := newCertificate("ca-cert", "myca", openvoxv1alpha1.CertificatePhasePending)
		cert2.Spec.Certname = "ca.example.com"

		c := setupTestClient(ca, cfg, server, cert1, cert2)
		r := newCertificateAuthorityReconciler(c)

		certs := []openvoxv1alpha1.Certificate{*cert1, *cert2}
		found := r.findCAServerCert(testCtx(), ca, certs)

		if found == nil {
			t.Fatal("expected to find a certificate, got nil")
		}
		if found.Name != "ca-cert" {
			t.Errorf("expected cert 'ca-cert', got %q", found.Name)
		}
	})

	t.Run("fallback to first cert when no CA server", func(t *testing.T) {
		ca := newCertificateAuthority("myca")
		cfg := newConfig("production", withAuthorityRef("myca"))
		// No server with ca:true
		server := newServer("server1")
		server.Spec.ConfigRef = "production"
		server.Spec.CA = false

		cert1 := newCertificate("first-cert", "myca", openvoxv1alpha1.CertificatePhasePending)
		cert2 := newCertificate("second-cert", "myca", openvoxv1alpha1.CertificatePhasePending)

		c := setupTestClient(ca, cfg, server, cert1, cert2)
		r := newCertificateAuthorityReconciler(c)

		certs := []openvoxv1alpha1.Certificate{*cert1, *cert2}
		found := r.findCAServerCert(testCtx(), ca, certs)

		if found == nil {
			t.Fatal("expected fallback to first cert, got nil")
		}
		if found.Name != "first-cert" {
			t.Errorf("expected 'first-cert', got %q", found.Name)
		}
	})

	t.Run("returns nil with no certs", func(t *testing.T) {
		ca := newCertificateAuthority("myca")
		c := setupTestClient(ca)
		r := newCertificateAuthorityReconciler(c)

		found := r.findCAServerCert(testCtx(), ca, nil)
		if found != nil {
			t.Errorf("expected nil with empty certs, got %v", found)
		}
	})
}

func TestBuildCASetupJob(t *testing.T) {
	t.Run("certname from CA server cert", func(t *testing.T) {
		ca := newCertificateAuthority("myca")
		cfg := newConfig("production", withAuthorityRef("myca"))
		server := newServer("ca-server", withCA(true), withServerRole(true))
		server.Spec.ConfigRef = "production"
		server.Spec.CertificateRef = "ca-cert"

		cert := newCertificate("ca-cert", "myca", openvoxv1alpha1.CertificatePhasePending)
		cert.Spec.Certname = "puppet-ca.example.com"
		cert.Spec.DNSAltNames = []string{"puppet.example.com"}

		c := setupTestClient(ca, cfg, server, cert)
		r := newCertificateAuthorityReconciler(c)

		certs := []openvoxv1alpha1.Certificate{*cert}
		job := r.buildCASetupJob(testCtx(), ca, cfg, "myca-ca-setup", certs)

		// Check env vars for certname
		container := job.Spec.Template.Spec.Containers[0]
		certnameFound := false
		dnsFound := false
		for _, env := range container.Env {
			if env.Name == "CERTNAME" && env.Value == "puppet-ca.example.com" {
				certnameFound = true
			}
			if env.Name == "DNS_ALT_NAMES" {
				dnsFound = true
				// Should contain the original DNS alt name and the service FQDN
				if !strings.Contains(env.Value, "puppet.example.com") {
					t.Errorf("expected DNS alt name 'puppet.example.com' in %q", env.Value)
				}
				serviceFQDN := "myca-internal.default.svc"
				if !strings.Contains(env.Value, serviceFQDN) {
					t.Errorf("expected service FQDN %q in DNS_ALT_NAMES %q", serviceFQDN, env.Value)
				}
			}
		}
		if !certnameFound {
			t.Error("CERTNAME env var not found or incorrect")
		}
		if !dnsFound {
			t.Error("DNS_ALT_NAMES env var not found")
		}
	})

	t.Run("default certname puppet when no cert found", func(t *testing.T) {
		ca := newCertificateAuthority("myca")
		cfg := newConfig("production", withAuthorityRef("myca"))

		c := setupTestClient(ca, cfg)
		r := newCertificateAuthorityReconciler(c)

		job := r.buildCASetupJob(testCtx(), ca, cfg, "myca-ca-setup", nil)

		container := job.Spec.Template.Spec.Containers[0]
		for _, env := range container.Env {
			if env.Name == "CERTNAME" {
				if env.Value != "puppet" {
					t.Errorf("expected default certname 'puppet', got %q", env.Value)
				}
				return
			}
		}
		t.Error("CERTNAME env var not found")
	})

	t.Run("job metadata and spec", func(t *testing.T) {
		ca := newCertificateAuthority("myca")
		cfg := newConfig("production", withAuthorityRef("myca"))

		c := setupTestClient(ca, cfg)
		r := newCertificateAuthorityReconciler(c)

		job := r.buildCASetupJob(testCtx(), ca, cfg, "myca-ca-setup", nil)

		if job.Name != "myca-ca-setup" {
			t.Errorf("expected job name 'myca-ca-setup', got %q", job.Name)
		}
		if job.Namespace != testNamespace {
			t.Errorf("expected namespace %q, got %q", testNamespace, job.Namespace)
		}

		spec := job.Spec.Template.Spec
		if spec.ServiceAccountName != "myca-ca-setup" {
			t.Errorf("expected SA name 'myca-ca-setup', got %q", spec.ServiceAccountName)
		}
		if spec.RestartPolicy != corev1.RestartPolicyNever {
			t.Errorf("expected RestartPolicyNever, got %v", spec.RestartPolicy)
		}

		container := spec.Containers[0]
		expectedImage := "ghcr.io/slauger/openvox-server:latest"
		if container.Image != expectedImage {
			t.Errorf("expected image %q, got %q", expectedImage, container.Image)
		}

		// Security context checks
		if spec.SecurityContext == nil {
			t.Fatal("expected pod security context")
		}
		if *spec.SecurityContext.RunAsUser != CASetupRunAsUser {
			t.Errorf("expected RunAsUser %d, got %d", CASetupRunAsUser, *spec.SecurityContext.RunAsUser)
		}
	})
}

func TestResolveCAJobResources(t *testing.T) {
	t.Run("default resources", func(t *testing.T) {
		ca := newCertificateAuthority("myca")
		res := resolveCAJobResources(ca)

		expectedCPUReq := resource.MustParse(DefaultCAJobCPURequest)
		expectedMemReq := resource.MustParse(DefaultCAJobMemoryRequest)
		expectedCPULim := resource.MustParse(DefaultCAJobCPULimit)
		expectedMemLim := resource.MustParse(DefaultCAJobMemoryLimit)

		if !res.Requests.Cpu().Equal(expectedCPUReq) {
			t.Errorf("expected CPU request %s, got %s", expectedCPUReq.String(), res.Requests.Cpu().String())
		}
		if !res.Requests.Memory().Equal(expectedMemReq) {
			t.Errorf("expected memory request %s, got %s", expectedMemReq.String(), res.Requests.Memory().String())
		}
		if !res.Limits.Cpu().Equal(expectedCPULim) {
			t.Errorf("expected CPU limit %s, got %s", expectedCPULim.String(), res.Limits.Cpu().String())
		}
		if !res.Limits.Memory().Equal(expectedMemLim) {
			t.Errorf("expected memory limit %s, got %s", expectedMemLim.String(), res.Limits.Memory().String())
		}
	})

	t.Run("user-specified resources", func(t *testing.T) {
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "myca", Namespace: testNamespace},
			Spec: openvoxv1alpha1.CertificateAuthoritySpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
		}

		res := resolveCAJobResources(ca)

		expectedCPU := resource.MustParse("500m")
		if !res.Requests.Cpu().Equal(expectedCPU) {
			t.Errorf("expected CPU request 500m, got %s", res.Requests.Cpu().String())
		}

		expectedMem := resource.MustParse("4Gi")
		if !res.Limits.Memory().Equal(expectedMem) {
			t.Errorf("expected memory limit 4Gi, got %s", res.Limits.Memory().String())
		}
	})
}

func TestReconcileJob_CreatesNew(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cfg := caPrereqs("test-ca")
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ca-setup",
			Namespace: testNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "setup", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	res, err := r.reconcileJob(testCtx(), ca, "test-ca-setup", job, "test-ca-ca")
	if err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}
	if res.RequeueAfter != RequeueIntervalMedium {
		t.Errorf("expected requeue after %v, got %v", RequeueIntervalMedium, res.RequeueAfter)
	}

	created := &batchv1.Job{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-setup", Namespace: testNamespace}, created); err != nil {
		t.Fatalf("Job not created: %v", err)
	}
}

func TestReconcileJob_Succeeded(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cfg := caPrereqs("test-ca")

	// Pre-create the expected secret
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert-pem"),
	})

	existingJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ca-setup",
			Namespace: testNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "setup", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: batchv1.JobStatus{
			Succeeded: 1,
		},
	}

	c := setupTestClient(ca, cfg, caSecret, existingJob)
	r := newCertificateAuthorityReconciler(c)

	desiredJob := existingJob.DeepCopy()
	res, err := r.reconcileJob(testCtx(), ca, "test-ca-setup", desiredJob, "test-ca-ca")
	if err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("expected no requeue for succeeded job, got %v", res.RequeueAfter)
	}
}

func TestReconcileJob_Failed(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cfg := caPrereqs("test-ca")

	existingJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ca-setup",
			Namespace: testNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "setup", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: batchv1.JobStatus{
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobFailed, Status: corev1.ConditionTrue},
			},
		},
	}

	c := setupTestClient(ca, cfg, existingJob)
	r := newCertificateAuthorityReconciler(c)

	desiredJob := existingJob.DeepCopy()
	res, err := r.reconcileJob(testCtx(), ca, "test-ca-setup", desiredJob, "test-ca-ca")
	if err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}
	if res.RequeueAfter != RequeueIntervalMedium {
		t.Errorf("expected requeue after %v for failed job, got %v", RequeueIntervalMedium, res.RequeueAfter)
	}
}

func TestReconcileJob_Running(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cfg := caPrereqs("test-ca")

	existingJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ca-setup",
			Namespace: testNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "setup", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	c := setupTestClient(ca, cfg, existingJob)
	r := newCertificateAuthorityReconciler(c)

	desiredJob := existingJob.DeepCopy()
	res, err := r.reconcileJob(testCtx(), ca, "test-ca-setup", desiredJob, "test-ca-ca")
	if err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}
	if res.RequeueAfter != RequeueIntervalLong {
		t.Errorf("expected requeue after %v for running job, got %v", RequeueIntervalLong, res.RequeueAfter)
	}
}

func TestReconcileJob_ImageChanged(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cfg := caPrereqs("test-ca")

	existingJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ca-setup",
			Namespace: testNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "setup", Image: "old:v1"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	c := setupTestClient(ca, cfg, existingJob)
	r := newCertificateAuthorityReconciler(c)

	desiredJob := existingJob.DeepCopy()
	desiredJob.Spec.Template.Spec.Containers[0].Image = "new:v2"

	res, err := r.reconcileJob(testCtx(), ca, "test-ca-setup", desiredJob, "test-ca-ca")
	if err != nil {
		t.Fatalf("reconcileJob: %v", err)
	}
	// Should delete and requeue
	if res.RequeueAfter != RequeueIntervalMedium {
		t.Errorf("expected requeue after %v for image change, got %v", RequeueIntervalMedium, res.RequeueAfter)
	}
}

func TestDeleteAndRequeueJob(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-job",
			Namespace: testNamespace,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers:    []corev1.Container{{Name: "setup", Image: "test:latest"}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
	}

	c := setupTestClient(ca, job)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.deleteAndRequeueJob(testCtx(), job, "test reason")
	if err != nil {
		t.Fatalf("deleteAndRequeueJob: %v", err)
	}
	if res.RequeueAfter != RequeueIntervalMedium {
		t.Errorf("expected requeue after %v, got %v", RequeueIntervalMedium, res.RequeueAfter)
	}
}
