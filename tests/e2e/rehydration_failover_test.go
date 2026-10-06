//go:build e2e

package e2e_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Rehydration Failover", Label("rehydration", "failover", "disruptive"), func() {
	BeforeEach(func() {
		requireMultiProvider()
		requirePodman()
	})

	Context("agent failover", Ordered, func() {
		var (
			instanceUID string
			policyID    string
			providerA   ThreeTierProvider
			providerB   ThreeTierProvider
		)

		BeforeAll(func() {
			providerA = threeTierProviders[0]
			providerB = threeTierProviders[1]

			policyID = createPlacementPolicy("failover-initial",
				regoSelectProvider(providerA.Name))

			inst := createTestInstance(uniqueName("failover"), defaultUserValues())
			instanceUID = inst.UID

			waitForInstanceRunning(inst.ResourceID, provisionTimeout)
		})

		AfterAll(func() {
			startProvider(providerA)
			startProvider(providerB)
			waitForProviderHealth(providerA.Name, "ready", healthTimeout)
			waitForProviderHealth(providerB.Name, "ready", healthTimeout)

			if instanceUID != "" {
				deleteInstance(instanceUID)
			}
			if policyID != "" {
				deletePlacementPolicy(policyID)
			}
		})

		It("rehydrate after agent stop moves workload to healthy agent", Label("cluster"), func() {
			preInst := getInstance(instanceUID)
			origResourceID := preInst.ResourceID

			stopProvider(providerA)
			waitForProviderHealth(providerA.Name, "unavailable", healthTimeout)

			deletePlacementPolicy(policyID)
			policyID = createPlacementPolicy("failover-to-b",
				regoSelectProvider(providerB.Name))

			resp, body := rehydrateInstance(instanceUID)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			newResourceID := firstResourceID(body)
			Expect(newResourceID).NotTo(Equal(origResourceID))

			waitForInstanceRunning(newResourceID, rehydrateTimeout)

			if kubectlAvailable && providerB.Namespace != "" {
				ns := getActiveDeploymentNamespace(newResourceID)
				Expect(ns).To(Equal(providerB.Namespace),
					"new resource should be in provider B's namespace %s", providerB.Namespace)
			}
		})

		It("rehydrate back after provider restore (bidirectional failover)", Label("cluster"), func() {
			startProvider(providerA)
			waitForProviderHealth(providerA.Name, "ready", healthTimeout)

			preInst := getInstance(instanceUID)

			deletePlacementPolicy(policyID)
			policyID = createPlacementPolicy("failover-back-to-a",
				regoSelectProvider(providerA.Name))

			resp, body := rehydrateInstance(instanceUID)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			newResourceID := firstResourceID(body)
			Expect(newResourceID).NotTo(Equal(preInst.ResourceID))

			waitForInstanceRunning(newResourceID, rehydrateTimeout)

			if kubectlAvailable && providerA.Namespace != "" {
				ns := getActiveDeploymentNamespace(newResourceID)
				Expect(ns).To(Equal(providerA.Namespace),
					"resource should return to provider A's namespace")
			}
		})

		PIt("sequential failover leaves no orphaned resources", Label("cluster"), func() {
			// TODO: collect resource IDs from prior failover steps and verify
			// their deployments are cleaned up via countDeploymentsAcrossNamespaces
		})
	})

	Context("deferred delete", Ordered, func() {
		var (
			instanceUID    string
			oldResourceID  string
			policyID       string
			providerA      ThreeTierProvider
			providerB      ThreeTierProvider
		)

		BeforeAll(func() {
			providerA = threeTierProviders[0]
			providerB = threeTierProviders[1]

			policyID = createPlacementPolicy("deferred-delete",
				regoSelectProvider(providerA.Name))

			inst := createTestInstance(uniqueName("deferred"), defaultUserValues())
			instanceUID = inst.UID

			waitForInstanceRunning(inst.ResourceID, provisionTimeout)
			oldResourceID = inst.ResourceID

			stopProvider(providerA)
			waitForProviderHealth(providerA.Name, "unavailable", healthTimeout)

			deletePlacementPolicy(policyID)
			policyID = createPlacementPolicy("deferred-to-b",
				regoSelectProvider(providerB.Name))

			resp, body := rehydrateInstance(inst.UID)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			newResourceID := firstResourceID(body)
			Expect(newResourceID).NotTo(Equal(oldResourceID))
			waitForInstanceRunning(newResourceID, rehydrateTimeout)
		})

		AfterAll(func() {
			startProvider(providerA)
			startProvider(providerB)
			waitForProviderHealth(providerA.Name, "ready", healthTimeout)
			waitForProviderHealth(providerB.Name, "ready", healthTimeout)

			if instanceUID != "" {
				deleteInstance(instanceUID)
			}
			if policyID != "" {
				deletePlacementPolicy(policyID)
			}
		})

		PIt("old resource is queued for deferred deletion", Label("cluster"), func() {
			// TODO: query SPRM API with show_deleted=true to verify old resource_id
			// appears in the deferred deletion queue
		})

		It("cleanup completes when provider becomes healthy", Label("cluster"), func() {
			requireKubectl()

			startProvider(providerA)
			waitForProviderHealth(providerA.Name, "ready", healthTimeout)

			if providerA.Namespace != "" {
				waitForDeploymentsGone(providerA.Namespace, oldResourceID, cleanupTimeout)
			}
		})

		It("deferred cleanup retries after provider restore", func() {
			startProvider(providerA)
			waitForProviderHealth(providerA.Name, "ready", healthTimeout)

			current := getInstance(instanceUID)
			Expect(current.ResourceID).NotTo(Equal(oldResourceID),
				"active resource should be the new one, not the old deferred one")
		})
	})
})
