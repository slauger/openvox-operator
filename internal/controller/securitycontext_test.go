package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestBuildPodSecurityContext_Defaults(t *testing.T) {
	psc := buildPodSecurityContext(ServerRunAsUser, ServerRunAsGroup, ServerFSGroup, nil)

	if psc.RunAsUser == nil || *psc.RunAsUser != ServerRunAsUser {
		t.Errorf("expected RunAsUser=%d, got %v", ServerRunAsUser, psc.RunAsUser)
	}
	if psc.RunAsGroup == nil || *psc.RunAsGroup != ServerRunAsGroup {
		t.Errorf("expected RunAsGroup=%d, got %v", ServerRunAsGroup, psc.RunAsGroup)
	}
	if psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true")
	}
	if psc.FSGroup == nil || *psc.FSGroup != ServerFSGroup {
		t.Errorf("expected FSGroup=%d, got %v", ServerFSGroup, psc.FSGroup)
	}
	if psc.FSGroupChangePolicy == nil || *psc.FSGroupChangePolicy != corev1.FSGroupChangeOnRootMismatch {
		t.Errorf("expected FSGroupChangePolicy=OnRootMismatch, got %v", psc.FSGroupChangePolicy)
	}
	if psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("expected Seccomp RuntimeDefault")
	}
}

func TestBuildPodSecurityContext_PartialOverride(t *testing.T) {
	// Overriding only fsGroup must leave the other defaults untouched.
	fsGroup := int64(2000)
	psc := buildPodSecurityContext(ServerRunAsUser, ServerRunAsGroup, ServerFSGroup,
		&openvoxv1alpha1.PodSecurityContextSpec{FSGroup: &fsGroup})

	if psc.FSGroup == nil || *psc.FSGroup != 2000 {
		t.Errorf("expected overridden FSGroup=2000, got %v", psc.FSGroup)
	}
	if psc.RunAsUser == nil || *psc.RunAsUser != ServerRunAsUser {
		t.Errorf("expected default RunAsUser=%d, got %v", ServerRunAsUser, psc.RunAsUser)
	}
	if psc.FSGroupChangePolicy == nil || *psc.FSGroupChangePolicy != corev1.FSGroupChangeOnRootMismatch {
		t.Errorf("expected default FSGroupChangePolicy=OnRootMismatch, got %v", psc.FSGroupChangePolicy)
	}
}

func TestBuildPodSecurityContext_FullOverride(t *testing.T) {
	user := int64(1234)
	group := int64(1234)
	fsGroup := int64(1234)
	nonRoot := false
	policy := corev1.FSGroupChangeAlways

	psc := buildPodSecurityContext(ServerRunAsUser, ServerRunAsGroup, ServerFSGroup,
		&openvoxv1alpha1.PodSecurityContextSpec{
			RunAsUser:           &user,
			RunAsGroup:          &group,
			RunAsNonRoot:        &nonRoot,
			FSGroup:             &fsGroup,
			FSGroupChangePolicy: &policy,
		})

	if *psc.RunAsUser != 1234 || *psc.RunAsGroup != 1234 || *psc.FSGroup != 1234 {
		t.Errorf("expected all uid/gid/fsGroup=1234, got %v/%v/%v", psc.RunAsUser, psc.RunAsGroup, psc.FSGroup)
	}
	if psc.RunAsNonRoot == nil || *psc.RunAsNonRoot {
		t.Error("expected overridden RunAsNonRoot=false")
	}
	if psc.FSGroupChangePolicy == nil || *psc.FSGroupChangePolicy != corev1.FSGroupChangeAlways {
		t.Errorf("expected overridden FSGroupChangePolicy=Always, got %v", psc.FSGroupChangePolicy)
	}
}
