package services

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Runtime is a thin wrapper around either the docker or podman CLI. Pick one
// with DetectRuntime — it inspects /etc/os-release and chooses podman on
// Red Hat–family systems, docker everywhere else.
type Runtime struct {
	// Cmd is the executable name (e.g. "docker" or "podman").
	Cmd string
	// Podman indicates whether Cmd is podman; some flags differ subtly
	// (notably SELinux relabel suffixes on bind mounts).
	Podman bool
}

// DetectRuntime returns the appropriate container runtime for the host. It
// errors when the chosen CLI is not in $PATH.
func DetectRuntime() (*Runtime, error) {
	cmd := "docker"
	podman := false
	if isRedHatFamily() {
		cmd = "podman"
		podman = true
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return nil, fmt.Errorf("%s not found in $PATH: %w", cmd, err)
	}
	return &Runtime{Cmd: cmd, Podman: podman}, nil
}

// isRedHatFamily reports whether /etc/os-release identifies the host as a
// Red Hat–family distribution (Fedora, RHEL, CentOS, Rocky, AlmaLinux, etc.).
func isRedHatFamily() bool {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return false
	}
	rhTokens := map[string]bool{
		"rhel":       true,
		"fedora":     true,
		"centos":     true,
		"rocky":      true,
		"almalinux":  true,
		"ol":         true, // Oracle Linux
		"amzn":       true, // Amazon Linux
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		var value string
		switch {
		case strings.HasPrefix(line, "ID="):
			value = strings.TrimPrefix(line, "ID=")
		case strings.HasPrefix(line, "ID_LIKE="):
			value = strings.TrimPrefix(line, "ID_LIKE=")
		default:
			continue
		}
		value = strings.Trim(value, `"'`)
		for _, tok := range strings.Fields(value) {
			if rhTokens[strings.ToLower(tok)] {
				return true
			}
		}
	}
	return false
}

// RunOpts describes a detached container to create.
type RunOpts struct {
	Name    string
	Image   string
	Ports   []string // each "<host>:<container>"
	Volumes []string // each "<host>:<container>" — Podman gets ":Z" appended automatically
	Env     map[string]string
}

// ContainerExists reports whether a container (running or stopped) with the
// given name is present.
func (r *Runtime) ContainerExists(name string) (bool, error) {
	out, err := exec.Command(r.Cmd, "ps", "-a", "--filter", "name=^"+name+"$", "--format", "{{.Names}}").Output()
	if err != nil {
		return false, fmt.Errorf("%s ps -a: %w", r.Cmd, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// ContainerRunning reports whether a container with the given name is running.
func (r *Runtime) ContainerRunning(name string) (bool, error) {
	out, err := exec.Command(r.Cmd, "ps", "--filter", "name=^"+name+"$", "--format", "{{.Names}}").Output()
	if err != nil {
		return false, fmt.Errorf("%s ps: %w", r.Cmd, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == name {
			return true, nil
		}
	}
	return false, nil
}

// Start launches an existing (stopped) container.
func (r *Runtime) Start(name string) error {
	cmd := exec.Command(r.Cmd, "start", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Run creates and starts a new detached container according to opts.
func (r *Runtime) Run(opts RunOpts) error {
	args := []string{"run", "-d", "--name", opts.Name}
	for _, p := range opts.Ports {
		args = append(args, "-p", p)
	}
	for _, v := range opts.Volumes {
		if r.Podman {
			v = v + ":Z"
		}
		args = append(args, "-v", v)
	}
	for k, v := range opts.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, opts.Image)

	cmd := exec.Command(r.Cmd, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
