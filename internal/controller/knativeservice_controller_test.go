package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	knativev1 "github.com/JeremyPDonahue/knative-route-sync/api/knative/v1"
	routev1 "github.com/JeremyPDonahue/knative-route-sync/api/openshift/route/v1"
)

var _ = Describe("KnativeServiceReconciler", func() {
	var reconciler *KnativeServiceReconciler

	BeforeEach(func() {
		reconciler = &KnativeServiceReconciler{
			Client:   k8sClient,
			Scheme:   scheme.Scheme,
			Recorder: record.NewFakeRecorder(32),
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: kourierNamespace},
		}
		_ = k8sClient.Create(ctx, ns)

		kourier := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      kourierServiceName,
				Namespace: kourierNamespace,
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 80}},
			},
		}
		_ = k8sClient.Create(ctx, kourier)
	})

	It("should not create resources when ksvc is not ready", func() {
		ksvc := &knativev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "not-ready-service",
				Namespace: "default",
			},
		}
		Expect(k8sClient.Create(ctx, ksvc)).To(Succeed())

		// First reconcile adds the finalizer and returns
		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "not-ready-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		// Second reconcile hits the IsReady gate — no conditions set, should skip
		_, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "not-ready-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		route := &routev1.Route{}
		err = k8sClient.Get(ctx, types.NamespacedName{
			Name:      "knative-route-not-ready-service",
			Namespace: "default",
		}, route)
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("should create bridge Service, Endpoints, and Route when ksvc is ready", func() {
		ksvc := &knativev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ready-service",
				Namespace: "default",
			},
		}
		Expect(k8sClient.Create(ctx, ksvc)).To(Succeed())

		// Simulate Knative marking the service ready with a URL
		ksvc.Status = knativev1.ServiceStatus{
			URL: "https://ready-service.example.com",
			Conditions: []knativev1.Condition{
				{Type: "Ready", Status: "True"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, ksvc)).To(Succeed())

		// First reconcile adds the finalizer and returns
		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "ready-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		// Second reconcile creates the resources
		_, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "ready-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		resourceName := types.NamespacedName{
			Name:      "knative-route-ready-service",
			Namespace: "default",
		}

		svc := &corev1.Service{}
		Expect(k8sClient.Get(ctx, resourceName, svc)).To(Succeed())

		endpoints := &corev1.Endpoints{} //nolint:staticcheck
		Expect(k8sClient.Get(ctx, resourceName, endpoints)).To(Succeed())

		route := &routev1.Route{}
		Expect(k8sClient.Get(ctx, resourceName, route)).To(Succeed())
		Expect(route.Spec.Host).To(Equal("ready-service.example.com"))
	})

	It("should clean up resources when ksvc is deleted", func() {
		ksvc := &knativev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "deleted-service",
				Namespace: "default",
			},
		}
		Expect(k8sClient.Create(ctx, ksvc)).To(Succeed())

		ksvc.Status = knativev1.ServiceStatus{
			URL: "https://deleted-service.example.com",
			Conditions: []knativev1.Condition{
				{Type: "Ready", Status: "True"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, ksvc)).To(Succeed())

		// First reconcile — add finalizer
		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "deleted-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		// Second reconcile — create resources
		_, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "deleted-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		// Delete the ksvc
		Expect(k8sClient.Delete(ctx, ksvc)).To(Succeed())

		// Fetch with deletion timestamp set
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: "deleted-service", Namespace: "default",
		}, ksvc)).To(Succeed())

		// Third reconcile — deletion path
		_, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "deleted-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		resourceName := types.NamespacedName{
			Name:      "knative-route-deleted-service",
			Namespace: "default",
		}

		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, resourceName, &routev1.Route{}))).To(BeTrue())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, resourceName, &corev1.Endpoints{}))).To(BeTrue()) //nolint:staticcheck
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, resourceName, &corev1.Service{}))).To(BeTrue())

		// Cleanup
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ksvc))).To(Succeed())
	})

	It("should return an error when Kourier has no ClusterIP", func() {
		// Remove the Kourier service created in BeforeEach
		kourier := &corev1.Service{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      kourierServiceName,
			Namespace: kourierNamespace,
		}, kourier)).To(Succeed())
		Expect(k8sClient.Delete(ctx, kourier)).To(Succeed())

		ksvc := &knativev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kourier-missing-service",
				Namespace: "default",
			},
		}
		Expect(k8sClient.Create(ctx, ksvc)).To(Succeed())

		ksvc.Status = knativev1.ServiceStatus{
			URL: "https://kourier-missing-service.example.com",
			Conditions: []knativev1.Condition{
				{Type: "Ready", Status: "True"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, ksvc)).To(Succeed())

		// First reconcile — add finalizer
		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "kourier-missing-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		// Second reconcile — should fail on Kourier lookup
		_, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "kourier-missing-service",
				Namespace: "default",
			},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("kourier-internal"))
	})

	It("should return an error when ksvc is ready but has no URL", func() {
		ksvc := &knativev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "no-url-service",
				Namespace: "default",
			},
		}
		Expect(k8sClient.Create(ctx, ksvc)).To(Succeed())

		// Ready: True but URL deliberately left empty
		ksvc.Status = knativev1.ServiceStatus{
			Conditions: []knativev1.Condition{
				{Type: "Ready", Status: "True"},
			},
		}
		Expect(k8sClient.Status().Update(ctx, ksvc)).To(Succeed())

		// First reconcile — add finalizer
		_, err := reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "no-url-service",
				Namespace: "default",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		// Second reconcile — should fail in ensureRoute when hostFromKsvc returns error
		_, err = reconciler.Reconcile(ctx, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Name:      "no-url-service",
				Namespace: "default",
			},
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no status URL"))
	})
})
