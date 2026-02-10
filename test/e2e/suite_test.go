//go:build !e2e

// Package e2e contains end-to-end tests for the Wormsign controller
// using envtest to run a local API server.
package e2e

import (
	"log/slog"
	"os"
	"testing"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	wormsignv1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
)

var (
	testEnv   *envtest.Environment
	restCfg   *rest.Config
	envtestOK bool
)

func TestMain(m *testing.M) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{"../../deploy/helm/wormsign/crds"},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		logger.Warn("envtest binaries not available, envtest-based tests will be skipped",
			"error", err,
		)
		// Do NOT os.Exit here — integration tests that don't need envtest
		// should still run. Tests requiring envtest call skipIfNoEnvtest().
	} else {
		restCfg = cfg
		envtestOK = true

		if err := wormsignv1alpha1.AddToScheme(scheme.Scheme); err != nil {
			logger.Error("failed to add wormsign scheme", "error", err)
			os.Exit(1)
		}
	}

	code := m.Run()

	if envtestOK {
		if err := testEnv.Stop(); err != nil {
			logger.Error("failed to stop envtest", "error", err)
		}
	}

	os.Exit(code)
}

// skipIfNoEnvtest skips the calling test if envtest binaries are not available.
func skipIfNoEnvtest(t *testing.T) {
	t.Helper()
	if !envtestOK {
		t.Skip("envtest binaries not available")
	}
}
