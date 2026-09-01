package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

const conflictHostname = "puppet.example.com"

// TestPoolConflict_ReportedAsConditionNotRetried is the core of the change: a
// duplicate hostname is a permanent condition. Returning an error made
// controller-runtime retry it with exponential backoff forever, which produced
// nothing but a growing backlog of identical events.
func TestPoolConflict_ReportedAsConditionNotRetried(t *testing.T) {
	winner := newPool("pool-a", withRoute(true, conflictHostname, "gw"))
	loser := newPool("pool-b", withRoute(true, conflictHostname, "gw"))

	c := setupTestClient(winner, loser)
	r := newPoolReconciler(c, true)

	res, err := r.Reconcile(testCtx(), testRequest("pool-b"))
	if err != nil {
		t.Fatalf("a hostname conflict must not fail the reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("a hostname conflict must not be polled, got RequeueAfter %v", res.RequeueAfter)
	}

	got := &openvoxv1alpha1.Pool{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "pool-b", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Pool back: %v", err)
	}

	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionPoolReady)
	if cond == nil {
		t.Fatal("expected a PoolReady condition reporting the conflict")
	}
	if cond.Status != metav1.ConditionFalse || cond.Reason != "HostnameConflict" {
		t.Errorf("expected Ready=False/HostnameConflict, got %s/%s", cond.Status, cond.Reason)
	}
	if !strings.Contains(cond.Message, "pool-a") {
		t.Errorf("the condition must name the Pool holding the hostname, got %q", cond.Message)
	}
}

// TestPoolConflict_OlderPoolKeepsTheHostname pins the tie-break. Both Pools see
// the same list, so both must reach the same verdict, otherwise they take turns
// creating and deleting the TLSRoute.
func TestPoolConflict_OlderPoolKeepsTheHostname(t *testing.T) {
	older := newPool("zzz-first", withRoute(true, conflictHostname, "gw"))
	older.CreationTimestamp = metav1.NewTime(time.Unix(1_700_000_000, 0))
	younger := newPool("aaa-second", withRoute(true, conflictHostname, "gw"))
	younger.CreationTimestamp = metav1.NewTime(time.Unix(1_700_000_100, 0))

	c := setupTestClient(older, younger)
	r := newPoolReconciler(c, true)

	// Reconcile both, in the order that would favour the younger one if the
	// decision depended on who runs first.
	for _, name := range []string{"aaa-second", "zzz-first"} {
		if _, err := r.Reconcile(testCtx(), testRequest(name)); err != nil {
			t.Fatalf("reconcile %s: %v", name, err)
		}
	}

	route := &gwapiv1.TLSRoute{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "zzz-first", Namespace: testNamespace}, route); err != nil {
		t.Errorf("the older Pool must keep the TLSRoute, got: %v", err)
	}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "aaa-second", Namespace: testNamespace}, &gwapiv1.TLSRoute{}); err == nil {
		t.Error("the younger Pool must not hold a TLSRoute for a hostname it lost")
	}

	// The name would have picked the other way round, which is what makes this
	// a real check of the timestamp ordering.
	loser := &openvoxv1alpha1.Pool{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "aaa-second", Namespace: testNamespace}, loser); err != nil {
		t.Fatalf("reading the younger Pool back: %v", err)
	}
	if cond := meta.FindStatusCondition(loser.Status.Conditions, openvoxv1alpha1.ConditionPoolReady); cond == nil || cond.Reason != "HostnameConflict" {
		t.Errorf("expected the younger Pool to report the conflict, got %+v", cond)
	}
}

// TestPoolConflict_LoserGivesUpItsRoute covers the case that produced two
// TLSRoutes for one hostname: a Pool that held the route before an older
// claimant enabled its own must release it.
func TestPoolConflict_LoserGivesUpItsRoute(t *testing.T) {
	loser := newPool("pool-b", withRoute(true, conflictHostname, "gw"))

	c := setupTestClient(loser)
	r := newPoolReconciler(c, true)

	if _, err := r.Reconcile(testCtx(), testRequest("pool-b")); err != nil {
		t.Fatalf("reconcile pool-b: %v", err)
	}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "pool-b", Namespace: testNamespace}, &gwapiv1.TLSRoute{}); err != nil {
		t.Fatalf("pool-b should own the TLSRoute while it is alone: %v", err)
	}

	// pool-a takes the hostname on the tie-break.
	winner := newPool("pool-a", withRoute(true, conflictHostname, "gw"))
	if err := c.Create(testCtx(), winner); err != nil {
		t.Fatalf("creating pool-a: %v", err)
	}

	if _, err := r.Reconcile(testCtx(), testRequest("pool-b")); err != nil {
		t.Fatalf("reconcile pool-b after the conflict appeared: %v", err)
	}

	if err := c.Get(testCtx(), types.NamespacedName{Name: "pool-b", Namespace: testNamespace}, &gwapiv1.TLSRoute{}); err == nil {
		t.Error("the losing Pool must release the TLSRoute it no longer owns")
	}
}

// TestPoolConflict_TerminatingPoolReleasesTheHostname keeps a stuck deletion
// from holding a hostname hostage.
func TestPoolConflict_TerminatingPoolReleasesTheHostname(t *testing.T) {
	now := metav1.Now()
	leaving := newPool("pool-a", withRoute(true, conflictHostname, "gw"))
	leaving.DeletionTimestamp = &now
	leaving.Finalizers = []string{"example.com/keep-around"}
	successor := newPool("pool-b", withRoute(true, conflictHostname, "gw"))

	c := setupTestClient(leaving, successor)
	r := newPoolReconciler(c, true)

	if _, err := r.Reconcile(testCtx(), testRequest("pool-b")); err != nil {
		t.Fatalf("reconcile pool-b: %v", err)
	}

	if err := c.Get(testCtx(), types.NamespacedName{Name: "pool-b", Namespace: testNamespace}, &gwapiv1.TLSRoute{}); err != nil {
		t.Errorf("a terminating Pool must not keep the hostname, got: %v", err)
	}
}

// TestPoolConflict_EventFiresOncePerTransition guards against the event noise
// the old backoff loop produced.
func TestPoolConflict_EventFiresOncePerTransition(t *testing.T) {
	winner := newPool("pool-a", withRoute(true, conflictHostname, "gw"))
	loser := newPool("pool-b", withRoute(true, conflictHostname, "gw"))

	c := setupTestClient(winner, loser)
	r := newPoolReconciler(c, true)
	rec := events.NewFakeRecorder(100)
	r.Recorder = rec

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(testCtx(), testRequest("pool-b")); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}

	if n := countEvents(rec, EventReasonHostnameConflict); n != 1 {
		t.Errorf("expected exactly one conflict event across three reconciles, got %d", n)
	}
}

// countEvents drains the recorder and counts the events carrying the given
// reason, so unrelated events from the same reconcile do not distort the count.
func countEvents(rec *events.FakeRecorder, reason string) int {
	n := 0
	for {
		select {
		case e := <-rec.Events:
			if strings.Contains(e, reason) {
				n++
			}
		default:
			return n
		}
	}
}

// TestPoolConflict_ResolvedWhenTheWinnerGivesUp closes the loop: once the
// hostname is free the Pool must pick it up, which is what the sibling watch is
// there to trigger.
func TestPoolConflict_ResolvedWhenTheWinnerGivesUp(t *testing.T) {
	winner := newPool("pool-a", withRoute(true, conflictHostname, "gw"))
	loser := newPool("pool-b", withRoute(true, conflictHostname, "gw"))

	c := setupTestClient(winner, loser)
	r := newPoolReconciler(c, true)

	if _, err := r.Reconcile(testCtx(), testRequest("pool-b")); err != nil {
		t.Fatalf("reconcile pool-b: %v", err)
	}

	if err := c.Delete(testCtx(), winner); err != nil {
		t.Fatalf("deleting pool-a: %v", err)
	}

	if _, err := r.Reconcile(testCtx(), testRequest("pool-b")); err != nil {
		t.Fatalf("reconcile pool-b after the conflict was resolved: %v", err)
	}

	if err := c.Get(testCtx(), types.NamespacedName{Name: "pool-b", Namespace: testNamespace}, &gwapiv1.TLSRoute{}); err != nil {
		t.Errorf("the Pool must take the hostname once it is free, got: %v", err)
	}

	got := &openvoxv1alpha1.Pool{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "pool-b", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Pool back: %v", err)
	}
	if cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionPoolReady); cond != nil && cond.Reason == "HostnameConflict" {
		t.Error("the conflict condition must clear once the hostname is free")
	}
}

// TestPoolsSharingHostname checks the watch that makes the resolution
// above happen without polling.
func TestPoolsSharingHostname(t *testing.T) {
	changed := newPool("pool-a", withRoute(true, conflictHostname, "gw"))
	sibling := newPool("pool-b", withRoute(true, conflictHostname, "gw"))
	unrelated := newPool("pool-c", withRoute(true, "other.example.com", "gw"))
	noRoute := newPool("pool-d")

	c := setupTestClient(changed, sibling, unrelated, noRoute)

	got := poolsSharingHostname(c)(testCtx(), changed)
	if !equalNames(got, "pool-b") {
		t.Errorf("expected only the Pool competing for the same hostname, got %v", names(got))
	}
}

// TestPoolConflict_SurvivesRouteRemovedMidReconcile guards a nil dereference
// that is easy to reintroduce: updateStatusWithRetry re-reads the whole object
// on every attempt, so a Pool whose route is removed while the reconcile is in
// flight comes back with a nil Spec.Route. Reporting the conflict must not
// reach into it.
func TestPoolConflict_SurvivesRouteRemovedMidReconcile(t *testing.T) {
	winner := newPool("pool-a", withRoute(true, conflictHostname, "gw"))
	loser := newPool("pool-b", withRoute(true, conflictHostname, "gw"))

	gets := 0
	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(winner, loser).
		WithStatusSubresource(&openvoxv1alpha1.Pool{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := cl.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				// The first read is the one the reconcile starts from; every
				// later read models the spec having changed in the meantime.
				if p, ok := obj.(*openvoxv1alpha1.Pool); ok {
					gets++
					if gets > 1 {
						p.Spec.Route = nil
					}
				}
				return nil
			},
		}).
		Build()

	r := newPoolReconciler(c, true)
	if _, err := r.Reconcile(testCtx(), testRequest("pool-b")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}
