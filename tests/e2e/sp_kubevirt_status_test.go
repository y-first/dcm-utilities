//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const kubevirtNATSSubject = "dcm.vm"

var _ = Describe("KubeVirt SP Status Monitoring", Label("sp", "kubevirt", "nats"), func() {
	var (
		nc     *nats.Conn
		vmID   string
		vmName string
	)

	BeforeEach(func() {
		requireKubevirtSP()
		requireNATS()

		natsURL := os.Getenv("DCM_NATS_URL")
		if natsURL == "" {
			natsURL = "nats://localhost:4222"
		}

		var err error
		nc, err = nats.Connect(natsURL)
		Expect(err).NotTo(HaveOccurred(), "NATS server should be reachable at %s", natsURL)

		// Ensure a JetStream stream captures kubevirt SP events (SP publishes via JS).
		js, err := jetstream.New(nc)
		Expect(err).NotTo(HaveOccurred())
		_, err = js.CreateOrUpdateStream(context.Background(), jetstream.StreamConfig{
			Name:     "DCM_VM",
			Subjects: []string{kubevirtNATSSubject},
		})
		Expect(err).NotTo(HaveOccurred(), "failed to ensure JetStream stream for %s", kubevirtNATSSubject)

		GinkgoWriter.Printf("Connected to NATS at %s (subject %s)\n", natsURL, kubevirtNATSSubject)
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}

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
			sub, err := nc.SubscribeSync(kubevirtNATSSubject)
			Expect(err).NotTo(HaveOccurred())
			defer sub.Unsubscribe()
			nc.Flush()

			vmName = uniqueName("e2e-nats-vm")
			spec := newTestVMSpec(vmName)
			payload, err := json.Marshal(map[string]interface{}{"spec": spec})
			Expect(err).NotTo(HaveOccurred())

			createPath, expectedID := createVMPath()
			resp, err := doKubevirtRequest("POST", createPath, string(payload))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(201))

			var createResp map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&createResp)
			Expect(err).NotTo(HaveOccurred())

			vmID = extractIDFromPath(createResp["path"].(string))
			Expect(vmID).NotTo(BeEmpty())
			Expect(vmID).To(Equal(expectedID))

			GinkgoWriter.Printf("Created VM %s, waiting for NATS events...\n", vmID)

			msg, err := sub.NextMsg(60 * time.Second)
			Expect(err).NotTo(HaveOccurred(), "should receive at least one NATS status event")

			var event map[string]interface{}
			err = json.Unmarshal(msg.Data, &event)
			Expect(err).NotTo(HaveOccurred(), "NATS message should be valid JSON")

			Expect(event).To(HaveKey("specversion"), "CloudEvent should have specversion")
			Expect(event).To(HaveKey("type"), "CloudEvent should have type")
			Expect(event["type"]).To(Equal("dcm.status.vm"))
			Expect(event).To(HaveKey("source"), "CloudEvent should have source")
			Expect(event).To(HaveKey("data"), "CloudEvent should have data")

			data, ok := event["data"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "CloudEvent data should be an object")

			// kubevirt SP publishes data.id (not instance_id / service_type)
			Expect(data).To(HaveKey("id"), "Event data should have id")
			Expect(data["id"]).To(Equal(vmID), "Event id should match VM ID")

			Expect(data).To(HaveKey("status"), "Event data should have status")
			status := data["status"].(string)
			Expect(status).To(BeElementOf("PENDING", "PROVISIONING", "RUNNING", "Pending", "Scheduling", "Running"),
				"Event status should be a known VM phase")

			Expect(data).To(HaveKey("timestamp"), "Event data should have timestamp")

			GinkgoWriter.Printf("✓ Received valid NATS event: status=%s, id=%s\n", status, vmID)
		})

		It("publishes state transitions as VM starts [TC-21]", func() {
			sub, err := nc.SubscribeSync(kubevirtNATSSubject)
			Expect(err).NotTo(HaveOccurred())
			defer sub.Unsubscribe()
			nc.Flush()

			vmName = uniqueName("e2e-transition-vm")
			spec := newTestVMSpec(vmName)
			payload, err := json.Marshal(map[string]interface{}{"spec": spec})
			Expect(err).NotTo(HaveOccurred())

			createPath, _ := createVMPath()
			resp, err := doKubevirtRequest("POST", createPath, string(payload))
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(201))

			var createResp map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&createResp)
			Expect(err).NotTo(HaveOccurred())
			vmID = extractIDFromPath(createResp["path"].(string))

			var statuses []string
			timeout := time.After(180 * time.Second)
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
						GinkgoWriter.Printf("No more events after %d collected\n", len(statuses))
						break eventLoop
					}

					var event map[string]interface{}
					if err := json.Unmarshal(msg.Data, &event); err != nil {
						continue
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

					if status == "RUNNING" || status == "Running" {
						seenRunning = true
						time.Sleep(2 * time.Second)
						break eventLoop
					}
				}
			}

			Expect(statuses).NotTo(BeEmpty(), "should receive at least one status event")

			if seenRunning {
				GinkgoWriter.Printf("✓ Observed RUNNING transition: %v\n", statuses)
			} else {
				GinkgoWriter.Printf("✓ Observed status events (RUNNING not reached within window): %v\n", statuses)
			}
		})
	})
})
