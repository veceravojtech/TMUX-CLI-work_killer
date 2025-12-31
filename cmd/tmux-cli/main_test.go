package main

import (
	"testing"
)

func TestMain_HasVersionConstant(t *testing.T) {
	// Verify version constant is defined
	if version == "" {
		t.Error("version constant should not be empty")
	}
}

func TestMain_HasAppNameConstant(t *testing.T) {
	// Verify appName constant is defined
	if appName == "" {
		t.Error("appName constant should not be empty")
	}

	if appName != "tmux-cli" {
		t.Errorf("appName = %q, want %q", appName, "tmux-cli")
	}
}
