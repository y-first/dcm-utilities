//go:build e2e

package e2e_test

import (
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Core Platform - KubeVirt Provider", Label("core", "platform", "kubevirt"), func() {
	Context("VM provisioning happy path", Ordered, func() {
		var kubevirtProviderName string
		var catalogItemID, policyID, instanceID, resourceID string

		BeforeAll(func() {
			requireKubevirtSP()

			// Verify cluster access (required for full E2E flow)
			if err := checkClusterAccess(); err != nil {
				Skip("kubectl/oc cluster access required for core platform test")
			}

			// Verify storage class availability (required for VM provisioning)
			if err := checkStorageClass(); err != nil {
				Skip("At least one StorageClass required for VM provisioning: " + err.Error())
			}
		})

		AfterAll(func() {
			// Cleanup catalog-item-instance
			if instanceID != "" {
				resp, err := doRequest(http.MethodDelete, "/catalog-item-instances/"+instanceID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
			}

			// Cleanup policy
			if policyID != "" {
				resp, err := doRequest(http.MethodDelete, "/policies/"+policyID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
			}

			// Cleanup catalog item
			if catalogItemID != "" {
				resp, err := doRequest(http.MethodDelete, "/catalog-items/"+catalogItemID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
			}
		})

		It("discovers the KubeVirt provider", func() {
			resp, err := doRequest(http.MethodGet, "/providers?type=vm", "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			Expect(body).To(HaveKey("providers"))

			providers, ok := body["providers"].([]interface{})
			Expect(ok).To(BeTrue())
			Expect(providers).NotTo(BeEmpty(), "no vm providers registered")

			p := providers[0].(map[string]interface{})
			kubevirtProviderName, _ = p["name"].(string)
			Expect(kubevirtProviderName).NotTo(BeEmpty())
			GinkgoWriter.Printf("Using KubeVirt provider: %s\n", kubevirtProviderName)
		})

		It("verifies the vm service type exists", func() {
			resp, err := doRequest(http.MethodGet, "/service-types", "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			Expect(body).To(HaveKey("results"))

			results, ok := body["results"].([]interface{})
			Expect(ok).To(BeTrue())

			var found bool
			for _, r := range results {
				st, ok := r.(map[string]interface{})
				Expect(ok).To(BeTrue())
				if stype, _ := st["service_type"].(string); stype == "vm" {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "no service type with service_type=vm found")
		})

		It("creates a catalog item for VM", func() {
			name := uniqueName("e2e-kubevirt")
			payload := fmt.Sprintf(`{
				"api_version": "v1alpha1",
				"display_name": %q,
				"spec": {
					"service_type": "vm",
					"fields": [
						{"path": "metadata.name", "display_name": "VM Name", "editable": true, "default": %q},
						{"path": "guest_os.type", "display_name": "Guest OS", "editable": true, "default": "linux"},
						{"path": "vcpu.count", "editable": false, "default": 1},
						{"path": "memory.size", "editable": false, "default": "1GB"},
						{"path": "storage.disks[0].name", "editable": false, "default": "boot"},
						{"path": "storage.disks[0].capacity", "editable": false, "default": "10GB"}
					]
				}
			}`, name, name)

			resp, err := doRequest(http.MethodPost, "/catalog-items", payload)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			Expect(body).To(HaveKey("uid"))

			uid, ok := body["uid"].(string)
			Expect(ok).To(BeTrue())
			catalogItemID = uid
		})

		It("creates a routing policy for KubeVirt provider", func() {
			Expect(kubevirtProviderName).NotTo(BeEmpty())

			name := uniqueName("e2e-kubevirt-policy")
			pkgName := fmt.Sprintf("e2e_kubevirt_%d", time.Now().UnixNano()%1000000)
			payload := fmt.Sprintf(`{
				"display_name": %q,
				"policy_type": "GLOBAL",
				"priority": 100,
				"description": "E2E test: route to KubeVirt provider",
				"rego_code": "package %s\n\nmain := {\"selected_provider\": \"%s\"}"
			}`, name, pkgName, kubevirtProviderName)

			resp, err := doRequest(http.MethodPost, "/policies", payload)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			Expect(body).To(HaveKey("id"))

			id, ok := body["id"].(string)
			Expect(ok).To(BeTrue())
			policyID = id
		})

		It("creates a catalog item instance for VM", func() {
			Expect(catalogItemID).NotTo(BeEmpty())

			name := uniqueName("e2e-kubevirt-inst")
			payload := fmt.Sprintf(`{
				"api_version": "v1alpha1",
				"display_name": %q,
				"spec": {
					"catalog_item_id": %q,
					"user_values": [
						{"path": "metadata.name", "value": %q},
						{"path": "guest_os.type", "value": "linux"}
					]
				}
			}`, name, catalogItemID, name)

			resp, err := doRequest(http.MethodPost, "/catalog-item-instances", payload)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			Expect(body).To(HaveKey("uid"))
			Expect(body).To(HaveKey("resource_id"))

			instanceID, _ = body["uid"].(string)
			resourceID, _ = body["resource_id"].(string)
		})

		It("VM reaches RUNNING status", func() {
			Expect(resourceID).NotTo(BeEmpty())

			// VMs take longer to provision than containers - increase timeout
			Eventually(func() string {
				resp, err := doRequest(http.MethodGet, "/service-type-instances/"+resourceID, "")
				if err != nil {
					return ""
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					return ""
				}

				var body map[string]interface{}
				decodeJSON(resp, &body)
				status, _ := body["status"].(string)
				GinkgoWriter.Printf("VM status: %s\n", status)
				return status
			}).WithTimeout(600 * time.Second).WithPolling(10 * time.Second).Should(Equal("RUNNING"),
				"VM should reach RUNNING status")
		})

		It("has correct provider assignment", func() {

			Expect(resourceID).NotTo(BeEmpty())

			resp, err := doRequest(http.MethodGet, "/service-type-instances/"+resourceID, "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			Expect(body["status"]).To(Equal("RUNNING"))
			Expect(body["provider_name"]).To(Equal(kubevirtProviderName))
		})
	})
})
