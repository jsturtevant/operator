// Copyright (c) 2022 Tigera, Inc. All rights reserved.

package imageassurance

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"

	operatorv1 "github.com/tigera/operator/api/v1"
	"github.com/tigera/operator/pkg/apis"
	"github.com/tigera/operator/pkg/common"
	"github.com/tigera/operator/pkg/components"
	"github.com/tigera/operator/pkg/controller/status"
	"github.com/tigera/operator/pkg/controller/utils"
	"github.com/tigera/operator/pkg/render/imageassurance"
	"github.com/tigera/operator/test"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Image Assurance Controller", func() {
	var c client.Client
	var ctx context.Context
	var r ReconcileImageAssurance
	var scheme *runtime.Scheme
	var mockStatus *status.MockStatus

	BeforeEach(func() {
		// The schema contains all objects that should be known to the fake client when the test runs.
		scheme = runtime.NewScheme()
		Expect(apis.AddToScheme(scheme)).NotTo(HaveOccurred())
		Expect(appsv1.SchemeBuilder.AddToScheme(scheme)).ShouldNot(HaveOccurred())
		Expect(rbacv1.SchemeBuilder.AddToScheme(scheme)).ShouldNot(HaveOccurred())
		Expect(batchv1.SchemeBuilder.AddToScheme(scheme)).ShouldNot(HaveOccurred())
		Expect(operatorv1.SchemeBuilder.AddToScheme(scheme)).NotTo(HaveOccurred())
		// Create a client that will have a crud interface of k8s objects.
		c = fake.NewClientBuilder().WithScheme(scheme).Build()
		ctx = context.Background()

		mockStatus = &status.MockStatus{}
		mockStatus.On("AddDaemonsets", mock.Anything).Return()
		mockStatus.On("AddDeployments", mock.Anything).Return()
		mockStatus.On("IsAvailable").Return(true)
		mockStatus.On("AddStatefulSets", mock.Anything).Return()
		mockStatus.On("AddCronJobs", mock.Anything)
		mockStatus.On("OnCRFound").Return()
		mockStatus.On("OnCRNotFound").Return()
		mockStatus.On("ClearDegraded")
		mockStatus.On("SetDegraded", "Waiting for LicenseKeyAPI to be ready", "").Return().Maybe()
		mockStatus.On("ReadyToMonitor")

		r = ReconcileImageAssurance{
			client:          c,
			scheme:          scheme,
			provider:        operatorv1.ProviderNone,
			status:          mockStatus,
			licenseAPIReady: &utils.ReadyFlag{},
		}

		Expect(c.Create(ctx, &operatorv1.Installation{
			ObjectMeta: metav1.ObjectMeta{Name: "default"},
			Spec: operatorv1.InstallationSpec{
				Variant:  operatorv1.TigeraSecureEnterprise,
				Registry: "some.registry.org/",
			},
			Status: operatorv1.InstallationStatus{
				Variant: operatorv1.TigeraSecureEnterprise,
				Computed: &operatorv1.InstallationSpec{
					Registry: "my-reg",
					// The test is provider agnostic.
					KubernetesProvider: operatorv1.ProviderNone,
				},
			},
		})).NotTo(HaveOccurred())

		// Create empty secrets, so that reconciles passes.
		Expect(c.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: imageassurance.PGUserSecretName, Namespace: common.OperatorNamespace()},
			Data: map[string][]byte{
				"username": []byte("username"),
				"password": []byte("my-secret-pass"),
			},
		})).NotTo(HaveOccurred())
		Expect(c.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: imageassurance.ManagerCertSecretName, Namespace: common.OperatorNamespace()},
			Data: map[string][]byte{
				"cert": []byte("cert"),
				"key":  []byte("private-key"),
			},
		})).NotTo(HaveOccurred())
		Expect(c.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: imageassurance.PGCertSecretName, Namespace: common.OperatorNamespace()},
			Data: map[string][]byte{
				"server-ca":   []byte("server-ca"),
				"client-cert": []byte("client-cert"),
				"client-key":  []byte("client-key"),
			},
		})).NotTo(HaveOccurred())
		Expect(c.Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: imageassurance.PGConfigMapName, Namespace: common.OperatorNamespace()},
			Data: map[string]string{
				"host": "some.domain.io",
				"name": "my-database",
				"port": "1234",
			},
		})).NotTo(HaveOccurred())

	})

	It("should render accurate resources for for image assurance", func() {

		By("applying the ImageAssurance CR to the fake cluster")
		//apply image assurance cr
		Expect(c.Create(ctx, &operatorv1.ImageAssurance{
			ObjectMeta: metav1.ObjectMeta{Name: "tigera-secure"},
			Spec:       operatorv1.ImageAssuranceSpec{},
		})).NotTo(HaveOccurred())

		_, err := r.Reconcile(ctx, reconcile.Request{})
		Expect(err).ShouldNot(HaveOccurred())

		By("ensuring the ImageAssurance API resource created ")
		api := appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      imageassurance.ResourceNameImageAssuranceAPI,
				Namespace: imageassurance.NameSpaceImageAssurance,
			},
		}
		Expect(test.GetResource(c, &api)).To(BeNil())
		Expect(api.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(api.Spec.Template.Spec.Containers[0].Image).To(Equal(fmt.Sprintf("some.registry.org/%s:%s",
			components.ComponentImageAssuranceApi.Image, components.ComponentImageAssuranceApi.Version)))

		By("ensuring that ImageAssurance scanner resources created properly")
		scanner := appsv1.Deployment{
			TypeMeta: metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      imageassurance.ResourceNameImageAssuranceScanner,
				Namespace: imageassurance.NameSpaceImageAssurance,
			},
		}
		Expect(test.GetResource(c, &scanner)).To(BeNil())
		Expect(scanner.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(scanner.Spec.Template.Spec.Containers[0].Image).To(Equal(fmt.Sprintf("some.registry.org/%s:%s",
			components.ComponentImageAssuranceScanner.Image, components.ComponentImageAssuranceScanner.Version)))

	})

})