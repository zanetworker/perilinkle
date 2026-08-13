package podman

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type SpireServerCLI struct {
	Binary       string
	Socket       string
	Container    string
	PodmanBinary string
}

func (s SpireServerCLI) EnsureEntry(ctx context.Context, entry SPIREEntry) error {
	if entry.SPIFFEID == "" {
		return fmt.Errorf("spire entry spiffe id is empty")
	}
	if entry.ParentID == "" {
		return fmt.Errorf("spire entry parent id is empty")
	}
	if len(entry.Selectors) == 0 {
		return fmt.Errorf("spire entry %q has no selectors", entry.SPIFFEID)
	}

	exists, err := s.entryExists(ctx, entry)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	args := s.withSocket("entry", "create")
	args = append(args, "-parentID", entry.ParentID, "-spiffeID", entry.SPIFFEID)
	for _, selector := range entry.Selectors {
		args = append(args, "-selector", selector)
	}
	if out, err := s.run(ctx, args...); err != nil {
		return fmt.Errorf("spire-server entry create %q: %w: %s", entry.SPIFFEID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (s SpireServerCLI) entryExists(ctx context.Context, entry SPIREEntry) (bool, error) {
	args := s.withSocket("entry", "show")
	args = append(args, "-spiffeID", entry.SPIFFEID, "-parentID", entry.ParentID)
	out, err := s.run(ctx, args...)
	if err != nil {
		if spireEntryMissing(out) {
			return false, nil
		}
		return false, fmt.Errorf("spire-server entry show %q: %w: %s", entry.SPIFFEID, err, strings.TrimSpace(string(out)))
	}
	if !bytes.Contains(out, []byte(entry.SPIFFEID)) || !bytes.Contains(out, []byte(entry.ParentID)) {
		return false, nil
	}
	for _, selector := range entry.Selectors {
		if !bytes.Contains(out, []byte(selector)) {
			return false, nil
		}
	}
	return true, nil
}

func spireEntryMissing(out []byte) bool {
	text := strings.ToLower(string(out))
	return strings.Contains(text, "no entries") ||
		strings.Contains(text, "not found") ||
		strings.Contains(text, "not exist")
}

func (s SpireServerCLI) withSocket(args ...string) []string {
	if s.Socket == "" {
		return args
	}
	return append(args, "-socketPath", s.Socket)
}

func (s SpireServerCLI) binary() string {
	if s.Binary == "" {
		if s.Container != "" {
			return "/opt/spire/bin/spire-server"
		}
		return "spire-server"
	}
	return s.Binary
}

func (s SpireServerCLI) run(ctx context.Context, args ...string) ([]byte, error) {
	if s.Container == "" {
		return exec.CommandContext(ctx, s.binary(), args...).CombinedOutput()
	}
	command := []string{"exec", s.Container, s.binary()}
	command = append(command, args...)
	podman := s.PodmanBinary
	if podman == "" {
		podman = "podman"
	}
	return exec.CommandContext(ctx, podman, command...).CombinedOutput()
}
