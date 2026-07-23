//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	kubevirtSPURL     string
	kubevirtSPOnce    sync.Once
	kubevirtSPSkipped bool
)

// initKubevirtSP initializes the KubeVirt SP URL from env or defaults
func initKubevirtSP() {
	kubevirtSPOnce.Do(func() {
		kubevirtSPURL = os.Getenv("DCM_KUBEVIRT_SP_URL")
		if kubevirtSPURL == "" {
			kubevirtSPURL = "http://localhost:8081/api/v1alpha1"
		}

		// Health lives under /vms/health (same pattern as container/acm SPs)
		resp, err := http.Get(kubevirtSPURL + "/vms/health")
		if err != nil || resp.StatusCode != http.StatusOK {
			GinkgoWriter.Printf("KubeVirt SP not reachable at %s (tests will skip)\n", kubevirtSPURL)
			kubevirtSPSkipped = true
			if resp != nil {
				resp.Body.Close()
			}
			return
		}
		resp.Body.Close()
		GinkgoWriter.Printf("KubeVirt SP available at: %s\n", kubevirtSPURL)
	})
}

// requireKubevirtSP skips the test if KubeVirt SP is not available
func requireKubevirtSP() {
	initKubevirtSP()
	if kubevirtSPSkipped {
		Skip("KubeVirt SP not available (deploy with --kubevirt-service-provider; port 8081 published via compose-kubevirt-sp.yaml)")
	}
}

// requireNATS skips the test if NATS is not reachable at DCM_NATS_URL.
func requireNATS() {
	url := os.Getenv("DCM_NATS_URL")
	if url == "" {
		url = "nats://localhost:4222"
	}

	nc, err := nats.Connect(url, nats.Timeout(2*time.Second), nats.Name("dcm-e2e-kubevirt-nats-check"))
	if err != nil {
		Skip(fmt.Sprintf("NATS server not available at %s: %v", url, err))
	}
	nc.Close()
}

// doKubevirtRequest performs HTTP request against the KubeVirt SP
func doKubevirtRequest(method, path, payload string) (*http.Response, error) {
	return doRequestToURL(kubevirtSPURL+path, method, payload)
}

// createVMPath returns POST /vms?id=<uuid>. The current kubevirt SP panics when
// the optional id query param is omitted (*request.Params.Id nil deref).
func createVMPath() (path string, id string) {
	id = uuid.NewString()
	return "/vms?id=" + id, id
}

// doRequestToURL performs HTTP request to a specific URL
func doRequestToURL(url, method, payload string) (*http.Response, error) {
	var bodyReader io.Reader
	if payload != "" {
		bodyReader = bytes.NewBufferString(payload)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	return (&http.Client{}).Do(req)
}

// VMSpec represents a minimal VM specification for testing
type VMSpec struct {
	ServiceType string      `json:"service_type"`
	Metadata    VMMetadata  `json:"metadata"`
	GuestOS     VMGuestOS   `json:"guest_os"`
	Vcpu        VMVcpu      `json:"vcpu"`
	Memory      VMMemory    `json:"memory"`
	Storage     VMStorage   `json:"storage"`
}

type VMMetadata struct {
	Name string `json:"name"`
}

type VMGuestOS struct {
	Type string `json:"type"`
}

type VMVcpu struct {
	Count int `json:"count"`
}

type VMMemory struct {
	Size string `json:"size"`
}

type VMStorage struct {
	Disks []VMDisk `json:"disks"`
}

type VMDisk struct {
	Name     string `json:"name"`
	Capacity string `json:"capacity"`
}

// newTestVMSpec creates a minimal VM spec for testing
func newTestVMSpec(name string) VMSpec {
	return VMSpec{
		ServiceType: "vm",
		Metadata: VMMetadata{
			Name: name,
		},
		GuestOS: VMGuestOS{
			Type: "linux",
		},
		Vcpu: VMVcpu{
			Count: 1,
		},
		Memory: VMMemory{
			// OpenAPI requires ^[0-9]+(MB|GB|TB)$ — not Kubernetes units like Gi
			Size: "1GB",
		},
		Storage: VMStorage{
			Disks: []VMDisk{
				{
					Name:     "boot",
					Capacity: "10GB",
				},
			},
		},
	}
}

// getKubevirtProviderName discovers the KubeVirt provider name from DCM
func getKubevirtProviderName() (string, error) {
	resp, err := doRequest(http.MethodGet, "/providers?type=vm", "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to get providers: status %d", resp.StatusCode)
	}

	var body map[string]interface{}
	decodeJSON(resp, &body)

	providers, ok := body["providers"].([]interface{})
	if !ok || len(providers) == 0 {
		return "", fmt.Errorf("no vm providers registered")
	}

	p := providers[0].(map[string]interface{})
	name, ok := p["name"].(string)
	if !ok || name == "" {
		return "", fmt.Errorf("provider name not found")
	}

	return name, nil
}

// extractIDFromPath extracts the instance ID from a path like "/api/v1alpha1/vms/abc-123"
func extractIDFromPath(path string) string {
	// Find the last segment after the final slash
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}

// getVMFromCluster fetches VirtualMachine resource via kubectl/oc
func getVMFromCluster(vmName, namespace string) (map[string]interface{}, error) {
	// Try oc first, fall back to kubectl
	cmd := exec.Command("oc", "get", "vm", vmName, "-n", namespace, "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		// Try kubectl as fallback
		cmd = exec.Command("kubectl", "get", "vm", vmName, "-n", namespace, "-o", "json")
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to get vm %s: %w", vmName, err)
		}
	}

	var vm map[string]interface{}
	if err := json.Unmarshal(out, &vm); err != nil {
		return nil, fmt.Errorf("failed to parse vm json: %w", err)
	}
	return vm, nil
}

// DCM label keys used by kubevirt-service-provider.
const (
	dcmLabelManagedBy  = "dcm.project/managed-by"
	dcmLabelInstanceID = "dcm.project/dcm-instance-id"
	dcmManagedByValue  = "dcm"
)

// kubevirtNamespace returns the namespace where the SP creates VMs.
func kubevirtNamespace() string {
	if ns := os.Getenv("KUBERNETES_NAMESPACE"); ns != "" {
		return ns
	}
	if ns := os.Getenv("KUBEVIRT_NAMESPACE"); ns != "" {
		return ns
	}
	return "vms"
}

// requireDisruptive skips unless DCM_DISRUPTIVE=1.
func requireDisruptive() {
	if os.Getenv("DCM_DISRUPTIVE") != "1" {
		Skip("disruptive test skipped (set DCM_DISRUPTIVE=1 to enable)")
	}
}

// expectProblemDetails asserts an RFC 7807-ish problem body (type + title at minimum).
func expectProblemDetails(resp *http.Response) map[string]interface{} {
	GinkgoHelper()
	var problem map[string]interface{}
	Expect(json.NewDecoder(resp.Body).Decode(&problem)).To(Succeed())
	Expect(problem).To(HaveKey("type"))
	Expect(problem).To(HaveKey("title"))
	return problem
}

// runKubeCmd tries oc then kubectl with the given args.
func runKubeCmd(args ...string) (string, error) {
	cmd := exec.Command("oc", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), nil
	}
	cmd = exec.Command("kubectl", args...)
	out, err = cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, string(out))
	}
	return string(out), nil
}

// findVMNameByInstanceID looks up the cluster VM name via DCM instance-id label.
func findVMNameByInstanceID(instanceID, namespace string) (string, error) {
	out, err := runKubeCmd("get", "vm", "-n", namespace,
		"-l", dcmLabelInstanceID+"="+instanceID,
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" {
		return "", fmt.Errorf("no VM found with %s=%s in ns %s", dcmLabelInstanceID, instanceID, namespace)
	}
	return name, nil
}

// getVMByInstanceID fetches the VirtualMachine for a DCM instance id.
func getVMByInstanceID(instanceID, namespace string) (map[string]interface{}, string, error) {
	name, err := findVMNameByInstanceID(instanceID, namespace)
	if err != nil {
		return nil, "", err
	}
	vm, err := getVMFromCluster(name, namespace)
	return vm, name, err
}

func labelMap(obj map[string]interface{}, path ...string) (map[string]interface{}, error) {
	cur := obj
	for i, key := range path {
		next, ok := cur[key].(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("missing path %v at %s", path[:i+1], key)
		}
		cur = next
	}
	return cur, nil
}

// verifyDCMLabels checks VirtualMachine (and template) have required DCM labels.
func verifyDCMLabels(vmName, namespace, expectedInstanceID string) error {
	vm, err := getVMFromCluster(vmName, namespace)
	if err != nil {
		return err
	}

	labels, err := labelMap(vm, "metadata", "labels")
	if err != nil {
		return err
	}

	managedBy, _ := labels[dcmLabelManagedBy].(string)
	if managedBy != dcmManagedByValue {
		return fmt.Errorf("missing or incorrect %s: got %q, want %q", dcmLabelManagedBy, managedBy, dcmManagedByValue)
	}

	instanceID, _ := labels[dcmLabelInstanceID].(string)
	if instanceID != expectedInstanceID {
		return fmt.Errorf("%s mismatch: got %q, want %q", dcmLabelInstanceID, instanceID, expectedInstanceID)
	}

	tmplLabels, err := labelMap(vm, "spec", "template", "metadata", "labels")
	if err != nil {
		return fmt.Errorf("template labels: %w", err)
	}
	if tmplManaged, _ := tmplLabels[dcmLabelManagedBy].(string); tmplManaged != dcmManagedByValue {
		return fmt.Errorf("template missing %s", dcmLabelManagedBy)
	}
	if tmplID, _ := tmplLabels[dcmLabelInstanceID].(string); tmplID != expectedInstanceID {
		return fmt.Errorf("template %s mismatch: got %q, want %q", dcmLabelInstanceID, tmplID, expectedInstanceID)
	}

	return nil
}

// createUnlabeledVM creates a minimal VirtualMachine without DCM labels (for TC-16).
func createUnlabeledVM(name, namespace string) error {
	yaml := fmt.Sprintf(`apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %s
  namespace: %s
spec:
  runStrategy: Halted
  template:
    metadata:
      labels:
        kubevirt.io/domain: %s
    spec:
      domain:
        devices:
          disks:
          - name: containerdisk
            disk:
              bus: virtio
        resources:
          requests:
            memory: 64Mi
      volumes:
      - name: containerdisk
        containerDisk:
          image: quay.io/kubevirt/cirros-container-disk-demo:latest
`, name, namespace, name)
	cmd := exec.Command("oc", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yaml)
	if out, err := cmd.CombinedOutput(); err != nil {
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(yaml)
		out, err = cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("apply unlabeled vm: %w: %s", err, string(out))
		}
	}
	return nil
}

// patchVMRunning sets spec.running (legacy) or runStrategy for stop/start (TC-32).
func setVMRunStrategy(name, namespace, strategy string) error {
	patch := fmt.Sprintf(`{"spec":{"runStrategy":%q}}`, strategy)
	_, err := runKubeCmd("patch", "vm", name, "-n", namespace, "--type=merge", "-p", patch)
	return err
}

// getPVCsForVM lists PVCs in the namespace (best-effort for storage class checks).
func getPVCsInNamespace(namespace string) ([]map[string]interface{}, error) {
	out, err := runKubeCmd("get", "pvc", "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}
	var list map[string]interface{}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, err
	}
	items, _ := list["items"].([]interface{})
	var result []map[string]interface{}
	for _, it := range items {
		if m, ok := it.(map[string]interface{}); ok {
			result = append(result, m)
		}
	}
	return result, nil
}

// createTestVM posts a VM to the SP and returns instance id + request name.
func createTestVM(name string) (id string, err error) {
	spec := newTestVMSpec(name)
	payload, err := json.Marshal(map[string]interface{}{"spec": spec})
	if err != nil {
		return "", err
	}
	path, expectedID := createVMPath()
	resp, err := doKubevirtRequest(http.MethodPost, path, string(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create VM status %d: %s", resp.StatusCode, string(body))
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}
	pathStr, _ := parsed["path"].(string)
	id = extractIDFromPath(pathStr)
	if id == "" {
		id = expectedID
	}
	return id, nil
}

// deleteTestVM best-effort deletes a VM via the SP API.
func deleteTestVM(id string) {
	if id == "" {
		return
	}
	resp, err := doKubevirtRequest(http.MethodDelete, "/vms/"+id, "")
	if err == nil && resp != nil {
		resp.Body.Close()
	}
}

// listVMIDs returns ids from GET /vms (from path or id fields).
func listVMIDs() ([]string, error) {
	resp, err := doKubevirtRequest(http.MethodGet, "/vms", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list status %d", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	vms, _ := body["vms"].([]interface{})
	var ids []string
	for _, v := range vms {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := m["id"].(string); id != "" {
			ids = append(ids, id)
			continue
		}
		if p, _ := m["path"].(string); p != "" {
			ids = append(ids, extractIDFromPath(p))
		}
	}
	return ids, nil
}

// deleteVMFromCluster removes VirtualMachine directly via kubectl/oc
func deleteVMFromCluster(vmName, namespace string) error {
	cmd := exec.Command("oc", "delete", "vm", vmName, "-n", namespace, "--ignore-not-found=true")
	if err := cmd.Run(); err != nil {
		// Try kubectl as fallback
		cmd = exec.Command("kubectl", "delete", "vm", vmName, "-n", namespace, "--ignore-not-found=true")
		return cmd.Run()
	}
	return nil
}

// verifyVMDeleted confirms VirtualMachine no longer exists
func verifyVMDeleted(vmName, namespace string) error {
	cmd := exec.Command("oc", "get", "vm", vmName, "-n", namespace)
	if err := cmd.Run(); err != nil {
		return nil // VM doesn't exist (expected)
	}
	return fmt.Errorf("VM %s still exists in namespace %s", vmName, namespace)
}

// checkClusterAccess verifies kubectl/oc connectivity
func checkClusterAccess() error {
	cmd := exec.Command("oc", "cluster-info")
	if err := cmd.Run(); err != nil {
		// Try kubectl as fallback
		cmd = exec.Command("kubectl", "cluster-info")
		return cmd.Run()
	}
	return nil
}

// checkStorageClass verifies at least one StorageClass is available
func checkStorageClass() error {
	cmd := exec.Command("oc", "get", "storageclass", "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		// Try kubectl as fallback
		cmd = exec.Command("kubectl", "get", "storageclass", "-o", "json")
		out, err = cmd.Output()
		if err != nil {
			return fmt.Errorf("failed to get storage classes: %w", err)
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("failed to parse storage class json: %w", err)
	}

	items, ok := result["items"].([]interface{})
	if !ok || len(items) == 0 {
		return fmt.Errorf("no storage classes available in cluster")
	}

	GinkgoWriter.Printf("Found %d storage class(es) available\n", len(items))
	return nil
}

// getDefaultStorageClass returns the name of the default storage class, or empty if none
func getDefaultStorageClass() string {
	cmd := exec.Command("oc", "get", "storageclass", "-o", "json")
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.Command("kubectl", "get", "storageclass", "-o", "json")
		out, err = cmd.Output()
		if err != nil {
			return ""
		}
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		return ""
	}

	items, ok := result["items"].([]interface{})
	if !ok {
		return ""
	}

	// Find default storage class
	for _, item := range items {
		sc, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		metadata, ok := sc["metadata"].(map[string]interface{})
		if !ok {
			continue
		}

		annotations, ok := metadata["annotations"].(map[string]interface{})
		if !ok {
			continue
		}

		// Check for default annotation
		if isDefault, _ := annotations["storageclass.kubernetes.io/is-default-class"].(string); isDefault == "true" {
			name, _ := metadata["name"].(string)
			return name
		}
	}

	return ""
}
