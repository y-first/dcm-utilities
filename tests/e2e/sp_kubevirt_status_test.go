//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("KubeVirt SP Status Monitoring", Label("sp", "kubevirt", "nats"), func() {
	var (
		nc     *nats.Conn
		vmID   string
		vmName string
	)

	BeforeEach(func() {
		requireKubevirtSP()
		requireNATS()

		// Connect to NATS
		natsURL := os.Getenv("DCM_NATS_URL")
		if natsURL == "" {
			natsURL = "nats://localhost:4222"
		}

		var err error
		nc, err = nats.Connect(natsURL)
		Expect(err).NotTo(HaveOccurred(), "NATS server should be reachable at %s", natsURL)

		GinkgoWriter.Printf("Connected to NATS at %s\n", natsURL)
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}

		// Cleanup VM if created
		if vmID != "" {
			resp, err := doKubevirtRequest("DELETE", "/vms/"+vmID, "")
			if err == nil && resp != nil {
				resp.Body.Close()
			}
			vmID = ""
		}
	})

	Context("VM Status Events", func() {
		It("publishes status events when VM is created [TC-22]", func() {
			// Subscribe to VM status subject before creating VM
			sub, err := nc.SubscribeSync("dcm.status.vm.>")
			Expect(err).NotTo(HaveOccurred())
			defer sub.Unsubscribe()

			// Flush to ensure subscription is registered
			nc.Flush()

			// Create VM
			vmName = uniqueName("e2e-nats-vm")
			spec := newTestVMSpec(vmName)
			payload, err := json.Marshal(map[string]interface{}{"spec": spec})
			Expect(err).NotTo(HaveOccurred())

			resp, err := doKubevirtRequest("POST", "/vms", string(payload))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(201))

			var createResp map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&createResp)
			Expect(err).NotTo(HaveOccurred())

			vmID = extractIDFromPath(createResp["path"].(string))
			Expect(vmID).NotTo(BeEmpty())

			GinkgoWriter.Printf("Created VM %s, waiting for NATS events...\n", vmID)

			// Wait for at least one NATS event (allow up to 30s)
			msg, err := sub.NextMsg(30 * time.Second)
			Expect(err).NotTo(HaveOccurred(), "should receive at least one NATS status event")

			// Validate CloudEvent schema
			var event map[string]interface{}
			err = json.Unmarshal(msg.Data, &event)
			Expect(err).NotTo(HaveOccurred(), "NATS message should be valid JSON")

			// Validate CloudEvent required fields
			Expect(event).To(HaveKey("specversion"), "CloudEvent should have specversion")
			Expect(event).To(HaveKey("type"), "CloudEvent should have type")
			Expect(event).To(HaveKey("source"), "CloudEvent should have source")
			Expect(event).To(HaveKey("data"), "CloudEvent should have data")

			// Validate event data payload
			data, ok := event["data"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "CloudEvent data should be an object")

			// Verify instance_id matches
			Expect(data).To(HaveKey("instance_id"), "Event data should have instance_id")
			Expect(data["instance_id"]).To(Equal(vmID), "Event instance_id should match VM ID")

			// Verify service_type
			Expect(data).To(HaveKey("service_type"), "Event data should have service_type")
			Expect(data["service_type"]).To(Equal("vm"), "Event service_type should be 'vm'")

			// Verify status is one of the expected values
			Expect(data).To(HaveKey("status"), "Event data should have status")
			status := data["status"].(string)
			Expect(status).To(BeElementOf("PENDING", "PROVISIONING", "RUNNING"),
				"Event status should be PENDING, PROVISIONING, or RUNNING")

			// Verify timestamp exists (don't validate format for now)
			Expect(data).To(HaveKey("timestamp"), "Event data should have timestamp")

			GinkgoWriter.Printf("✓ Received valid NATS event: status=%s, instance_id=%s\n", status, vmID)
		})

		It("publishes state transitions as VM starts [TC-21]", func() {
			// Subscribe to VM status events
			sub, err := nc.SubscribeSync("dcm.status.vm.>")
			Expect(err).NotTo(HaveOccurred())
			defer sub.Unsubscribe()

			nc.Flush()

			// Create VM
			vmName = uniqueName("e2e-transition-vm")
			spec := newTestVMSpec(vmName)
			payload, err := json.Marshal(map[string]interface{}{"spec": spec})
			Expect(err).NotTo(HaveOccurred())

			resp, err := doKubevirtRequest("POST", "/vms", string(payload))
			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(201))

			// Collect status events over time
			var statuses []string
			timeout := time.After(120 * time.Second)
			seenRunning := false

		eventLoop:
			for {
				select {
				case <-timeout:
					GinkgoWriter.Printf("Timeout reached, collected %d events\n", len(statuses))
					break eventLoop
				default:
					msg, err := sub.NextMsg(5 * time.Second)
					if err != nil {
						// No more immediate events, exit loop
						GinkgoWriter.Printf("No more events after %d collected\n", len(statuses))
						break eventLoop
					}

					var event map[string]interface{}
					if err := json.Unmarshal(msg.Data, &event); err != nil {
						continue // Skip malformed events
					}

					data, ok := event["data"].(map[string]interface{})
					if !ok {
						continue
					}

					status, ok := data["status"].(string)
					if !ok {
						continue
					}

					statuses = append(statuses, status)
					GinkgoWriter.Printf("Event %d: status=%s\n", len(statuses), status)

					if status == "RUNNING" {
						seenRunning = true
						// Wait a bit more for any final events
						time.Sleep(2 * time.Second)
						break eventLoop
					}
				}
			}

			// Verify we received at least some events
			Expect(statuses).NotTo(BeEmpty(), "should receive at least one status event")

			// Verify we saw PENDING at some point
			Expect(statuses).To(ContainElement("PENDING"),
				"should receive PENDING status event during VM creation")

			// If VM reached RUNNING, verify PENDING came before RUNNING
			if seenRunning {
				Expect(statuses).To(ContainElement("RUNNING"),
					"should receive RUNNING status eventually")

				// Find indices
				firstPending := -1
				lastRunning := -1
				for i, s := range statuses {
					if s == "PENDING" && firstPending == -1 {
						firstPending = i
					}
					if s == "RUNNING" {
						lastRunning = i
					}
				}

				Expect(firstPending).To(BeNumerically("<", lastRunning),
					"PENDING event should come before RUNNING event")
			}

			GinkgoWriter.Printf("✓ Observed status transitions: %v\n", statuses)
			GinkgoWriter.Printf("✓ Total events: %d, transitions tracked successfully\n", len(statuses))
		})
	})
})
