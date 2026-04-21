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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	knativev1 "github.com/JeremyPDonahue/knative-route-sync/api/knative/v1"
	routev1 "github.com/JeremyPDonahue/knative-route-sync/api/openshift/route/v1"
)

const (
	finalizerName      = "knative-route-sync.io/finalizer"
	kourierNamespace   = "knative-serving"
	kourierServiceName = "kourier-internal"
	routePrefix        = "knative-route-"
)

// KnativeServiceReconciler watches Knative Services and manages corresponding OpenShift Routes.
type KnativeServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=serving.knative.dev,resources=services,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch;create;update;patch;delete

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
			return ctrl.Result{}, err
		}
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

	kourierIP, err := r.getKourierClusterIP(ctx)
	if err != nil {
		log.Error(err, "Failed to get Kourier ClusterIP")
		return ctrl.Result{}, err
	}

	if err := r.ensureBridgeService(ctx, &ksvc, resourceName, kourierIP); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.ensureRoute(ctx, &ksvc, resourceName); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Route resources reconciled", "route", resourceName, "namespace", ksvc.Namespace)
	return ctrl.Result{}, nil
}

func (r *KnativeServiceReconciler) getKourierClusterIP(ctx context.Context) (string, error) {
	var svc corev1.Service
	if err := r.Get(ctx, types.NamespacedName{Namespace: kourierNamespace, Name: kourierServiceName}, &svc); err != nil {
		return "", fmt.Errorf("getting Kourier service %s/%s: %w", kourierNamespace, kourierServiceName, err)
	}
	if svc.Spec.ClusterIP == "" || svc.Spec.ClusterIP == "None" {
		return "", fmt.Errorf("Kourier service %s/%s has no ClusterIP", kourierNamespace, kourierServiceName)
	}
	return svc.Spec.ClusterIP, nil
}

// ensureBridgeService creates/updates a ClusterIP Service and manual Endpoints in the Knative
// Service's namespace so the Route can target Kourier's ClusterIP across namespaces.
func (r *KnativeServiceReconciler) ensureBridgeService(ctx context.Context, ksvc *knativev1.Service, name, kourierIP string) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ksvc.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		
		if err := controllerutil.SetControllerReference(ksvc, svc, r.Scheme); err != nil {
    		return err
		}
		
		svc.Spec.Ports = []corev1.ServicePort{
				{Port: 80, TargetPort: intstr.FromInt32(80), Protocol: corev1.ProtocolTCP},
			}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconciling bridge Service: %w", err)
	}

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ksvc.Namespace},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, endpoints, func() error {
		
		if err := controllerutil.SetControllerReference(ksvc, endpoints, r.Scheme); err != nil {
    		return err
		}
		
		endpoints.Subsets = []corev1.EndpointSubset{

			{
				Addresses: []corev1.EndpointAddress{{IP: kourierIP}},
				Ports:     []corev1.EndpointPort{{Port: 80, Protocol: corev1.ProtocolTCP}},
			},
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconciling bridge Endpoints: %w", err)
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
		
		route.Spec = routev1.RouteSpec{
			Host: hostFromKsvc(ksvc),
			To: routev1.RouteTargetReference{
				Kind:   "Service",
				Name:   name,
				Weight: &w,
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt32(80),
			},
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

	endpoints := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := r.Delete(ctx, endpoints); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("deleting bridge Endpoints: %w", err)
	}

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := r.Delete(ctx, svc); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("deleting bridge Service: %w", err)
	}

	return nil
}

// hostFromKsvc returns the hostname for the Route, derived from the Knative Service's status URL.
func hostFromKsvc(ksvc *knativev1.Service) string {
	u := ksvc.Status.URL
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return u
}

func (r *KnativeServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&knativev1.Service{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Endpoints{}).
		Owns(&routev1.Route{}).
		Named("knativeservice").
		Complete(r)
}