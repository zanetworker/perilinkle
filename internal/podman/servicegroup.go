package podman

import (
	"fmt"
	"os"

	"github.com/gsim/perilinkle/api/v1alpha1"
	"sigs.k8s.io/yaml"
)

func LoadServiceGroups(paths []string) ([]v1alpha1.ServiceGroup, error) {
	groups := make([]v1alpha1.ServiceGroup, 0, len(paths))
	for _, path := range paths {
		group, err := LoadServiceGroup(path)
		if err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, nil
}

func LoadServiceGroup(path string) (v1alpha1.ServiceGroup, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return v1alpha1.ServiceGroup{}, fmt.Errorf("read service group %q: %w", path, err)
	}
	var group v1alpha1.ServiceGroup
	if err := yaml.Unmarshal(data, &group); err != nil {
		return v1alpha1.ServiceGroup{}, fmt.Errorf("parse service group %q: %w", path, err)
	}
	if group.Name == "" {
		return v1alpha1.ServiceGroup{}, fmt.Errorf("service group %q metadata.name is required", path)
	}
	return group, nil
}
