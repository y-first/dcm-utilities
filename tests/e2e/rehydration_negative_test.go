//go:build e2e

package e2e_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Rehydration Negative Cases", Label("rehydration", "negative"), func() {
	BeforeEach(func() {
		requireThreeTierSP()
	})

	It("returns 404 for non-existent instance", func() {
		resp, _ := rehydrateInstanceRaw("does-not-exist-" + uniqueName("tc14"))
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("returns 424 when no policies exist", func() {
		provider := threeTierProviders[0]
		policyID := createPlacementPolicy("tc15-policy", regoSelectProvider(provider.Name))

		inst := createTestInstance(uniqueName("tc15"), defaultUserValues())
		defer deleteInstance(inst.UID)

		waitForInstanceRunning(inst.ResourceID, provisionTimeout)

		deletePlacementPolicy(policyID)
		deleteAllPolicies()

		resp, _ := rehydrateInstanceRaw(inst.UID)
		Expect(resp.StatusCode).To(Equal(http.StatusFailedDependency)) // 424
	})

		It("returns 422 when all agents are unhealthy", Label("disruptive"), func() {
		requirePodman()

		provider := threeTierProviders[0]
		policyID := createPlacementPolicy("tc16-policy", regoSelectProvider(provider.Name))
		defer deletePlacementPolicy(policyID)

		inst := createTestInstance(uniqueName("tc16"), defaultUserValues())
		defer deleteInstance(inst.UID)

		waitForInstanceRunning(inst.ResourceID, provisionTimeout)
		origResourceID := inst.ResourceID

		for _, p := range threeTierProviders {
			if p.ContainerName != "" {
				stopProvider(p)
				defer startProvider(p)
			}
		}

		for _, p := range threeTierProviders {
			waitForProviderHealth(p.Name, "unavailable", healthTimeout)
		}

		resp, _ := rehydrateInstanceRaw(inst.UID)
		Expect(resp.StatusCode).To(SatisfyAny(
			Equal(422), // provider error
			Equal(http.StatusNotAcceptable), // 406 policy rejected
		))

		// Error preserves original resource
		current := getInstance(inst.UID)
		Expect(current.ResourceID).To(Equal(origResourceID),
			"resource_id should be unchanged after failed rehydration")
	})

	It("returns 404 for deleted instance", func() {
		provider := threeTierProviders[0]
		policyID := createPlacementPolicy("tc17-policy", regoSelectProvider(provider.Name))
		defer deletePlacementPolicy(policyID)

		inst := createTestInstance(uniqueName("tc17"), defaultUserValues())
		waitForInstanceRunning(inst.ResourceID, provisionTimeout)

		deleteInstance(inst.UID)

		resp, _ := rehydrateInstanceRaw(inst.UID)
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})

	// Error preservation is tested inline with the all-unhealthy test above
})
