//go:build e2e

package e2e

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var (
	kindBin    string
	kubectlBin string
	helmBin    string
	dockerBin  string

	clusterName = "wormsign-e2e"
	imageName   = "wormsign-controller"
	imageTag    = "e2e"
	projectRoot string
)

func TestMain(m *testing.M) {
	resolveToolPaths()
	resolveProjectRoot()

	if err := validateTools(); err != nil {
		log.Fatalf("e2e: tool validation failed: %v", err)
	}

	reuseCluster := os.Getenv("E2E_REUSE_CLUSTER") == "1"
	skipTeardown := os.Getenv("E2E_SKIP_TEARDOWN") == "1"

	if !reuseCluster {
		if err := setupCluster(); err != nil {
			dumpClusterDiagnostics()
			// Clean up retained nodes so the container can exit cleanly.
			_ = runCmdStreamed(kindBin, "delete", "cluster", "--name", clusterName)
			log.Fatalf("e2e: cluster setup failed: %v", err)
		}
	} else {
		log.Println("e2e: reusing existing cluster")
		if err := exportKubeconfig(); err != nil {
			log.Fatalf("e2e: export kubeconfig failed: %v", err)
		}
	}

	code := m.Run()

	if !skipTeardown && !reuseCluster {
		if err := teardownCluster(); err != nil {
			log.Printf("e2e: cluster teardown failed: %v", err)
		}
	} else {
		log.Println("e2e: skipping cluster teardown")
	}

	os.Exit(code)
}

func resolveToolPaths() {
	// Default to PATH-based lookup. Override via env vars for custom setups:
	//   KIND=~/go/bin/kind KUBECTL=/tmp/kubectl make test-e2e-local
	kindBin = envOrDefault("KIND", "kind")
	kubectlBin = envOrDefault("KUBECTL", "kubectl")
	helmBin = envOrDefault("HELM", "helm")
	dockerBin = envOrDefault("DOCKER", "docker")
}

func resolveProjectRoot() {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("e2e: cannot determine project root from runtime.Caller")
	}
	// test/e2e/e2e_suite_test.go → project root is 2 dirs up
	projectRoot = filepath.Dir(filepath.Dir(filepath.Dir(filename)))
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func validateTools() error {
	for name, path := range map[string]string{
		"kind":    kindBin,
		"kubectl": kubectlBin,
		"helm":    helmBin,
		"docker":  dockerBin,
	} {
		// Check if it's on PATH or exists as a file.
		if _, err := exec.LookPath(path); err != nil {
			return fmt.Errorf("%s not found at %q: %w", name, path, err)
		}
	}
	return nil
}

func setupCluster() error {
	log.Println("e2e: setting up Kind cluster and building image...")

	// Check if cluster already exists.
	if clusterExists() {
		log.Printf("e2e: cluster %q already exists, reusing", clusterName)
	} else {
		log.Printf("e2e: creating Kind cluster %q...", clusterName)
		// Use kind-config-dind.yaml (single node) in DinD to avoid OOM; full
		// 3-node config for native runs.
		cfgFile := envOrDefault("KIND_CONFIG", "kind-config.yaml")
		kindCfg := filepath.Join(projectRoot, "hack", cfgFile)
		if err := runCmdStreamed(kindBin, "create", "cluster",
			"--config", kindCfg,
			"--name", clusterName,
			"--wait", "5m",
			"--retain",
		); err != nil {
			return fmt.Errorf("kind create cluster: %w", err)
		}
		log.Println("e2e: cluster created")
	}

	// Export kubeconfig.
	if err := exportKubeconfig(); err != nil {
		return err
	}

	// Build Docker image.
	log.Printf("e2e: building image %s:%s...", imageName, imageTag)
	if err := runCmdStreamed(dockerBin, "build", "-t", imageName+":"+imageTag, projectRoot); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	log.Println("e2e: image built")

	// Load image into Kind nodes.
	if err := loadImage(); err != nil {
		return err
	}

	// Install CRDs.
	log.Println("e2e: installing CRDs...")
	crdDir := filepath.Join(projectRoot, "deploy", "helm", "wormsign", "crds")
	if out, err := runCmdCombined(kubectlBin, "apply", "-f", crdDir); err != nil {
		return fmt.Errorf("kubectl apply CRDs: %s\n%s", err, out)
	}
	log.Println("e2e: CRDs installed")

	return nil
}

func clusterExists() bool {
	out, err := runCmdCombined(kindBin, "get", "clusters")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == clusterName {
			return true
		}
	}
	return false
}

func exportKubeconfig() error {
	out, err := runCmdCombined(kindBin, "export", "kubeconfig", "--name", clusterName)
	if err != nil {
		return fmt.Errorf("kind export kubeconfig: %s\n%s", err, out)
	}
	return nil
}

func loadImage() error {
	log.Println("e2e: loading image into Kind nodes...")
	image := imageName + ":" + imageTag

	// Try `kind load` first.
	if err := runCmdStreamed(kindBin, "load", "docker-image", image, "--name", clusterName); err == nil {
		log.Println("e2e: image loaded via kind")
		return nil
	}

	// Fallback: pipe through containerd on each node (snap Docker workaround).
	log.Println("e2e: kind load failed, using containerd fallback...")
	nodesOut, err := runCmdCombined(kindBin, "get", "nodes", "--name", clusterName)
	if err != nil {
		return fmt.Errorf("kind get nodes: %s\n%s", err, nodesOut)
	}

	for _, node := range strings.Split(strings.TrimSpace(nodesOut), "\n") {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		log.Printf("e2e: loading image into node %s...", node)

		saveCmd := exec.Command(dockerBin, "save", image)
		importCmd := exec.Command(dockerBin, "exec", "-i", node,
			"ctr", "--namespace=k8s.io", "images", "import", "--all-platforms", "-")

		pipe, err := saveCmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("pipe setup: %w", err)
		}
		importCmd.Stdin = pipe
		importCmd.Stdout = os.Stdout
		importCmd.Stderr = os.Stderr

		if err := saveCmd.Start(); err != nil {
			return fmt.Errorf("docker save start: %w", err)
		}
		importErr := importCmd.Run()
		if saveErr := saveCmd.Wait(); saveErr != nil {
			return fmt.Errorf("docker save: %w", saveErr)
		}
		// containerd import returns non-zero when content already exists (dedup),
		// which is benign. Only log a warning.
		if importErr != nil {
			log.Printf("e2e: ctr import on %s returned error (may be benign dedup): %v", node, importErr)
		}
	}
	log.Println("e2e: image loaded into all nodes")
	return nil
}

// dumpClusterDiagnostics inspects retained Kind nodes after a failed cluster
// setup. Prints kubelet logs, memory state, dmesg OOM kills, and cgroup info
// from each node so we can diagnose why kubelet didn't start.
func dumpClusterDiagnostics() {
	log.Println("e2e: === Cluster Failure Diagnostics ===")

	// Host-level memory at time of failure.
	log.Println("e2e: --- Host memory ---")
	if out, err := runCmdCombined("cat", "/proc/meminfo"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "MemTotal") ||
				strings.HasPrefix(line, "MemFree") ||
				strings.HasPrefix(line, "MemAvailable") ||
				strings.HasPrefix(line, "SwapTotal") ||
				strings.HasPrefix(line, "SwapFree") {
				log.Printf("e2e:   %s", line)
			}
		}
	}

	// Get nodes that were retained.
	nodesOut, err := runCmdCombined(kindBin, "get", "nodes", "--name", clusterName)
	if err != nil {
		log.Printf("e2e: no retained nodes found: %v", err)
		log.Println("e2e: === End Diagnostics ===")
		return
	}

	nodes := strings.Split(strings.TrimSpace(nodesOut), "\n")
	log.Printf("e2e: retained nodes: %v", nodes)

	for _, node := range nodes {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		log.Printf("e2e: --- Node: %s ---", node)

		// Check if the container is actually running.
		if out, err := runCmdCombined(dockerBin, "inspect", "-f", "{{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}}", node); err == nil {
			log.Printf("e2e:   container state: %s", strings.TrimSpace(out))
		}

		// Kubelet journal/logs (Kind nodes use journalctl).
		log.Printf("e2e:   kubelet logs (last 30 lines):")
		if out, err := runCmdCombined(dockerBin, "exec", node, "journalctl", "-u", "kubelet", "--no-pager", "-n", "30"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if line != "" {
					log.Printf("e2e:     %s", line)
				}
			}
		} else {
			// Fallback: try reading kubelet log file directly.
			if out2, err2 := runCmdCombined(dockerBin, "exec", node, "sh", "-c", "tail -30 /var/log/kubelet.log 2>/dev/null || echo 'no kubelet log file'"); err2 == nil {
				log.Printf("e2e:     (fallback) %s", out2)
			} else {
				log.Printf("e2e:     journalctl failed: %v, fallback failed: %v", err, err2)
			}
		}

		// Check dmesg for OOM kills.
		log.Printf("e2e:   dmesg OOM/killed entries:")
		if out, err := runCmdCombined(dockerBin, "exec", node, "sh", "-c", "dmesg 2>/dev/null | grep -iE 'oom|killed|out of memory' | tail -10 || echo 'none'"); err == nil {
			log.Printf("e2e:     %s", strings.TrimSpace(out))
		}

		// Memory inside the node.
		log.Printf("e2e:   node memory:")
		if out, err := runCmdCombined(dockerBin, "exec", node, "sh", "-c", "cat /proc/meminfo | head -5"); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				log.Printf("e2e:     %s", line)
			}
		}

		// Cgroup info.
		log.Printf("e2e:   cgroup mounts:")
		if out, err := runCmdCombined(dockerBin, "exec", node, "sh", "-c", "mount | grep cgroup | head -5"); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				log.Printf("e2e:     %s", line)
			}
		}

		// Containerd daemon logs — shows why CRI plugin failed to register.
		log.Printf("e2e:   containerd logs (last 40 lines):")
		if out, err := runCmdCombined(dockerBin, "exec", node, "journalctl", "-u", "containerd", "--no-pager", "-n", "40"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if line != "" {
					log.Printf("e2e:     %s", line)
				}
			}
		}

		// Containerd version.
		if out, err := runCmdCombined(dockerBin, "exec", node, "containerd", "--version"); err == nil {
			log.Printf("e2e:   containerd version: %s", strings.TrimSpace(out))
		}

		// Check if containerd is running inside the node.
		log.Printf("e2e:   containerd CRI check:")
		if out, err := runCmdCombined(dockerBin, "exec", node, "sh", "-c", "crictl --runtime-endpoint unix:///run/containerd/containerd.sock ps -a 2>&1 | head -20"); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				log.Printf("e2e:     %s", line)
			}
		}
	}

	log.Println("e2e: === End Diagnostics ===")
}

func teardownCluster() error {
	log.Printf("e2e: deleting Kind cluster %q...", clusterName)
	if err := runCmdStreamed(kindBin, "delete", "cluster", "--name", clusterName); err != nil {
		return fmt.Errorf("kind delete cluster: %w", err)
	}
	log.Println("e2e: cluster deleted")
	return nil
}

// runCmdCombined runs a command and returns combined stdout+stderr.
func runCmdCombined(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runCmdStreamed runs a command with stdout/stderr piped to os.Stdout/os.Stderr
// so the user can see progress from long-running commands.
func runCmdStreamed(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
