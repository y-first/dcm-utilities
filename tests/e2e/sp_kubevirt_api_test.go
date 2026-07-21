//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KubeVirt Service Provider API", Label("sp", "kubevirt"), func() {
	BeforeEach(func() {
		requireKubevirtSP()
	})

	Context("Health endpoint", func() {
		It("returns healthy status when cluster is reachable [TC-04]", func() {
			resp, err := doKubevirtRequest(http.MethodGet, "/vms/health", "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&body)
			Expect(err).NotTo(HaveOccurred())

			status, ok := body["status"].(string)
			Expect(ok).To(BeTrue())
			Expect(status).To(Equal("healthy"))

			path, ok := body["path"].(string)
			Expect(ok).To(BeTrue())
			Expect(path).To(Equal("/api/v1alpha1/health"))
		})
	})

	Context("VM CRUD operations", Ordered, func() {
		var vmID string

		AfterAll(func() {
			if vmID != "" {
				resp, err := doKubevirtRequest(http.MethodDelete, "/vms/"+vmID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
				vmID = ""
			}
		})

		It("creates a VM with valid spec [TC-06]", func() {
			if err := checkClusterAccess(); err != nil {
				Skip("Cluster access required for VM creation test")
			}
			if err := checkStorageClass(); err != nil {
				Skip("At least one StorageClass required for VM creation: " + err.Error())
			}

			vmName := uniqueName("e2e-kubevirt-vm")
			spec := newTestVMSpec(vmName)

			payload, err := json.Marshal(map[string]interface{}{"spec": spec})
			Expect(err).NotTo(HaveOccurred())

			createPath, expectedID := createVMPath()
			resp, err := doKubevirtRequest(http.MethodPost, createPath, string(payload))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&body)
			Expect(err).NotTo(HaveOccurred())

			path, ok := body["path"].(string)
			Expect(ok).To(BeTrue())
			Expect(path).To(ContainSubstring("/vms/"))

			vmID = extractIDFromPath(path)
			Expect(vmID).NotTo(BeEmpty())
			Expect(vmID).To(Equal(expectedID))

			GinkgoWriter.Printf("Created VM with ID: %s\n", vmID)
		})

		It("lists VMs [TC-15]", func() {
			if vmID == "" {
				Skip("No VM created (create skipped or failed)")
			}

			resp, err := doKubevirtRequest(http.MethodGet, "/vms", "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&body)
			Expect(err).NotTo(HaveOccurred())

			vms, ok := body["vms"].([]interface{})
			Expect(ok).To(BeTrue())
			Expect(vms).NotTo(BeNil())
			Expect(vms).NotTo(BeEmpty())

			GinkgoWriter.Printf("Listed %d VMs\n", len(vms))
		})

		It("gets a specific VM [TC-12]", func() {
			if vmID == "" {
				Skip("No VM created (create skipped or failed)")
			}

			resp, err := doKubevirtRequest(http.MethodGet, "/vms/"+vmID, "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var body map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&body)
			Expect(err).NotTo(HaveOccurred())

			Expect(body).To(HaveKey("spec"))
			Expect(body).To(HaveKey("path"))

			path, _ := body["path"].(string)
			Expect(path).To(ContainSubstring(vmID))

			GinkgoWriter.Printf("Retrieved VM: %s\n", vmID)
		})

		It("deletes a VM [TC-18]", func() {
			if vmID == "" {
				Skip("No VM created (create skipped or failed)")
			}

			resp, err := doKubevirtRequest(http.MethodDelete, "/vms/"+vmID, "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

			resp, err = doKubevirtRequest(http.MethodGet, "/vms/"+vmID, "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))

			GinkgoWriter.Printf("Deleted VM: %s\n", vmID)
			vmID = ""
		})

		It("returns 404 for non-existent VM [TC-13]", func() {
			resp, err := doKubevirtRequest(http.MethodGet, "/vms/non-existent-vm-id", "")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})
	})

	Context("Validation", func() {
		It("rejects invalid VM spec", func() {
			Skip("TODO: Implement validation tests - define invalid payloads")
		})

		It("rejects request with missing required fields", func() {
			Skip("TODO: Implement validation tests for required fields")
		})
	})
})
