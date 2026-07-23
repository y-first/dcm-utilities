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
		var instanceDisplayName string

		BeforeAll(func() {
			requireKubevirtSP()

			if err := checkClusterAccess(); err != nil {
				Skip("kubectl/oc cluster access required for core platform test")
			}
			if err := checkStorageClass(); err != nil {
				Skip("At least one StorageClass required for VM provisioning: " + err.Error())
			}
		})

		AfterAll(func() {
			if instanceID != "" {
				resp, err := doRequest(http.MethodDelete, "/catalog-item-instances/"+instanceID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
			}
			if policyID != "" {
				resp, err := doRequest(http.MethodDelete, "/policies/"+policyID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
			}
			if catalogItemID != "" {
				resp, err := doRequest(http.MethodDelete, "/catalog-items/"+catalogItemID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
			}
		})

		It("discovers the KubeVirt provider with registration fields [TC-01]", func() {
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

			Expect(p["service_type"]).To(Equal("vm"))
			Expect(p["schema_version"]).To(Equal("v1alpha1"))
			endpoint, _ := p["endpoint"].(string)
			Expect(endpoint).To(ContainSubstring("/api/v1alpha1/vms"))
			// operations may be omitted by current SPRM payload; assert when present
			if ops, ok := p["operations"]; ok {
				GinkgoWriter.Printf("provider operations: %#v\n", ops)
				Expect(ops).NotTo(BeNil())
			}

			GinkgoWriter.Printf("Using KubeVirt provider: %s endpoint=%s\n", kubevirtProviderName, endpoint)
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
			// Use whole-array path storage.disks (not disks[0].*) — control-plane
			// nested_map only splits on '.', so indexed segments become literal keys.
			payload := fmt.Sprintf(`{
				"api_version": "v1alpha1",
				"display_name": %q,
				"spec": {
					"resources": [
						{
							"name": "vm",
							"service_type": "vm",
							"fields": [
								{"path": "metadata.name", "display_name": "VM Name", "editable": true, "default": %q},
								{"path": "guest_os.type", "display_name": "Guest OS", "editable": true, "default": "linux"},
								{"path": "vcpu.count", "editable": false, "default": 1},
								{"path": "memory.size", "editable": false, "default": "1GB"},
								{"path": "storage.disks", "editable": false, "default": [{"name":"boot","capacity":"10GB"}]}
							]
						}
					]
				}
			}`, name, name)

			resp, err := doRequest(http.MethodPost, "/catalog-items", payload)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			uid, ok := body["uid"].(string)
			Expect(ok).To(BeTrue())
			catalogItemID = uid
		})

		It("creates a routing policy for KubeVirt provider", func() {
			Expect(kubevirtProviderName).NotTo(BeEmpty())

			name := uniqueName("e2e-kubevirt-policy")
			pkgName := fmt.Sprintf("e2e_kubevirt_%d", time.Now().UnixNano()%1000000)
			priority := int(time.Now().UnixNano()%999) + 1
			payload := fmt.Sprintf(`{
				"display_name": %q,
				"policy_type": "GLOBAL",
				"priority": %d,
				"description": "E2E test: route to KubeVirt provider",
				"rego_code": "package %s\n\nmain := {\"selected_provider\": \"%s\"}"
			}`, name, priority, pkgName, kubevirtProviderName)

			resp, err := doRequest(http.MethodPost, "/policies", payload)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			id, ok := body["id"].(string)
			Expect(ok).To(BeTrue())
			policyID = id
		})

		It("creates a catalog item instance for VM [TC-23]", func() {
			Expect(catalogItemID).NotTo(BeEmpty())

			instanceDisplayName = uniqueName("e2e-kubevirt-inst")
			payload := fmt.Sprintf(`{
				"api_version": "v1alpha1",
				"display_name": %q,
				"spec": {
					"catalog_item_id": %q,
					"user_values": [
						{"resource": "vm", "path": "metadata.name", "value": %q},
						{"resource": "vm", "path": "guest_os.type", "value": "linux"}
					]
				}
			}`, instanceDisplayName, catalogItemID, instanceDisplayName)

			resp, err := doRequest(http.MethodPost, "/catalog-item-instances", payload)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			instanceID, _ = body["uid"].(string)

			spec, ok := body["spec"].(map[string]interface{})
			Expect(ok).To(BeTrue())
			ids, ok := spec["resource_ids"].([]interface{})
			Expect(ok).To(BeTrue())
			Expect(ids).NotTo(BeEmpty())
			resourceID, _ = ids[0].(string)
			Expect(resourceID).NotTo(BeEmpty())
		})

		It("VM reaches Running status on STI and cluster [TC-23]", func() {
			Expect(resourceID).NotTo(BeEmpty())

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
			}).WithTimeout(600 * time.Second).WithPolling(10 * time.Second).Should(Equal("Running"),
				"VM should reach Running status")

			// Cluster verification: STI id is the DCM instance id used as SP VM id
			ns := kubevirtNamespace()
			Eventually(func() error {
				name, err := findVMNameByInstanceID(resourceID, ns)
				if err != nil {
					return err
				}
				return verifyDCMLabels(name, ns, resourceID)
			}).WithTimeout(120 * time.Second).WithPolling(5 * time.Second).Should(Succeed())
		})

		It("has correct provider assignment", func() {
			Expect(resourceID).NotTo(BeEmpty())

			resp, err := doRequest(http.MethodGet, "/service-type-instances/"+resourceID, "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			decodeJSON(resp, &body)
			Expect(body["status"]).To(Equal("Running"))
			Expect(body["provider_name"]).To(Equal(kubevirtProviderName))
		})

		It("deletes catalog instance and removes VM [TC-24]", func() {
			Expect(instanceID).NotTo(BeEmpty())
			Expect(resourceID).NotTo(BeEmpty())

			ns := kubevirtNamespace()
			clusterName, err := findVMNameByInstanceID(resourceID, ns)
			Expect(err).NotTo(HaveOccurred())

			resp, err := doRequest(http.MethodDelete, "/catalog-item-instances/"+instanceID, "")
			Expect(err).NotTo(HaveOccurred())
			if resp != nil {
				resp.Body.Close()
			}
			instanceID = "" // AfterAll should not double-delete

			Eventually(func() int {
				r, err := doRequest(http.MethodGet, "/service-type-instances/"+resourceID, "")
				if err != nil {
					return 0
				}
				defer r.Body.Close()
				return r.StatusCode
			}).WithTimeout(180 * time.Second).WithPolling(5 * time.Second).
				Should(Or(Equal(http.StatusNotFound), Equal(http.StatusGone)))

			Eventually(func() error {
				return verifyVMDeleted(clusterName, ns)
			}).WithTimeout(180 * time.Second).WithPolling(5 * time.Second).Should(Succeed())
		})
	})
})
