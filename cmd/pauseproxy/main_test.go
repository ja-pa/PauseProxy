package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpExplainsCABootstrapWithoutLoadingConfig(t *testing.T) {
	for _, args := range [][]string{
		{"-h"},
		{"--help"},
		{"bootstrap-ca", "-h"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			output, err := os.CreateTemp(t.TempDir(), "help")
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			oldArgs, oldStderr := os.Args, os.Stderr
			t.Cleanup(func() { os.Args, os.Stderr = oldArgs, oldStderr })
			missingConfig := filepath.Join(t.TempDir(), "missing.json")
			os.Args = append([]string{"pauseproxy"}, args...)
			os.Args = append(os.Args, "--config", missingConfig)
			os.Stderr = output
			if err := run(); err != nil {
				t.Fatalf("help should succeed without loading config: %v", err)
			}
			data, err := os.ReadFile(output.Name())
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"pauseproxy bootstrap-ca --config /etc/pauseproxy/config.json",
				"Do not repeat on upgrades", "ca-cert.pem", "ca-key.pem",
				"-config", "-ca-dir",
			} {
				if !strings.Contains(string(data), want) {
					t.Errorf("help missing %q: %s", want, data)
				}
			}
		})
	}
}
