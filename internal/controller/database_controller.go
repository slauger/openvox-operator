package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
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
	EventReasonDatabaseRunning              = "DatabaseRunning"
	EventReasonDatabaseError                = "DatabaseError"
	EventReasonDatabaseDeploymentSync       = "DatabaseDeploymentSynced"
	EventReasonDatabaseServiceSync          = "DatabaseServiceSynced"
	EventReasonDatabasePDBCreated           = "DatabasePDBCreated"
	EventReasonDatabasePDBUpdated           = "DatabasePDBUpdated"
	EventReasonDatabasePDBDeleted           = "DatabasePDBDeleted"
	EventReasonDatabaseNetworkPolicyCreated = "DatabaseNetworkPolicyCreated"
	EventReasonDatabaseNetworkPolicyUpdated = "DatabaseNetworkPolicyUpdated"
	EventReasonDatabaseNetworkPolicyDeleted = "DatabaseNetworkPolicyDeleted"
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
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting Database %s: %w", req.NamespacedName, err)
	}

	// Pausing comes after the deletion path: a paused resource must still be
	// deletable, otherwise the annotation turns into a trap.
	if paused, err := reconcilePauseState(ctx, r.Client, db, &db.Status.Conditions); err != nil {
		return ctrl.Result{}, err
	} else if paused {
		logger.Info("reconciliation paused by annotation", "name", db.Name)
		return ctrl.Result{}, nil
	}

	// Set initial phase
	if db.Status.Phase == "" {
		if err := updateStatusWithRetry(ctx, r.Client, db, func() {
			db.Status.Phase = openvoxv1alpha1.DatabasePhasePending
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating initial status for Database %s: %w", db.Name, err)
		}
	}

	// Resolve Certificate -- wait until phase is Signed
	cert := &openvoxv1alpha1.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: db.Spec.CertificateRef, Namespace: db.Namespace}, cert); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("waiting for Certificate", "certificateRef", db.Spec.CertificateRef)
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting Certificate %s: %w", db.Spec.CertificateRef, err)
	}

	if !certificateUsable(cert) {
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
		if apierrors.IsNotFound(err) {
			logger.Info("waiting for CertificateAuthority", "authorityRef", cert.Spec.AuthorityRef)
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting CertificateAuthority %s: %w", cert.Spec.AuthorityRef, err)
	}

	// Validate PG credentials Secret exists
	pgSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: db.Spec.Postgres.CredentialsSecretRef, Namespace: db.Namespace}, pgSecret); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("waiting for PostgreSQL credentials Secret", "secretRef", db.Spec.Postgres.CredentialsSecretRef)
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting Secret %s: %w", db.Spec.Postgres.CredentialsSecretRef, err)
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
	ready, err := r.getReadyReplicas(ctx, db)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reading ready replicas for Database %s: %w", db.Name, err)
	}
	if err := updateStatusWithRetry(ctx, r.Client, db, func() {
		replicas := int32(1)
		if db.Spec.Replicas != nil {
			replicas = *db.Spec.Replicas
		}
		db.Status.ObservedGeneration = db.Generation
		db.Status.Desired = replicas
		db.Status.Ready = ready

		port := db.Spec.Service.Port
		if port == 0 {
			port = DatabaseHTTPSPort
		}
		db.Status.URL = fmt.Sprintf("https://%s.%s.svc.cluster.local:%d", db.Name, db.Namespace, port)

		if ready > 0 {
			db.Status.Phase = openvoxv1alpha1.DatabasePhaseRunning
			meta.SetStatusCondition(&db.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionDatabaseReady,
				Status:             metav1.ConditionTrue,
				Reason:             "ReplicasReady",
				Message:            fmt.Sprintf("%d/%d replicas ready", ready, replicas),
				ObservedGeneration: db.Generation,
			})
		} else {
			db.Status.Phase = openvoxv1alpha1.DatabasePhasePending
			meta.SetStatusCondition(&db.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionDatabaseReady,
				Status:             metav1.ConditionFalse,
				Reason:             "ReplicasNotReady",
				Message:            fmt.Sprintf("0/%d replicas ready", replicas),
				ObservedGeneration: db.Generation,
			})
		}
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status for Database %s: %w", db.Name, err)
	}

	if ready > 0 {
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseRunning, "Reconcile", "Database reconciled successfully")
	}
	return ctrl.Result{}, nil
}

func (r *DatabaseReconciler) reconcileConfigMap(ctx context.Context, db *openvoxv1alpha1.Database, cert *openvoxv1alpha1.Certificate) error {
	logger := log.FromContext(ctx)
	cmName := fmt.Sprintf("%s-config", db.Name)

	data := map[string]string{
		"jetty.ini":  renderJettyIni(cert.Spec.Certname),
		"config.ini": renderConfigIni(),
		"auth.conf":  renderAuthConf(),
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: db.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if err := assertControlledBy(cm, db, "ConfigMap"); err != nil {
			return err
		}
		cm.Labels = databaseLabels(db.Name)
		cm.Data = data
		return controllerutil.SetControllerReference(db, cm, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling ConfigMap %s: %w", cmName, err)
	}
	if op == controllerutil.OperationResultCreated {
		logger.Info("created Database ConfigMap", "name", cmName)
	}
	return nil
}

func (r *DatabaseReconciler) reconcileDatabaseSecret(ctx context.Context, db *openvoxv1alpha1.Database) error {
	dbIni, err := r.renderDatabaseIni(ctx, db)
	if err != nil {
		return fmt.Errorf("rendering database.ini for Database %s: %w", db.Name, err)
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

	port := db.Spec.Service.Port
	if port == 0 {
		port = DatabaseHTTPSPort
	}
	svcType := db.Spec.Service.Type
	if svcType == "" {
		svcType = corev1.ServiceTypeClusterIP
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: db.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := assertControlledBy(svc, db, "Service"); err != nil {
			return err
		}
		svc.Labels = databaseLabels(db.Name)
		svc.Annotations = db.Spec.Service.Annotations

		// clusterIP and any assigned nodePort are Kubernetes' to manage, so only
		// the fields the Database owns are written.
		svc.Spec.Type = svcType
		svc.Spec.Selector = map[string]string{LabelDatabase: db.Name}
		if len(svc.Spec.Ports) == 0 {
			svc.Spec.Ports = []corev1.ServicePort{{}}
		}
		svc.Spec.Ports[0].Name = "https"
		svc.Spec.Ports[0].Port = port
		svc.Spec.Ports[0].TargetPort = intstr.FromInt32(DatabaseHTTPSPort)
		svc.Spec.Ports[0].Protocol = corev1.ProtocolTCP
		return controllerutil.SetControllerReference(db, svc, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling Service %s: %w", svcName, err)
	}
	switch op {
	case controllerutil.OperationResultCreated:
		logger.Info("created Database Service", "name", svcName)
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseServiceSync, "Reconcile", "Service %s created", svcName)
	case controllerutil.OperationResultUpdated:
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseServiceSync, "Reconcile", "Service %s updated", svcName)
	}
	return nil
}

// getReadyReplicas reports how many replicas of the Database Deployment are
// ready. A missing Deployment counts as zero; any other error is returned so a
// transient lookup failure does not masquerade as an idle workload.
func (r *DatabaseReconciler) getReadyReplicas(ctx context.Context, db *openvoxv1alpha1.Database) (int32, error) {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: db.Name, Namespace: db.Namespace}, deploy); err != nil {
		if apierrors.IsNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("getting Deployment %s: %w", db.Name, err)
	}
	return deploy.Status.ReadyReplicas, nil
}

func (r *DatabaseReconciler) reconcilePDB(ctx context.Context, db *openvoxv1alpha1.Database) error {
	logger := log.FromContext(ctx)
	pdbName := db.Name

	if db.Spec.PDB == nil || !db.Spec.PDB.Enabled {
		existing := &policyv1.PodDisruptionBudget{}
		err := r.Get(ctx, types.NamespacedName{Name: pdbName, Namespace: db.Namespace}, existing)
		if err == nil {
			if guardErr := assertControlledBy(existing, db, "PodDisruptionBudget"); guardErr != nil {
				return guardErr
			}
			logger.Info("deleting Database PDB (disabled)", "name", pdbName)
			if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting PodDisruptionBudget %s: %w", pdbName, err)
			}
			r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabasePDBDeleted, "Reconcile", "PodDisruptionBudget %s deleted", pdbName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting PodDisruptionBudget %s: %w", pdbName, err)
		}
		return nil
	}

	desired, buildErr := r.buildPDB(db)
	if buildErr != nil {
		return buildErr
	}

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: pdbName, Namespace: db.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, pdb, func() error {
		if err := assertControlledBy(pdb, db, "PodDisruptionBudget"); err != nil {
			return err
		}
		pdb.Labels = desired.Labels
		pdb.Spec = desired.Spec
		return controllerutil.SetControllerReference(db, pdb, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling PodDisruptionBudget %s: %w", pdbName, err)
	}
	switch op {
	case controllerutil.OperationResultCreated:
		logger.Info("created Database PDB", "name", pdbName)
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabasePDBCreated, "Reconcile", "PodDisruptionBudget %s created", pdbName)
	case controllerutil.OperationResultUpdated:
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabasePDBUpdated, "Reconcile", "PodDisruptionBudget %s updated", pdbName)
	}
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
	switch {
	case db.Spec.PDB.MinAvailable != nil:
		pdb.Spec.MinAvailable = db.Spec.PDB.MinAvailable
	case db.Spec.PDB.MaxUnavailable != nil:
		pdb.Spec.MaxUnavailable = db.Spec.PDB.MaxUnavailable
	default:
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

	if db.Spec.NetworkPolicy == nil || !db.Spec.NetworkPolicy.Enabled {
		existing := &networkingv1.NetworkPolicy{}
		err := r.Get(ctx, types.NamespacedName{Name: npName, Namespace: db.Namespace}, existing)
		if err == nil {
			if guardErr := assertControlledBy(existing, db, "NetworkPolicy"); guardErr != nil {
				return guardErr
			}
			logger.Info("deleting Database NetworkPolicy (disabled)", "name", npName)
			if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting NetworkPolicy %s: %w", npName, err)
			}
			r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseNetworkPolicyDeleted, "Reconcile", "NetworkPolicy %s deleted", npName)
		} else if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting NetworkPolicy %s: %w", npName, err)
		}
		return nil
	}

	desired, buildErr := r.buildNetworkPolicy(db)
	if buildErr != nil {
		return buildErr
	}

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: npName, Namespace: db.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		if err := assertControlledBy(np, db, "NetworkPolicy"); err != nil {
			return err
		}
		np.Labels = desired.Labels
		np.Spec = desired.Spec
		return controllerutil.SetControllerReference(db, np, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling NetworkPolicy %s: %w", npName, err)
	}
	switch op {
	case controllerutil.OperationResultCreated:
		logger.Info("created Database NetworkPolicy", "name", npName)
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseNetworkPolicyCreated, "Reconcile", "NetworkPolicy %s created", npName)
	case controllerutil.OperationResultUpdated:
		r.Recorder.Eventf(db, nil, corev1.EventTypeNormal, EventReasonDatabaseNetworkPolicyUpdated, "Reconcile", "NetworkPolicy %s updated", npName)
	}
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
		Watches(&openvoxv1alpha1.Certificate{}, handler.EnqueueRequestsFromMapFunc(
			enqueueDatabasesForCertificate(mgr.GetClient()),
		)).
		Complete(r)
}
