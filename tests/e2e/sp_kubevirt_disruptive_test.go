//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Disruptive tests mutate SP/cluster connectivity. Enable with DCM_DISRUPTIVE=1.
var _ = Describe("KubeVirt SP Disruptive", Label("sp", "kubevirt", "disruptive"), func() {
	BeforeEach(func() {
		requireKubevirtSP()
		requireDisruptive()
	})

	Context("Registration retries", func() {
		It("retries registration when SPRM is unavailable [TC-02]", func() {
			Skip("requires stopping control-plane/SPRM then restarting kubevirt-service-provider; automate via podman when safe")
		})

		It("stops retrying registration on 4xx [TC-03]", func() {
			Skip("requires injecting an invalid registration payload and inspecting SP logs")
		})
	})

	Context("Health unhealthy [TC-05]", func() {
		It("returns unhealthy when cluster is unreachable", func() {
			container := os.Getenv("DCM_KUBEVIRT_SP_CONTAINER")
			if container == "" {
				container = "kubevirt-service-provider"
			}

			if err := exec.Command("podman", "inspect", container).Run(); err != nil {
				Skip(fmt.Sprintf("podman inspect %s failed: %v", container, err))
			}

			backup, err := exec.Command("podman", "exec", container, "cat", "/etc/hosts").Output()
			if err != nil {
				Skip("cannot read /etc/hosts in SP container: " + err.Error())
			}

			poison := append(append([]byte{}, backup...), []byte("\n0.0.0.0 kubernetes.default.svc kubernetes.default.svc.cluster.local\n")...)
			cmd := exec.Command("podman", "exec", "-i", container, "tee", "/etc/hosts")
			cmd.Stdin = bytes.NewReader(poison)
			if out, err := cmd.CombinedOutput(); err != nil {
				Skip(fmt.Sprintf("cannot poison /etc/hosts: %v: %s", err, string(out)))
			}
			DeferCleanup(func() {
				cmd := exec.Command("podman", "exec", "-i", container, "tee", "/etc/hosts")
				cmd.Stdin = bytes.NewReader(backup)
				_ = cmd.Run()
				time.Sleep(2 * time.Second)
			})

			Eventually(func() string {
				resp, err := doKubevirtRequest(http.MethodGet, "/vms/health", "")
				if err != nil {
					return ""
				}
				defer resp.Body.Close()
				var body map[string]interface{}
				_ = json.NewDecoder(resp.Body).Decode(&body)
				s, _ := body["status"].(string)
				GinkgoWriter.Printf("health status=%s code=%d\n", s, resp.StatusCode)
				return s
			}).WithTimeout(120 * time.Second).WithPolling(5 * time.Second).
				Should(Equal("unhealthy"), "health should report unhealthy when cluster unreachable")
		})
	})

	Context("Delete errors [TC-20]", func() {
		It("returns an error when KubeVirt access fails", func() {
			Skip("requires temporary RBAC revoke on the SP service account; not automated in default env")
		})
	})

	Context("Namespace isolation [TC-30]", func() {
		It("creates VMs only in the configured namespace", func() {
			if err := checkClusterAccess(); err != nil {
				Skip(err.Error())
			}
			ns := kubevirtNamespace()
			id, err := createTestVM(uniqueName("e2e-ns"))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { deleteTestVM(id) })

			Eventually(func() error {
				name, err := findVMNameByInstanceID(id, ns)
				if err != nil {
					return err
				}
				vm, err := getVMFromCluster(name, ns)
				if err != nil {
					return err
				}
				md, _ := vm["metadata"].(map[string]interface{})
				gotNS, _ := md["namespace"].(string)
				if gotNS != ns {
					return fmt.Errorf("vm namespace %q != %q", gotNS, ns)
				}
				return nil
			}).WithTimeout(60 * time.Second).WithPolling(2 * time.Second).Should(Succeed())

			otherNS := "default"
			manual := uniqueName("e2e-other-ns")
			if err := createUnlabeledVM(manual, otherNS); err != nil {
				Skip("cannot create VM in other namespace: " + err.Error())
			}
			DeferCleanup(func() { _ = deleteVMFromCluster(manual, otherNS) })

			listed, err := listVMIDs()
			Expect(err).NotTo(HaveOccurred())
			Expect(listed).NotTo(ContainElement(manual))
		})
	})
})
