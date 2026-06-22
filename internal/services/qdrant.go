package services

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"nav/config"
)

const (
	// QdrantContainerName is the well-known container name nav uses for the
	// locally-managed Qdrant instance.
	QdrantContainerName = "nav-qdrant"
	// QdrantImage is the published Qdrant image used when starting a fresh
	// container. It is fully qualified with the docker.io registry so that
	// Podman's short-name resolution (which prompts for a registry and fails
	// without a TTY when short-name-mode is "enforcing") is never triggered.
	QdrantImage = "docker.io/qdrant/qdrant:latest"

	// Qdrant inside the container listens on 6333 (REST) and 6334 (gRPC).
	qdrantRESTPortInside = 6333
	qdrantGRPCPortInside = 6334
)

// EnsureLocalQdrant starts the nav-qdrant container when Qdrant is configured
// for localhost and nothing is listening on the gRPC port. It is a no-op for
// remote Qdrant. On success the gRPC endpoint is verified reachable before
// returning. If the container is running but unhealthy it is restarted.
func EnsureLocalQdrant(cfg *config.Config) error {
	if !isLocalHost(cfg.Qdrant.Host) {
		return nil
	}

	if isPortOpen(cfg.Qdrant.Host, cfg.Qdrant.Port) && qdrantHealthOK(cfg.Qdrant.Host) {
		return nil
	}

	rt, err := DetectRuntime()
	if err != nil {
		return fmt.Errorf("ensure local qdrant: %w", err)
	}

	exists, err := rt.ContainerExists(QdrantContainerName)
	if err != nil {
		return err
	}

	if exists {
		running, err := rt.ContainerRunning(QdrantContainerName)
		if err != nil {
			return err
		}
		if running {
			fmt.Printf("nav: container %q is running but unhealthy, restarting via %s…\n", QdrantContainerName, rt.Cmd)
			if err := rt.Restart(QdrantContainerName); err != nil {
				return fmt.Errorf("restarting %s: %w", QdrantContainerName, err)
			}
		} else {
			fmt.Printf("nav: starting existing container %q via %s…\n", QdrantContainerName, rt.Cmd)
			if err := rt.Start(QdrantContainerName); err != nil {
				return fmt.Errorf("starting %s: %w", QdrantContainerName, err)
			}
		}
	} else {
		dbDir := filepath.Join(config.Dir(), "db")
		if err := os.MkdirAll(dbDir, 0o755); err != nil {
			return fmt.Errorf("creating qdrant data dir %s: %w", dbDir, err)
		}
		fmt.Printf("nav: launching %s container %q with data dir %s\n", rt.Cmd, QdrantContainerName, dbDir)
		if err := rt.Run(RunOpts{
			Name:  QdrantContainerName,
			Image: QdrantImage,
			Ports: []string{
				fmt.Sprintf("%d:%d", qdrantRESTPortInside, qdrantRESTPortInside),
				fmt.Sprintf("%d:%d", cfg.Qdrant.Port, qdrantGRPCPortInside),
			},
			Volumes: []string{dbDir + ":/qdrant/storage"},
		}); err != nil {
			return fmt.Errorf("running %s container: %w", QdrantContainerName, err)
		}
	}

	if err := waitForPort(cfg.Qdrant.Host, cfg.Qdrant.Port, 30*time.Second); err != nil {
		return fmt.Errorf("qdrant did not become ready: %w", err)
	}
	if err := waitForHealth(cfg.Qdrant.Host, 30*time.Second); err != nil {
		return fmt.Errorf("qdrant health check failed: %w", err)
	}
	return nil
}

// isLocalHost reports whether host resolves to the loopback interface as far
// as our "should we run a local container?" check is concerned.
func isLocalHost(host string) bool {
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func isPortOpen(host string, port int) bool {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func waitForPort(host string, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if isPortOpen(host, port) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s waiting for %s:%d", timeout, host, port)
}

// qdrantHealthOK reports whether the Qdrant REST health endpoint responds with
// a 2xx status. It uses the well-known REST port (6333) which is always mapped
// 1:1 to the host.
func qdrantHealthOK(host string) bool {
	if host == "" {
		host = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%d/healthz", host, qdrantRESTPortInside)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// waitForHealth polls the Qdrant REST health endpoint until it responds with a
// 2xx status or the timeout is reached.
func waitForHealth(host string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if qdrantHealthOK(host) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out after %s waiting for qdrant health check on %s", timeout, host)
}
