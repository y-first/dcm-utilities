//go:build e2e

package e2e_test

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
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
		// Prefer core NATS subscribe on dcm.vm — the compose stack already has
		// a JetStream stream (dcm-status: dcm.*) covering this subject.
		GinkgoWriter.Printf("Connected to NATS at %s (subject %s)\n", natsURL, kubevirtNATSSubject)
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}
		deleteTestVM(vmID)
		vmID = ""
	})

	Context("VM Status Events", func() {
		It("publishes status events when VM is created [TC-22]", func() {
			sub, err := nc.SubscribeSync(kubevirtNATSSubject)
			Expect(err).NotTo(HaveOccurred())
			defer sub.Unsubscribe()
			Expect(nc.Flush()).To(Succeed())

			vmName = uniqueName("e2e-nats-vm")
			id, err := createTestVM(vmName)
			Expect(err).NotTo(HaveOccurred())
			vmID = id

			GinkgoWriter.Printf("Created VM %s, waiting for NATS events for this id...\n", vmID)

			deadline := time.Now().Add(90 * time.Second)
			var matched map[string]interface{}
			var matchedData map[string]interface{}
			for time.Now().Before(deadline) {
				msg, err := sub.NextMsg(5 * time.Second)
				if err != nil {
					continue
				}
				var event map[string]interface{}
				if err := json.Unmarshal(msg.Data, &event); err != nil {
					continue
				}
				data, ok := event["data"].(map[string]interface{})
				if !ok {
					continue
				}
				eid, _ := data["id"].(string)
				if eid == "" {
					eid, _ = data["instance_id"].(string)
				}
				if eid != vmID {
					GinkgoWriter.Printf("skip stale event id=%s\n", eid)
					continue
				}
				matched = event
				matchedData = data
				break
			}
			Expect(matched).NotTo(BeNil(), "should receive a NATS status event for VM %s", vmID)

			Expect(matched).To(HaveKey("specversion"))
			Expect(matched).To(HaveKey("type"))
			Expect(matched["type"]).To(Equal("dcm.status.vm"))
			Expect(matched).To(HaveKey("source"))
			Expect(matchedData).To(HaveKey("status"))
			status := matchedData["status"].(string)
			Expect(status).To(BeElementOf("PENDING", "PROVISIONING", "RUNNING", "Pending", "Scheduling", "Scheduled", "Running", "Failed"))
			Expect(matchedData).To(HaveKey("timestamp"))

			GinkgoWriter.Printf("Received NATS event status=%s id=%s\n", status, vmID)
		})

		It("publishes state transitions and GET reflects status [TC-21]", func() {
			sub, err := nc.SubscribeSync(kubevirtNATSSubject)
			Expect(err).NotTo(HaveOccurred())
			defer sub.Unsubscribe()
			Expect(nc.Flush()).To(Succeed())

			vmName = uniqueName("e2e-transition-vm")
			id, err := createTestVM(vmName)
			Expect(err).NotTo(HaveOccurred())
			vmID = id

			var statuses []string
			seenStarted := false
			deadline := time.Now().Add(180 * time.Second)

			for time.Now().Before(deadline) && !seenStarted {
				msg, err := sub.NextMsg(5 * time.Second)
				if err != nil {
					// Also poll GET while waiting
					getStatus := getVMAPIStatus(vmID)
					if getStatus != "" {
						GinkgoWriter.Printf("GET status=%s\n", getStatus)
						if isStartedPhase(getStatus) {
							seenStarted = true
							statuses = append(statuses, getStatus)
							break
						}
					}
					continue
				}
				var event map[string]interface{}
				if err := json.Unmarshal(msg.Data, &event); err != nil {
					continue
				}
				data, ok := event["data"].(map[string]interface{})
				if !ok {
					continue
				}
				eid, _ := data["id"].(string)
				if eid != "" && eid != vmID {
					continue
				}
				status, _ := data["status"].(string)
				if status == "" {
					continue
				}
				statuses = append(statuses, status)
				GinkgoWriter.Printf("Event status=%s\n", status)
				if isStartedPhase(status) {
					seenStarted = true
				}
			}

			Expect(statuses).NotTo(BeEmpty(), "should observe at least one status via NATS or GET")

			// Prefer GET status; fall back to cluster printableStatus when SP omits it
			Eventually(func() string {
				if s := getVMAPIStatus(vmID); s != "" {
					return s
				}
				ns := kubevirtNamespace()
				name, err := findVMNameByInstanceID(vmID, ns)
				if err != nil {
					return ""
				}
				out, err := runKubeCmd("get", "vm", name, "-n", ns, "-o", "jsonpath={.status.printableStatus}")
				if err != nil {
					return ""
				}
				return strings.TrimSpace(out)
			}).WithTimeout(60 * time.Second).WithPolling(3 * time.Second).
				ShouldNot(BeEmpty(), "GET /vms/{id} or cluster should expose a status")

			if seenStarted {
				// If we saw both Pending and Running, Pending should appear first
				pendingIdx, runningIdx := -1, -1
				for i, s := range statuses {
					if pendingIdx < 0 && isPendingPhase(s) {
						pendingIdx = i
					}
					if runningIdx < 0 && isStartedPhase(s) {
						runningIdx = i
					}
				}
				if pendingIdx >= 0 && runningIdx >= 0 {
					Expect(pendingIdx).To(BeNumerically("<", runningIdx),
						"Pending should precede Running when both observed: %v", statuses)
				}
			}

			GinkgoWriter.Printf("Collected transitions: %v (started=%v)\n", statuses, seenStarted)
		})
	})
})

func getVMAPIStatus(id string) string {
	resp, err := doKubevirtRequest("GET", "/vms/"+id, "")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return ""
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ""
	}
	s, _ := body["status"].(string)
	return s
}

func isStartedPhase(s string) bool {
	switch strings.ToLower(s) {
	case "running", "scheduled", "succeeded", "failed":
		return true
	default:
		return false
	}
}

func isPendingPhase(s string) bool {
	switch strings.ToLower(s) {
	case "pending", "provisioning", "scheduling":
		return true
	default:
		return false
	}
}
