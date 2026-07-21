//go:build e2e

package e2e_test

import (
	"fmt"
	"net/http"
	"os"
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
	client := &http.Client{}
	var body *http.Request
	var err error

	if payload != "" {
		body, err = http.NewRequest(method, url, nil)
		if err != nil {
			return nil, err
		}
		body.Header.Set("Content-Type", "application/json")
	} else {
		body, err = http.NewRequest(method, url, nil)
		if err != nil {
			return nil, err
		}
	}

	return client.Do(body)
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
