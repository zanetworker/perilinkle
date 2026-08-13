package podman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

type PodmanRuntime struct {
	Binary string
}

func (r PodmanRuntime) ListContainers(ctx context.Context) ([]Container, error) {
	binary := r.Binary
	if binary == "" {
		binary = "podman"
	}
	out, err := exec.CommandContext(ctx, binary, "ps", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("podman ps: %w", err)
	}
	containers, err := ParsePodmanPS(out)
	if err != nil {
		return nil, err
	}
	return containers, nil
}

func ParsePodmanPS(data []byte) ([]Container, error) {
	var raw []podmanPSContainer
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse podman ps json: %w", err)
	}
	containers := make([]Container, 0, len(raw))
	for _, item := range raw {
		name := item.Name
		if name == "" && len(item.Names.Values) > 0 {
			name = item.Names.Values[0]
		}
		id := firstNonEmpty(item.ID, item.Id)
		containers = append(containers, Container{
			ID:     id,
			Name:   strings.TrimPrefix(name, "/"),
			Labels: item.Labels,
		})
	}
	return containers, nil
}

type podmanPSContainer struct {
	ID     string            `json:"ID"`
	Id     string            `json:"Id"`
	Name   string            `json:"Name"`
	Names  podmanNames       `json:"Names"`
	Labels map[string]string `json:"Labels"`
}

type podmanNames struct {
	Values []string
}

func (n *podmanNames) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		n.Values = list
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		if single != "" {
			n.Values = []string{single}
		}
		return nil
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
