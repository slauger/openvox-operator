package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// DatabaseReconciler reconciles a Database object.
type DatabaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// Event reasons for Database.
const (
	EventReasonDatabaseRunning        = "DatabaseRunning"
	EventReasonDatabaseError          = "DatabaseError"
	EventReasonDatabaseDeploymentSync = "DatabaseDeploymentSynced"
	EventReasonDatabaseServiceSync    = "DatabaseServiceSynced"
	EventReasonDatabasePDBCreated     = "DatabasePDBCreated"
	EventReasonDatabasePDBUpdated     = "DatabasePDBUpdated"
	EventReasonDatabasePDBDeleted             = "DatabasePDBDeleted"
	EventReasonDatabaseNetworkPolicyCreated   = "DatabaseNetworkPolicyCreated"
	EventReasonDatabaseNetworkPolicyUpdated   = "DatabaseNetworkPolicyUpdated"
	EventReasonDatabaseNetworkPolicyDeleted   = "DatabaseNetworkPolicyDeleted"
)

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=databases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=databases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=databases/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *DatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	db := &openvoxv1alpha1.Database{}
	if err := r.Get(ctx, req.NamespacedName, db); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if db.Status.Phase == "" {
		if err := updateStatusWithRetry(ctx, r.Client, db, func() {
			db.Status.Phase = openvoxv1alpha1.DatabasePhasePending
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve Certificate -- wait until phase is Signed
	cert := &openvoxv1alpha1.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: db.Spec.CertificateRef, Namespace: db.Namespace}, cert); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("waiting for Certificate", "certificateRef", db.Spec.CertificateRef)
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return ctrl.Result{}, err
	}

	if cert.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned || cert.Status.SecretName == "" {
		logger.Info("waiting for Certificate to be signed", "certificate", cert.Name, "phase", cert.Status.Phase)
		if statusErr := updateStatusWithRetry(ctx, r.Client, db, func() {
			db.Status.Phase = openvoxv1alpha1.DatabasePhaseWaitingForCert
		}); statusErr != nil {
			logger.Error(statusErr, "failed to update Database status", "name", db.Name)
		}
		return ctrl.Result{RequeueAfter: RequeueIntervalMedium}, nil
	}

	// Resolve CertificateAuthority via Certificate's authorityRef
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: db.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("waiting for CertificateAuthority", "authorityRef", cert.Spec.AuthorityRef)
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return ctrl.Result{}, err
	}

	// Validate PG credentials Secret exists
	pgSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: db.Spec.Postgres.CredentialsSecretRef, Namespace: db.Namespace}, pgSecret); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("waiting for PostgreSQL credentials Secret", "secretRef", db.Spec.Postgres.CredentialsSecretRef)
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return ctrl.Result{}, err
	}

	// Reconcile ConfigMap (jetty.ini, config.ini)
	if err := r.reconcileConfigMap(ctx, db, cert); err != nil {
		r.Recorder.Eventf(db, nil, corev1.EventTypeWarning, EventReasonDatabaseError, "Reconcile", "Failed to reconcile ConfigMap: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling ConfigMap: %w", err)
	}

	// Reconcile Secret (database.ini with PG credentials)
	if err := r.reconcileDatabaseSecret(ctx, db); err != nil {
		r.Recorder.Eventf(db, nil, corev1.EventTypeWarning, EventReasonDatabaseError, "Reconcile", "Failed to reconcile database Secret: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling database Secret: %w", err)
	}

	// Reconcile Deployment
	if err := r.reconcileDeployment(ctx, db, cert, ca); err != nil {
		r.Recorder.Eventf(db, nil, corev1.EventTypeWarning, EventReasonDatabaseError, "Reconcile", "Failed to reconcile Deployment: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling Deployment: %w", err)
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, db); err != nil {
		r.Recorder.Eventf(db, nil, corev1.EventTypeWarning, EventReasonDatabaseError, "Reconcile", "Failed to reconcile Service: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling Service: %w", err)
	}

	// Reconcile PodDisruptionBudget
	if err := r.reconcilePDB(ctx, db); err != nil {
		r.Recorder.Eventf(db, nil, corev1.EventTypeWarning, EventReasonDatabaseError, "Reconcile", "Failed to reconcile PDB: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling PDB: %w", err)
	}

	// Reconcile NetworkPolicy
	if err := r.reconcileNetworkPolicy(ctx, db); err != nil {
		r.Recorder.Eventf(db, nil, corev1.EventTypeWarning, EventReasonDatabaseError, "Reconcile", "Failed to reconcile NetworkPolicy: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling NetworkPolicy: %w", err)
	}

	// Update status
	ready := r.getReadyReplicas(ctx, db)
	if err := updateStatusWithRetry(ctx, r.Client, db, func() {
		replicas := int32(1)
		if db.Spec.Replicas != nil {
			replicas = *db.Spec.Replicas
		}
		db.Status.Desired = replicas
		db.Status.Ready = ready

		port := db.Spec.Service.Port
		if port == 0 {
			port = DatabaseHTTPSPort
		}
		db.Status.URL = fmt.Sprintf("https://%s.%s.svc.cluster.local:%d", db.Name, db.Namespace, port)

		if ready > 0 {
			db.Status.Phase = openvoxv1alpha1.DatabasePhaseRunning
		} else {
			db.Status.Phase = openvoxv1alpha1.DatabasePhasePending
		}
	}); err != nil {
		return ctrl.Result{}, err
	}

	if ready > 0 {
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseRunning, "Reconcile", "Database reconciled successfully")
	}
	return ctrl.Result{}, nil
}

func (r *DatabaseReconciler) reconcileConfigMap(ctx context.Context, db *openvoxv1alpha1.Database, cert *openvoxv1alpha1.Certificate) error {
	logger := log.FromContext(ctx)
	cmName := fmt.Sprintf("%s-config", db.Name)
	labels := databaseLabels(db.Name)

	data := map[string]string{
		"jetty.ini":  renderJettyIni(cert.Spec.Certname),
		"config.ini": renderConfigIni(),
		"auth.conf":  renderAuthConf(),
	}

	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: db.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating Database ConfigMap", "name", cmName)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: db.Namespace,
				Labels:    labels,
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(db, cm, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, cm)
	} else if err != nil {
		return err
	}

	existing.Data = data
	return r.Update(ctx, existing)
}

func (r *DatabaseReconciler) reconcileDatabaseSecret(ctx context.Context, db *openvoxv1alpha1.Database) error {
	dbIni, err := r.renderDatabaseIni(ctx, db)
	if err != nil {
		return err
	}

	secretName := fmt.Sprintf("%s-db", db.Name)
	labels := databaseLabels(db.Name)

	return createOrUpdateSecret(ctx, r.Client, r.Scheme, db,
		secretName, db.Namespace, labels,
		map[string][]byte{"database.ini": []byte(dbIni)},
	)
}

func (r *DatabaseReconciler) reconcileService(ctx context.Context, db *openvoxv1alpha1.Database) error {
	logger := log.FromContext(ctx)
	svcName := db.Name
	labels := databaseLabels(db.Name)

	port := db.Spec.Service.Port
	if port == 0 {
		port = DatabaseHTTPSPort
	}

	svcType := db.Spec.Service.Type
	if svcType == "" {
		svcType = corev1.ServiceTypeClusterIP
	}

	svcAnnotations := db.Spec.Service.Annotations

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: db.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating Database Service", "name", svcName)
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:        svcName,
				Namespace:   db.Namespace,
				Labels:      labels,
				Annotations: svcAnnotations,
			},
			Spec: corev1.ServiceSpec{
				Type: svcType,
				Selector: map[string]string{
					LabelDatabase: db.Name,
				},
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       port,
						TargetPort: intstr.FromInt32(DatabaseHTTPSPort),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}
		if err := controllerutil.SetControllerReference(db, svc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	} else if err != nil {
		return err
	}

	// Update existing
	existing.Spec.Type = svcType
	existing.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       port,
			TargetPort: intstr.FromInt32(DatabaseHTTPSPort),
			Protocol:   corev1.ProtocolTCP,
		},
	}
	existing.Annotations = svcAnnotations
	return r.Update(ctx, existing)
}

func (r *DatabaseReconciler) getReadyReplicas(ctx context.Context, db *openvoxv1alpha1.Database) int32 {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: db.Name, Namespace: db.Namespace}, deploy); err != nil {
		return 0
	}
	return deploy.Status.ReadyReplicas
}

func (r *DatabaseReconciler) reconcilePDB(ctx context.Context, db *openvoxv1alpha1.Database) error {
	logger := log.FromContext(ctx)
	pdbName := db.Name
	existing := &policyv1.PodDisruptionBudget{}
	err := r.Get(ctx, types.NamespacedName{Name: pdbName, Namespace: db.Namespace}, existing)

	// If PDB is disabled or not configured, delete if exists
	if db.Spec.PDB == nil || !db.Spec.PDB.Enabled {
		if err == nil {
			logger.Info("deleting PDB (disabled)", "name", pdbName)
			if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
				return err
			}
			r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabasePDBDeleted, "Reconcile", "PodDisruptionBudget %s deleted", pdbName)
		}
		return nil
	}

	desired, buildErr := r.buildPDB(db)
	if buildErr != nil {
		return buildErr
	}
	if errors.IsNotFound(err) {
		logger.Info("creating PDB", "name", pdbName)
		if err := r.Create(ctx, desired); err != nil {
			return err
		}
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabasePDBCreated, "Reconcile", "PodDisruptionBudget %s created", pdbName)
		return nil
	}
	if err != nil {
		return err
	}

	// Update existing
	existing.Spec = desired.Spec
	if err := r.Update(ctx, existing); err != nil {
		return err
	}
	r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabasePDBUpdated, "Reconcile", "PodDisruptionBudget %s updated", pdbName)
	return nil
}

func (r *DatabaseReconciler) buildPDB(db *openvoxv1alpha1.Database) (*policyv1.PodDisruptionBudget, error) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    databaseLabels(db.Name),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelDatabase: db.Name,
				},
			},
		},
	}
	if db.Spec.PDB.MinAvailable != nil {
		pdb.Spec.MinAvailable = db.Spec.PDB.MinAvailable
	} else if db.Spec.PDB.MaxUnavailable != nil {
		pdb.Spec.MaxUnavailable = db.Spec.PDB.MaxUnavailable
	} else {
		minAvailable := intstrInt(DefaultPDBMinAvailable)
		pdb.Spec.MinAvailable = &minAvailable
	}
	if err := controllerutil.SetControllerReference(db, pdb, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on PDB: %w", err)
	}
	return pdb, nil
}

func (r *DatabaseReconciler) reconcileNetworkPolicy(ctx context.Context, db *openvoxv1alpha1.Database) error {
	logger := log.FromContext(ctx)
	npName := db.Name + "-netpol"
	existing := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, types.NamespacedName{Name: npName, Namespace: db.Namespace}, existing)

	if db.Spec.NetworkPolicy == nil || !db.Spec.NetworkPolicy.Enabled {
		if err == nil {
			logger.Info("deleting NetworkPolicy (disabled)", "name", npName)
			if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
				return err
			}
			r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseNetworkPolicyDeleted, "Reconcile", "NetworkPolicy %s deleted", npName)
		}
		return nil
	}

	desired, buildErr := r.buildNetworkPolicy(db)
	if buildErr != nil {
		return buildErr
	}
	if errors.IsNotFound(err) {
		logger.Info("creating NetworkPolicy", "name", npName)
		if err := r.Create(ctx, desired); err != nil {
			return err
		}
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseNetworkPolicyCreated, "Reconcile", "NetworkPolicy %s created", npName)
		return nil
	}
	if err != nil {
		return err
	}

	existing.Spec = desired.Spec
	if err := r.Update(ctx, existing); err != nil {
		return err
	}
	r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseNetworkPolicyUpdated, "Reconcile", "NetworkPolicy %s updated", npName)
	return nil
}

func (r *DatabaseReconciler) buildNetworkPolicy(db *openvoxv1alpha1.Database) (*networkingv1.NetworkPolicy, error) {
	port8081 := intstr.FromInt32(DatabaseHTTPSPort)
	tcp := corev1.ProtocolTCP

	ingress := []networkingv1.NetworkPolicyIngressRule{
		{
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &tcp, Port: &port8081},
			},
			From: []networkingv1.NetworkPolicyPeer{
				{
					PodSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app.kubernetes.io/name": "openvox",
						},
					},
				},
			},
		},
	}
	if db.Spec.NetworkPolicy.AdditionalIngress != nil {
		ingress = append(ingress, db.Spec.NetworkPolicy.AdditionalIngress...)
	}

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name + "-netpol",
			Namespace: db.Namespace,
			Labels:    databaseLabels(db.Name),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{LabelDatabase: db.Name},
			},
			Ingress:     ingress,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
	if err := controllerutil.SetControllerReference(db, np, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on NetworkPolicy: %w", err)
	}
	return np, nil
}

func (r *DatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Database{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(&corev1.Secret{}, enqueueDatabasesForSecret(mgr.GetClient())).
		Complete(r)
}
