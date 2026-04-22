package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	knativev1 "github.com/JeremyPDonahue/knative-route-sync/api/knative/v1"
	routev1 "github.com/JeremyPDonahue/knative-route-sync/api/openshift/route/v1"
)

func buildFakeReconciler(c client.Client) *KnativeServiceReconciler {
	return &KnativeServiceReconciler{
		Client:   c,
		Scheme:   scheme.Scheme,
		Recorder: record.NewFakeRecorder(32),
	}
}

func reconcileRequest(name, namespace string) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}}
}

func readyKsvcWithFinalizer(name, namespace, url string) *knativev1.Service {
	return &knativev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Finalizers: []string{finalizerName},
		},
		Status: knativev1.ServiceStatus{
			URL: url,
			Conditions: []knativev1.Condition{
				{Type: "Ready", Status: "True"},
			},
		},
	}
}

func deletingKsvc(name, namespace string) *knativev1.Service {
	now := metav1.Now()
	return &knativev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			Finalizers:        []string{finalizerName},
			DeletionTimestamp: &now,
		},
	}
}

func kourierSvc(clusterIP string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kourierServiceName,
			Namespace: kourierNamespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: clusterIP,
			Ports:     []corev1.ServicePort{{Port: 80}},
		},
	}
}

func childObjects(resourceName string) []client.Object {
	return []client.Object{
		&routev1.Route{ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"}},
		&corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: "default"}},
	}
}

// foreignOwnerRef returns an owner reference pointing at a controller other than ksvc,
// which causes SetControllerReference to return AlreadyOwnedError.
func foreignOwnerRef() metav1.OwnerReference {
	isController := true
	return metav1.OwnerReference{
		APIVersion: "serving.knative.dev/v1",
		Kind:       "Service",
		Name:       "other-owner",
		UID:        "uid-other",
		Controller: &isController,
	}
}

var _ = Describe("KnativeServiceReconciler (fake client)", func() {
	It("should return an error when Get fails with a non-404 error", func() {
		ksvc := &knativev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "get-err", Namespace: "default"},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*knativev1.Service); ok {
						return fmt.Errorf("storage unavailable")
					}
					return cl.Get(ctx, key, obj, opts...)
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("get-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("storage unavailable"))
	})

	It("should return an error when Update fails after adding finalizer", func() {
		ksvc := &knativev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "update-err", Namespace: "default"},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return fmt.Errorf("etcd write failed")
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("update-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("etcd write failed"))
	})

	It("should return an error when Kourier ClusterIP is None", func() {
		ksvc := readyKsvcWithFinalizer("headless-kourier", "default", "https://headless-kourier.example.com")
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc, kourierSvc("None")).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("headless-kourier", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no ClusterIP"))
	})

	It("should return an error when bridge Service creation fails", func() {
		ksvc := readyKsvcWithFinalizer("bridge-svc-err", "default", "https://bridge-svc-err.example.com")
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc, kourierSvc("10.96.0.1")).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*corev1.Service); ok {
						return fmt.Errorf("service quota exceeded")
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("bridge-svc-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciling bridge Service"))
	})

	It("should return an error when bridge Endpoints creation fails", func() {
		ksvc := readyKsvcWithFinalizer("bridge-ep-err", "default", "https://bridge-ep-err.example.com")
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc, kourierSvc("10.96.0.1")).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*corev1.Endpoints); ok {
						return fmt.Errorf("endpoints quota exceeded")
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("bridge-ep-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciling bridge Endpoints"))
	})

	It("should return an error when Route creation fails", func() {
		ksvc := readyKsvcWithFinalizer("route-create-err", "default", "https://route-create-err.example.com")
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc, kourierSvc("10.96.0.1")).
			WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*routev1.Route); ok {
						return fmt.Errorf("route quota exceeded")
					}
					return cl.Create(ctx, obj, opts...)
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("route-create-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciling Route"))
	})

	It("should return an error when Route deletion fails during cleanup", func() {
		ksvc := deletingKsvc("del-route-err", "default")
		rName := routePrefix + "del-route-err"
		objs := append([]client.Object{ksvc}, childObjects(rName)...)
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(objs...).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*routev1.Route); ok {
						return fmt.Errorf("route delete failed")
					}
					return cl.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("del-route-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deleting Route"))
	})

	It("should return an error when Endpoints deletion fails during cleanup", func() {
		ksvc := deletingKsvc("del-ep-err", "default")
		rName := routePrefix + "del-ep-err"
		objs := append([]client.Object{ksvc}, childObjects(rName)...)
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(objs...).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*corev1.Endpoints); ok {
						return fmt.Errorf("endpoints delete failed")
					}
					return cl.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("del-ep-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deleting bridge Endpoints"))
	})

	It("should return an error when Service deletion fails during cleanup", func() {
		ksvc := deletingKsvc("del-svc-err", "default")
		rName := routePrefix + "del-svc-err"
		objs := append([]client.Object{ksvc}, childObjects(rName)...)
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(objs...).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if _, ok := obj.(*corev1.Service); ok {
						return fmt.Errorf("service delete failed")
					}
					return cl.Delete(ctx, obj, opts...)
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("del-svc-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("deleting bridge Service"))
	})

	It("should return an error when Update fails after removing finalizer", func() {
		ksvc := deletingKsvc("del-update-err", "default")
		rName := routePrefix + "del-update-err"
		objs := append([]client.Object{ksvc}, childObjects(rName)...)
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(objs...).
			WithInterceptorFuncs(interceptor.Funcs{
				Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					return fmt.Errorf("finalizer removal failed")
				},
			}).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("del-update-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("finalizer removal failed"))
	})

	It("should return nil when the ksvc no longer exists", func() {
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		result, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("gone", "default"))
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
	})

	It("should return an error when SetControllerReference fails for bridge Service", func() {
		ksvc := readyKsvcWithFinalizer("scr-svc-err", "default", "https://scr-svc-err.example.com")
		ksvc.UID = "uid-ksvc"
		bridgeSvc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            routePrefix + "scr-svc-err",
				Namespace:       "default",
				OwnerReferences: []metav1.OwnerReference{foreignOwnerRef()},
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc, kourierSvc("10.96.0.1"), bridgeSvc).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("scr-svc-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciling bridge Service"))
	})

	It("should return an error when SetControllerReference fails for bridge Endpoints", func() {
		ksvc := readyKsvcWithFinalizer("scr-ep-err", "default", "https://scr-ep-err.example.com")
		ksvc.UID = "uid-ksvc"
		bridgeEndpoints := &corev1.Endpoints{
			ObjectMeta: metav1.ObjectMeta{
				Name:            routePrefix + "scr-ep-err",
				Namespace:       "default",
				OwnerReferences: []metav1.OwnerReference{foreignOwnerRef()},
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc, kourierSvc("10.96.0.1"), bridgeEndpoints).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("scr-ep-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciling bridge Endpoints"))
	})

	It("should return an error when SetControllerReference fails for Route", func() {
		ksvc := readyKsvcWithFinalizer("scr-route-err", "default", "https://scr-route-err.example.com")
		ksvc.UID = "uid-ksvc"
		existingRoute := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:            routePrefix + "scr-route-err",
				Namespace:       "default",
				OwnerReferences: []metav1.OwnerReference{foreignOwnerRef()},
			},
		}
		c := fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(ksvc, kourierSvc("10.96.0.1"), existingRoute).
			Build()

		_, err := buildFakeReconciler(c).Reconcile(ctx, reconcileRequest("scr-route-err", "default"))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciling Route"))
	})

	It("should register with the controller manager without error", func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 scheme.Scheme,
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred())

		r := &KnativeServiceReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor("test"),
		}
		Expect(r.SetupWithManager(mgr)).To(Succeed())
	})
})
