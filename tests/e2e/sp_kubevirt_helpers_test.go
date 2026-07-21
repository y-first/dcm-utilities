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
	"sync"

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

		// Test connectivity
		resp, err := http.Get(kubevirtSPURL + "/health")
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
		Skip("KubeVirt SP not available")
	}
}

// doKubevirtRequest performs HTTP request against the KubeVirt SP
func doKubevirtRequest(method, path, payload string) (*http.Response, error) {
	return doRequestToURL(kubevirtSPURL+path, method, payload)
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
			Size: "1Gi",
		},
		Storage: VMStorage{
			Disks: []VMDisk{
				{
					Name:     "boot",
					Capacity: "10Gi",
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
	if err := decodeJSON(resp, &body); err != nil {
		return "", err
	}

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

// verifyDCMLabels checks VirtualMachine has required DCM labels
func verifyDCMLabels(vmName, namespace, expectedInstanceID string) error {
	vm, err := getVMFromCluster(vmName, namespace)
	if err != nil {
		return err
	}

	metadata, ok := vm["metadata"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("vm missing metadata")
	}

	labels, ok := metadata["labels"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("vm missing labels")
	}

	managedBy, _ := labels["dcm.project/managed-by"].(string)
	if managedBy != "dcm" {
		return fmt.Errorf("missing or incorrect dcm.project/managed-by label: got %q, want \"dcm\"", managedBy)
	}

	instanceID, _ := labels["dcm.project/instance-id"].(string)
	if instanceID != expectedInstanceID {
		return fmt.Errorf("instance-id mismatch: got %q, want %q", instanceID, expectedInstanceID)
	}

	return nil
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
