package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

const (
	agentSandboxGroup = "agents.x-k8s.io"
	agentSandboxKind  = "Sandbox"
)

// supportedSandboxVersions lists Agent Sandbox API versions in preference order.
// Red Hat build of Agent Sandbox 0.9 serves only v1beta1.
var supportedSandboxVersions = []string{"v1beta1", "v1alpha1"}

// ResolveSandboxGVK discovers which Agent Sandbox Sandbox version the cluster serves.
func ResolveSandboxGVK(dc discovery.DiscoveryInterface, override string) (schema.GroupVersionKind, error) {
	served, err := servedSandboxVersions(dc)
	if err != nil {
		return schema.GroupVersionKind{}, err
	}
	return resolveSandboxGVK(served, override)
}

func resolveSandboxGVK(served []string, override string) (schema.GroupVersionKind, error) {
	gvk := schema.GroupVersionKind{Group: agentSandboxGroup, Kind: agentSandboxKind}
	if override != "" {
		if !contains(served, override) {
			return schema.GroupVersionKind{}, fmt.Errorf("sandbox API version %q is not served by the cluster (served: %v)", override, served)
		}
		gvk.Version = override
		return gvk, nil
	}
	for _, version := range supportedSandboxVersions {
		if contains(served, version) {
			gvk.Version = version
			return gvk, nil
		}
	}
	return schema.GroupVersionKind{}, fmt.Errorf("no supported %s %s version served (want one of %v, served: %v)", agentSandboxGroup, agentSandboxKind, supportedSandboxVersions, served)
}

func servedSandboxVersions(dc discovery.DiscoveryInterface) ([]string, error) {
	_, lists, err := dc.ServerGroupsAndResources()
	if err != nil && len(lists) == 0 {
		return nil, fmt.Errorf("discover %s resources: %w", agentSandboxGroup, err)
	}
	var versions []string
	for _, list := range lists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil || gv.Group != agentSandboxGroup {
			continue
		}
		for _, resource := range list.APIResources {
			if resource.Kind == agentSandboxKind {
				versions = append(versions, gv.Version)
				break
			}
		}
	}
	return versions, nil
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
