/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	knativev1 "github.com/JeremyPDonahue/knative-route-sync/api/knative/v1"
	routev1 "github.com/JeremyPDonahue/knative-route-sync/api/openshift/route/v1"
)

const (
	finalizerName       = "knative-route-sync.io/finalizer"
	kourierExternalName = "kourier-internal.kourier-system.svc.cluster.local"
	routePrefix         = "knative-route-"
)

// KnativeServiceReconciler watches Knative Services and manages corresponding OpenShift Routes.
type KnativeServiceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=serving.knative.dev,resources=services/finalizers,verbs=update
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes/custom-host,verbs=create;update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *KnativeServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var ksvc knativev1.Service
	if err := r.Get(ctx, req.NamespacedName, &ksvc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	resourceName := routePrefix + ksvc.Name

	if !ksvc.DeletionTimestamp.IsZero() {
		log.Info("Knative Service deleted, cleaning up Route resources", "name", ksvc.Name)
		if err := r.deleteRouteResources(ctx, ksvc.Namespace, resourceName); err != nil {
			r.Recorder.Eventf(&ksvc, corev1.EventTypeWarning, "CleanupFailed", "Failed to clean up Route resources: %s", err)
			return ctrl.Result{}, err
		}
		r.Recorder.Event(&ksvc, corev1.EventTypeNormal, "CleanedUp", "Route resources deleted successfully")
		controllerutil.RemoveFinalizer(&ksvc, finalizerName)
		return ctrl.Result{}, r.Update(ctx, &ksvc)
	}

	if !controllerutil.ContainsFinalizer(&ksvc, finalizerName) {
		controllerutil.AddFinalizer(&ksvc, finalizerName)
		return ctrl.Result{}, r.Update(ctx, &ksvc)
	}

	if !ksvc.IsReady() {
		log.Info("Knative Service not ready, skipping reconcile", "name", ksvc.Name)
		return ctrl.Result{}, nil
	}

	if err := r.ensureBridgeService(ctx, &ksvc, resourceName); err != nil {
		r.Recorder.Eventf(&ksvc, corev1.EventTypeWarning, "ReconcileFailed", "Failed to reconcile bridge Service: %s", err)
		return ctrl.Result{}, err
	}

	if err := r.ensureRoute(ctx, &ksvc, resourceName); err != nil {
		r.Recorder.Eventf(&ksvc, corev1.EventTypeWarning, "ReconcileFailed", "Failed to reconcile Route: %s", err)
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(&ksvc, corev1.EventTypeNormal, "RouteReconciled", "Route resources reconciled successfully: %s", resourceName)
	log.Info("Route resources reconciled", "route", resourceName, "namespace", ksvc.Namespace)
	return ctrl.Result{}, nil
}

// ensureBridgeService creates/updates an ExternalName Service in the Knative Service's namespace
// that resolves to Kourier's internal service, allowing the Route to target Kourier cross-namespace.
func (r *KnativeServiceReconciler) ensureBridgeService(ctx context.Context, ksvc *knativev1.Service, name string) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ksvc.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(ksvc, svc, r.Scheme); err != nil {
			return err
		}
		svc.Spec.Type = corev1.ServiceTypeExternalName
		svc.Spec.ExternalName = kourierExternalName
		svc.Spec.Ports = []corev1.ServicePort{
			{Port: 80, TargetPort: intstr.FromInt32(80), Protocol: corev1.ProtocolTCP},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconciling bridge Service: %w", err)
	}
	return nil
}

func (r *KnativeServiceReconciler) ensureRoute(ctx context.Context, ksvc *knativev1.Service, name string) error {
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ksvc.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		w := int32(100)

		if err := controllerutil.SetControllerReference(ksvc, route, r.Scheme); err != nil {
			return err
		}

		// spec.host is immutable after creation in OpenShift; only set it for new Routes.
		if route.Spec.Host == "" {
			host, err := hostFromKsvc(ksvc)
			if err != nil {
				return err
			}
			route.Spec.Host = host
		}

		route.Spec.To = routev1.RouteTargetReference{
			Kind:   "Service",
			Name:   name,
			Weight: &w,
		}
		route.Spec.Port = &routev1.RoutePort{
			TargetPort: intstr.FromInt32(80),
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconciling Route: %w", err)
	}
	return nil
}

func (r *KnativeServiceReconciler) deleteRouteResources(ctx context.Context, namespace, name string) error {
	route := &routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := r.Delete(ctx, route); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("deleting Route: %w", err)
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := r.Delete(ctx, svc); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("deleting bridge Service: %w", err)
	}

	return nil
}

// hostFromKsvc returns the hostname for the Route, derived from the Knative Service's status URL.
func hostFromKsvc(ksvc *knativev1.Service) (string, error) {
	u := ksvc.Status.URL
	if u == "" {
		return "", fmt.Errorf("knative Service %s/%s has no status URL", ksvc.Namespace, ksvc.Name)
	}
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return u, nil
}

func (r *KnativeServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&knativev1.Service{}).
		Owns(&corev1.Service{}).
		Owns(&routev1.Route{}).
		Named("knativeservice").
		Complete(r)
}
