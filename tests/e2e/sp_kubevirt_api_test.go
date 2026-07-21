//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KubeVirt Service Provider API", Label("sp", "kubevirt"), func() {
	BeforeEach(func() {
		requireKubevirtSP()
	})

	Context("Health endpoint", func() {
		It("returns healthy status when cluster is reachable", func() {
			resp, err := doKubevirtRequest(http.MethodGet, "/health", "")
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

	Context("VM CRUD operations", func() {
		var vmID string

		AfterEach(func() {
			if vmID != "" {
				// Cleanup: delete the VM
				resp, err := doKubevirtRequest(http.MethodDelete, "/vms/"+vmID, "")
				if err == nil && resp != nil {
					resp.Body.Close()
				}
				vmID = ""
			}
		})

		It("creates a VM with valid spec", func() {
			Skip("TODO: Implement CreateVM test - requires cluster access and proper payload format")

			vmName := uniqueName("e2e-kubevirt-vm")
			spec := newTestVMSpec(vmName)

			payload, err := json.Marshal(spec)
			Expect(err).NotTo(HaveOccurred())

			resp, err := doKubevirtRequest(http.MethodPost, "/vms", string(payload))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			var body map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&body)
			Expect(err).NotTo(HaveOccurred())

			// Extract VM ID from response path
			path, ok := body["path"].(string)
			Expect(ok).To(BeTrue())
			Expect(path).To(ContainSubstring("/vms/"))

			// TODO: Extract actual ID from path
			// vmID = extractIDFromPath(path)
		})

		It("lists VMs", func() {
			Skip("TODO: Implement ListVMs test - requires cluster access")

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
		})

		It("gets a specific VM", func() {
			Skip("TODO: Implement GetVM test - requires creating a VM first")
		})

		It("deletes a VM", func() {
			Skip("TODO: Implement DeleteVM test - requires creating a VM first")
		})

		It("returns 404 for non-existent VM", func() {
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
