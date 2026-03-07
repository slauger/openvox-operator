package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// reconcileCAPVC ensures the PVC for CA data exists.
func (r *OpenVoxServerReconciler) reconcileCAPVC(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) error {
	pvcName := fmt.Sprintf("%s-ca-data", ovs.Name)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: ovs.Namespace}, pvc)
	if errors.IsNotFound(err) {
		storageSize := "1Gi"
		if ovs.Spec.CA.Storage.Size != "" {
			storageSize = ovs.Spec.CA.Storage.Size
		}

		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: ovs.Namespace,
				Labels:    commonLabels(ovs),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(storageSize),
					},
				},
			},
		}

		if ovs.Spec.CA.Storage.StorageClass != "" {
			pvc.Spec.StorageClassName = &ovs.Spec.CA.Storage.StorageClass
		}

		if err := r.setOwnerReference(ovs, pvc); err != nil {
			return err
		}
		return r.Create(ctx, pvc)
	}
	return err
}

// reconcileCASetupJob creates and monitors the CA setup job.
func (r *OpenVoxServerReconciler) reconcileCASetupJob(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	jobName := fmt.Sprintf("%s-ca-setup", ovs.Name)

	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: ovs.Namespace}, job)
	if errors.IsNotFound(err) {
		logger.Info("creating CA setup job", "name", jobName)
		job = r.buildCASetupJob(ovs, jobName)
		if err := r.setOwnerReference(ovs, job); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check job status
	if job.Status.Succeeded > 0 {
		logger.Info("CA setup job completed successfully")
		return ctrl.Result{}, nil
	}

	if job.Status.Failed > 0 {
		return ctrl.Result{}, fmt.Errorf("CA setup job failed")
	}

	// Still running
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// buildCASetupJob constructs the Job spec for CA initialization.
func (r *OpenVoxServerReconciler) buildCASetupJob(ovs *openvoxv1alpha1.OpenVoxServer, name string) *batchv1.Job {
	image := fmt.Sprintf("%s:%s", ovs.Spec.Image.Repository, ovs.Spec.Image.Tag)
	backoffLimit := int32(3)

	caName := fmt.Sprintf("OpenVox CA %s", ovs.Name)

	setupScript := fmt.Sprintf(`#!/bin/bash
set -euo pipefail
echo "Starting CA setup..."
puppetserver ca setup \
    --config /etc/puppetlabs/puppetserver/conf.d \
    --ca-name "%s"
echo "CA setup complete."
`, caName)

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ovs.Namespace,
			Labels:    caLabels(ovs),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: caLabels(ovs),
				},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    int64Ptr(1001),
						RunAsGroup:   int64Ptr(0),
						RunAsNonRoot: boolPtr(true),
					},
					Containers: []corev1.Container{
						{
							Name:    "ca-setup",
							Image:   image,
							Command: []string{"/bin/bash", "-c", setupScript},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "ca-data",
									MountPath: "/etc/puppetlabs/puppetserver/ca",
								},
								{
									Name:      "ssl",
									MountPath: "/etc/puppetlabs/puppet/ssl",
								},
								{
									Name:      "config",
									MountPath: "/etc/puppetlabs/puppet/puppet.conf",
									SubPath:   "puppet.conf",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "ca-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: fmt.Sprintf("%s-ca-data", ovs.Name),
								},
							},
						},
						{
							Name: "ssl",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", ovs.Name),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// reconcileCAStatefulSet creates or updates the CA StatefulSet.
func (r *OpenVoxServerReconciler) reconcileCAStatefulSet(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) error {
	logger := log.FromContext(ctx)
	stsName := fmt.Sprintf("%s-ca", ovs.Name)
	image := fmt.Sprintf("%s:%s", ovs.Spec.Image.Repository, ovs.Spec.Image.Tag)
	replicas := int32(1)

	javaArgs := "-Xms512m -Xmx1024m"
	if ovs.Spec.CA.JavaArgs != "" {
		javaArgs = ovs.Spec.CA.JavaArgs
	}

	sts := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: stsName, Namespace: ovs.Namespace}, sts)
	if errors.IsNotFound(err) {
		logger.Info("creating CA StatefulSet", "name", stsName)
		sts = &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      stsName,
				Namespace: ovs.Namespace,
				Labels:    caLabels(ovs),
			},
			Spec: appsv1.StatefulSetSpec{
				Replicas:    &replicas,
				ServiceName: fmt.Sprintf("%s-ca", ovs.Name),
				Selector: &metav1.LabelSelector{
					MatchLabels: caLabels(ovs),
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: caLabels(ovs),
					},
					Spec: corev1.PodSpec{
						SecurityContext: &corev1.PodSecurityContext{
							RunAsUser:    int64Ptr(1001),
							RunAsGroup:   int64Ptr(0),
							RunAsNonRoot: boolPtr(true),
						},
						Containers: []corev1.Container{
							{
								Name:  "puppetserver",
								Image: image,
								Env: []corev1.EnvVar{
									{Name: "JAVA_ARGS", Value: javaArgs},
								},
								Ports: []corev1.ContainerPort{
									{
										Name:          "https",
										ContainerPort: 8140,
										Protocol:      corev1.ProtocolTCP,
									},
								},
								Resources: ovs.Spec.CA.Resources,
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "ca-data",
										MountPath: "/etc/puppetlabs/puppetserver/ca",
									},
									{
										Name:      "ssl",
										MountPath: "/etc/puppetlabs/puppet/ssl",
									},
									{
										Name:      "puppet-conf",
										MountPath: "/etc/puppetlabs/puppet/puppet.conf",
										SubPath:   "puppet.conf",
										ReadOnly:  true,
									},
									{
										Name:      "puppetdb-conf",
										MountPath: "/etc/puppetlabs/puppet/puppetdb.conf",
										SubPath:   "puppetdb.conf",
										ReadOnly:  true,
									},
									{
										Name:      "puppetserver-conf",
										MountPath: "/etc/puppetlabs/puppetserver/conf.d/puppetserver.conf",
										SubPath:   "puppetserver.conf",
										ReadOnly:  true,
									},
									{
										Name:      "webserver-conf",
										MountPath: "/etc/puppetlabs/puppetserver/conf.d/webserver.conf",
										SubPath:   "webserver.conf",
										ReadOnly:  true,
									},
									{
										Name:      "product-conf",
										MountPath: "/etc/puppetlabs/puppetserver/conf.d/product.conf",
										SubPath:   "product.conf",
										ReadOnly:  true,
									},
								},
								ReadinessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										TCPSocket: &corev1.TCPSocketAction{
											Port: intstr.FromInt32(8140),
										},
									},
									InitialDelaySeconds: 60,
									PeriodSeconds:       10,
								},
								LivenessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										TCPSocket: &corev1.TCPSocketAction{
											Port: intstr.FromInt32(8140),
										},
									},
									InitialDelaySeconds: 120,
									PeriodSeconds:       30,
								},
							},
						},
						Volumes: r.caVolumes(ovs),
					},
				},
			},
		}

		if err := r.setOwnerReference(ovs, sts); err != nil {
			return err
		}
		return r.Create(ctx, sts)
	} else if err != nil {
		return err
	}

	// Update existing StatefulSet
	sts.Spec.Template.Spec.Containers[0].Image = image
	sts.Spec.Template.Spec.Containers[0].Resources = ovs.Spec.CA.Resources
	sts.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "JAVA_ARGS", Value: javaArgs},
	}
	return r.Update(ctx, sts)
}

// reconcileCAService creates the CA headless service.
func (r *OpenVoxServerReconciler) reconcileCAService(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) error {
	svcName := fmt.Sprintf("%s-ca", ovs.Name)

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: ovs.Namespace}, svc)
	if errors.IsNotFound(err) {
		port := int32(8140)
		if ovs.Spec.Puppet.ServerPort != 0 {
			port = ovs.Spec.Puppet.ServerPort
		}

		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: ovs.Namespace,
				Labels:    caLabels(ovs),
			},
			Spec: corev1.ServiceSpec{
				Selector: caLabels(ovs),
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       port,
						TargetPort: intstr.FromInt32(8140),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}

		if err := r.setOwnerReference(ovs, svc); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	}
	return err
}

// caVolumes returns the volume list for CA pods.
func (r *OpenVoxServerReconciler) caVolumes(ovs *openvoxv1alpha1.OpenVoxServer) []corev1.Volume {
	configMapName := fmt.Sprintf("%s-config", ovs.Name)
	return []corev1.Volume{
		{
			Name: "ca-data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: fmt.Sprintf("%s-ca-data", ovs.Name),
				},
			},
		},
		{
			Name: "ssl",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "puppet-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "puppet.conf", Path: "puppet.conf"},
					},
				},
			},
		},
		{
			Name: "puppetdb-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "puppetdb.conf", Path: "puppetdb.conf"},
					},
				},
			},
		},
		{
			Name: "puppetserver-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "puppetserver.conf", Path: "puppetserver.conf"},
					},
				},
			},
		},
		{
			Name: "webserver-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "webserver.conf", Path: "webserver.conf"},
					},
				},
			},
		},
		{
			Name: "product-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "product.conf", Path: "product.conf"},
					},
				},
			},
		},
	}
}

// caLabels returns labels for CA-specific resources.
func caLabels(ovs *openvoxv1alpha1.OpenVoxServer) map[string]string {
	labels := commonLabels(ovs)
	labels["app.kubernetes.io/component"] = "ca"
	return labels
}

func int64Ptr(i int64) *int64 { return &i }
func boolPtr(b bool) *bool    { return &b }
