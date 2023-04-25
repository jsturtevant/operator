// Copyright (c) 2019-2023 Tigera, Inc. All rights reserved.

package render_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	operatorv1 "github.com/tigera/operator/api/v1"
	"github.com/tigera/operator/pkg/components"
	"github.com/tigera/operator/pkg/render"
	rcimageassurance "github.com/tigera/operator/pkg/render/common/imageassurance"
	"github.com/tigera/operator/pkg/render/common/networkpolicy"
	rtest "github.com/tigera/operator/pkg/render/common/test"
	"github.com/tigera/operator/pkg/render/testutils"
	"github.com/tigera/operator/pkg/tls/certificatemanagement"
)

var _ = Describe("Tigera Secure Cloud Manager rendering tests", func() {
	installation := &operatorv1.InstallationSpec{}

	It("should render all resources for a Image Assurance configuration", func() {
		resources := renderObjects(renderConfig{
			oidc:                  false,
			managementCluster:     nil,
			installation:          installation,
			imageAssuranceEnabled: true})

		// Should render the correct resources.
		expectedResources := []struct {
			name    string
			ns      string
			group   string
			version string
			kind    string
		}{
			// In addition to default resources, extra resource voltron image assurance secret is expected.
			{name: render.ManagerNamespace, ns: "", group: "", version: "v1", kind: "Namespace"},
			{name: render.ManagerPolicyName, ns: "tigera-manager", group: "projectcalico.org", version: "v3", kind: "NetworkPolicy"},
			{name: networkpolicy.TigeraComponentDefaultDenyPolicyName, ns: "tigera-manager", group: "projectcalico.org", version: "v3", kind: "NetworkPolicy"},
			{name: render.ManagerServiceAccount, ns: render.ManagerNamespace, group: "", version: "v1", kind: "ServiceAccount"},
			{name: render.ManagerClusterRole, ns: "", group: "rbac.authorization.k8s.io", version: "v1", kind: "ClusterRole"},
			{name: render.ManagerClusterRoleBinding, ns: "", group: "rbac.authorization.k8s.io", version: "v1", kind: "ClusterRoleBinding"},
			{name: render.ManagerClusterSettings, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettingsGroup"},
			{name: render.ManagerUserSettings, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettingsGroup"},
			{name: render.ManagerClusterSettingsLayerTigera, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettings"},
			{name: render.ManagerClusterSettingsViewDefault, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettings"},
			{name: "tigera-manager", ns: render.ManagerNamespace, group: "", version: "v1", kind: "Service"},
			{name: "tigera-manager", ns: "", group: "policy", version: "v1beta1", kind: "PodSecurityPolicy"},
			{name: rcimageassurance.ImageAssuranceSecretName, ns: render.ManagerNamespace, group: "", version: "v1", kind: "Secret"},
			{name: "tigera-image-assurance-config", ns: render.ManagerNamespace, group: "", version: "v1", kind: "ConfigMap"},
			{name: render.ImageAssurancePolicyName, ns: "tigera-manager", group: "projectcalico.org", version: "v3", kind: "NetworkPolicy"},
			{name: "tigera-manager", ns: render.ManagerNamespace, group: "apps", version: "v1", kind: "Deployment"},
		}

		i := 0
		for _, expectedRes := range expectedResources {
			rtest.ExpectResource(resources[i], expectedRes.name, expectedRes.ns, expectedRes.group, expectedRes.version, expectedRes.kind)
			i++
		}
		Expect(len(resources)).To(Equal(len(expectedResources)))

		deployment := rtest.GetResource(resources, "tigera-manager", render.ManagerNamespace, "apps", "v1", "Deployment").(*appsv1.Deployment)
		Expect(len(deployment.Spec.Template.Spec.Containers)).Should(Equal(3))
		var manager = deployment.Spec.Template.Spec.Containers[0]
		var esProxy = deployment.Spec.Template.Spec.Containers[1]
		var voltron = deployment.Spec.Template.Spec.Containers[2]
		var dpSpec = deployment.Spec.Template.Spec

		Expect(manager.Image).Should(Equal(components.TigeraRegistry + "tigera/cnx-manager:" + components.ComponentManager.Version))
		Expect(esProxy.Image).Should(Equal(components.CloudRegistry + "tigera/es-proxy:tesla-" + components.ComponentEsProxy.Version))
		Expect(voltron.Image).Should(Equal(components.TigeraRegistry + "tigera/voltron:" + components.ComponentManagerProxy.Version))

		Expect(esProxy.Env).Should(ContainElements(
			corev1.EnvVar{Name: "ELASTIC_INDEX_SUFFIX", Value: "tenant_id.clusterTestName"},
		))
		// In cloud we have 2 volume mounts since manager tls is included
		Expect(esProxy.VolumeMounts).To(HaveLen(2))
		Expect(esProxy.VolumeMounts[0].Name).To(Equal(certificatemanagement.TrustedCertConfigMapName))
		Expect(esProxy.VolumeMounts[0].MountPath).To(Equal("/etc/pki/tls/certs"))

		// cloud modification: manager tls cert
		Expect(esProxy.VolumeMounts[1].Name).To(Equal(render.ManagerTLSSecretName))
		Expect(esProxy.VolumeMounts[1].MountPath).To(Equal("/manager-tls"))

		// In addition to default volumes, deployment should have extra volume for image assurance secret
		Expect(dpSpec.Volumes).To(ContainElement(
			corev1.Volume{
				Name: rcimageassurance.ImageAssuranceSecretName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: rcimageassurance.ImageAssuranceSecretName,
						Items: []corev1.KeyToPath{{
							Key:  "tls.crt",
							Path: "tls.crt",
						}},
					},
				},
			},
		))

		// deployment should have an annotation for image assurance cert.
		Expect(deployment.Spec.Template.Annotations).Should(HaveKey(
			rcimageassurance.ImageAssuranceCertHashAnnotation,
		))

		// in addition to default volumes mounts, voltron should have an extra volume mount for bast certs
		Expect(voltron.VolumeMounts).To(ContainElement(
			corev1.VolumeMount{
				Name:      rcimageassurance.ImageAssuranceSecretName,
				MountPath: "/certs/bast",
				ReadOnly:  true,
			},
		))

		// voltron should contain Image assurance related variables.
		Expect(voltron.Env).Should(ContainElements(
			corev1.EnvVar{Name: "VOLTRON_ENABLE_IMAGE_ASSURANCE", Value: "true"},
			corev1.EnvVar{Name: "VOLTRON_IMAGE_ASSURANCE_ENDPOINT", Value: "https://tigera-image-assurance-api.tigera-image-assurance.svc:9443"},
			corev1.EnvVar{Name: "VOLTRON_IMAGE_ASSURANCE_CA_BUNDLE_PATH", Value: "/certs/bast/tls.crt"},
		))

		// manager should contain Image assurance related variables.
		Expect(manager.Env).Should(ContainElements(
			corev1.EnvVar{Name: "ENABLE_IMAGE_ASSURANCE_SUPPORT", Value: "true"},
			corev1.EnvVar{Name: "CNX_IMAGE_ASSURANCE_API_URL", Value: "/bast/v1"},
		))
	})

	Context("allow-tigera.image-assurance-access rendering", func() {
		policyName := types.NamespacedName{Name: "allow-tigera.image-assurance-access", Namespace: "tigera-manager"}

		It("should render allow-tigera.image-assurance-access policy",
			func() {
				// Default configuration.
				resources := renderObjects(renderConfig{
					oidc:                    false,
					managementCluster:       nil,
					installation:            installation,
					complianceFeatureActive: true,
					imageAssuranceEnabled:   true,
				})

				policy := testutils.GetAllowTigeraPolicyFromResources(policyName, resources)
				expectedPolicy := testutils.GetExpectedPolicyFromFile("testutils/expected_policies/image-assurance.json")
				Expect(policy).To(Equal(expectedPolicy))
			},
		)
	})

	render.VoltronExtraEnv = map[string]string{
		"VOLTRON_EXTRA_ENVIRONMENT_VARIABLE1": "value1",
		"VOLTRON_EXTRA_ENVIRONMENT_VARIABLE3": "value3",
		"VOLTRON_EXTRA_ENVIRONMENT_VARIABLE2": "value2",
	}
	resources := renderObjects(renderConfig{
		oidc:              false,
		managementCluster: nil,
		installation:      installation,
		cloudRBACEnabled:  true})

	deployment := rtest.GetResource(resources, "tigera-manager", render.ManagerNamespace, "apps", "v1", "Deployment").(*appsv1.Deployment)
	template := deployment.Spec.Template.Spec

	It("should render voltron resources for Cloud RBAC", func() {

		// Should render the correct resources.
		expectedResources := []struct {
			name    string
			ns      string
			group   string
			version string
			kind    string
		}{
			// In addition to default resources, extra resource voltron cloud-rbac-ca secret is expected.
			{name: render.ManagerNamespace, ns: "", group: "", version: "v1", kind: "Namespace"},
			{name: render.ManagerPolicyName, ns: "tigera-manager", group: "projectcalico.org", version: "v3", kind: "NetworkPolicy"},
			{name: networkpolicy.TigeraComponentDefaultDenyPolicyName, ns: "tigera-manager", group: "projectcalico.org", version: "v3", kind: "NetworkPolicy"},
			{name: render.ManagerServiceAccount, ns: render.ManagerNamespace, group: "", version: "v1", kind: "ServiceAccount"},
			{name: render.ManagerClusterRole, ns: "", group: "rbac.authorization.k8s.io", version: "v1", kind: "ClusterRole"},
			{name: render.ManagerClusterRoleBinding, ns: "", group: "rbac.authorization.k8s.io", version: "v1", kind: "ClusterRoleBinding"},
			{name: render.ManagerClusterSettings, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettingsGroup"},
			{name: render.ManagerUserSettings, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettingsGroup"},
			{name: render.ManagerClusterSettingsLayerTigera, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettings"},
			{name: render.ManagerClusterSettingsViewDefault, ns: "", group: "projectcalico.org", version: "v3", kind: "UISettings"},
			{name: "tigera-manager", ns: render.ManagerNamespace, group: "", version: "v1", kind: "Service"},
			{name: "tigera-manager", ns: "", group: "policy", version: "v1beta1", kind: "PodSecurityPolicy"},
			{name: "calico-cloud-rbac-tls", ns: render.ManagerNamespace, group: "", version: "v1", kind: "Secret"},
			{name: render.CloudRBACAPIPolicyName, ns: "tigera-manager", group: "projectcalico.org", version: "v3", kind: "NetworkPolicy"},
			{name: "tigera-manager", ns: render.ManagerNamespace, group: "apps", version: "v1", kind: "Deployment"},
		}

		for i, expectedRes := range expectedResources {
			rtest.ExpectResource(resources[i], expectedRes.name, expectedRes.ns, expectedRes.group, expectedRes.version, expectedRes.kind)
		}
		Expect(len(resources)).To(Equal(len(expectedResources)))

		Expect(len(template.Containers)).Should(Equal(3))
		var manager = template.Containers[0]
		var esProxy = template.Containers[1]
		var voltron = template.Containers[2]

		Expect(manager.Image).Should(Equal(components.TigeraRegistry + "tigera/cnx-manager:" + components.ComponentManager.Version))
		Expect(esProxy.Image).Should(Equal(components.CloudRegistry + "tigera/es-proxy:tesla-" + components.ComponentEsProxy.Version))
		Expect(voltron.Image).Should(Equal(components.TigeraRegistry + "tigera/voltron:" + components.ComponentManagerProxy.Version))
	})

	Context("voltron", func() {
		var voltron = template.Containers[2]

		It("should have the ca secret volume", func() {
			Expect(template.Volumes).To(ContainElement(
				corev1.Volume{
					Name: "calico-cloud-rbac-tls",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "calico-cloud-rbac-tls",
						},
					},
				},
			))
		})

		It("should have the ca volume mount", func() {
			Expect(voltron.VolumeMounts).To(ContainElement(
				corev1.VolumeMount{
					Name:      "calico-cloud-rbac-tls",
					MountPath: "/certs/cloud-rbac",
					ReadOnly:  true,
				},
			))
		})

		It("should have the cert hash annotation", func() {
			Expect(deployment.Spec.Template.Annotations).Should(HaveKey(
				"hash.operator.tigera.io/cloud-rbac-cert",
			))
		})

		It("should have env vars", func() {
			Expect(voltron.Env).Should(ContainElements(
				corev1.EnvVar{Name: "VOLTRON_CHECK_MANAGED_CLUSTER_AUTHORIZATION_BEFORE_PROXY", Value: "true"},
				corev1.EnvVar{Name: "VOLTRON_ENABLE_CALICO_CLOUD_RBAC_API", Value: "true"},
				corev1.EnvVar{Name: "VOLTRON_CALICO_CLOUD_RBAC_API_CA_BUNDLE_PATH", Value: "/certs/cloud-rbac/tls.crt"},
				corev1.EnvVar{Name: "VOLTRON_CALICO_CLOUD_RBAC_API_ENDPOINT", Value: "https://cc-rbac-api.calico-cloud-rbac.svc:8443"},
			))
		})

		It("should have env vars appended from configmap override in correct order", func() {
			Expect(len(voltron.Env)).To(BeNumerically(">=", 3))
			Expect(voltron.Env[len(voltron.Env)-3:]).Should(Equal([]corev1.EnvVar{
				{Name: "VOLTRON_EXTRA_ENVIRONMENT_VARIABLE1", Value: "value1"},
				{Name: "VOLTRON_EXTRA_ENVIRONMENT_VARIABLE2", Value: "value2"},
				{Name: "VOLTRON_EXTRA_ENVIRONMENT_VARIABLE3", Value: "value3"},
			}))
		})
	})
})