package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

// defaultKubeProxyPort is the local port 'kube proxy' listens on unless told
// otherwise, and the one to pass to 'kube config --proxy-port'.
const defaultKubeProxyPort = 18443

type kubeProxyFlagValues struct {
	port       int
	kubeconfig string
}

var kubeProxyFlags kubeProxyFlagValues

var kubeProxyCmd = &cobra.Command{
	Use:   "proxy CLUSTER",
	Short: "Run a local proxy for a cluster",
	Long: `Run a local HTTP CONNECT proxy on 127.0.0.1 that reaches the proxy host of
one cluster's context and nowhere else. It never reads the traffic: kubectl's
TLS runs end to end, and the proxy follows the same HTTPS_PROXY and NO_PROXY
settings as alpacon.

The proxy is optional. kubectl uses it only when the context was written with
'alpacon kube config CLUSTER --proxy-port PORT' for the same port. Run
'alpacon kube config CLUSTER' first; this command reads the context it wrote.

Only one proxy per cluster and port runs at a time. Press Ctrl+C to stop it.`,
	Example: `  alpacon kube config prod-east --proxy-port 18443
  alpacon kube proxy prod-east
  alpacon kube proxy prod-east --port 18444`,
	Args: cobra.ExactArgs(1),
	Run:  runKubeProxy,
}

func init() {
	kubeProxyCmd.Flags().IntVar(&kubeProxyFlags.port, "port", defaultKubeProxyPort, "local port to listen on")
	kubeProxyCmd.Flags().StringVar(&kubeProxyFlags.kubeconfig, "kubeconfig", "", "kubeconfig file to read the context from (default: the files kubectl reads)")
}

func runKubeProxy(cmd *cobra.Command, args []string) {
	clusterName := args[0]
	port := kubeProxyFlags.port
	if err := validatePort(port); err != nil {
		utils.CliUsageErrorWithExit("--port %s.", err)
	}

	paths, err := kubeconfig.SearchPaths(kubeProxyFlags.kubeconfig)
	if err != nil {
		utils.CliErrorWithExit("Failed to find the kubeconfig: %s.", err)
	}
	contextName := contextPrefix + clusterName
	server, proxyURL, err := kubeconfig.Lookup(paths, contextName)
	if err != nil {
		if errors.Is(err, kubeconfig.ErrContextNotFound) || errors.Is(err, kubeconfig.ErrClusterNotFound) {
			utils.CliErrorWithExit("No usable context %q in %s. Run 'alpacon kube config %s' first.", contextName, strings.Join(paths, ", "), clusterName)
		}
		utils.CliErrorWithExit("Failed to read the kubeconfig: %s.", err)
	}
	target, err := proxyTarget(server)
	if err != nil {
		utils.CliErrorWithExit("Cannot proxy context %q: %s.", contextName, err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		utils.CliErrorWithExit("Failed to find the home directory: %s.", err)
	}
	// The kernel drops the lock however this process ends, so the exits below
	// need not release it.
	lock, err := filelock.Acquire(kubeProxyLockPath(filepath.Join(home, config.ConfigFileDir), clusterName, port), 0644)
	if err != nil {
		if errors.Is(err, filelock.ErrLocked) {
			utils.CliErrorWithExit("A kube proxy for %s on port %d is already running.", clusterName, port)
		}
		utils.CliErrorWithExit("Failed to take the kube proxy lock: %s.", err)
	}
	defer func() { _ = lock.Release() }()

	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		utils.CliErrorWithExit("Failed to listen on 127.0.0.1:%d: %s.", port, err)
	}

	relay := newConnectRelay(target)
	served := make(chan error, 1)
	go func() { served <- relay.Serve(listener) }()

	if want := localProxyURL(port); proxyURL != want {
		utils.CliWarning("Context %q does not send kubectl through this proxy. Run 'alpacon kube config %s --proxy-port %d' to point it here.", contextName, clusterName, port)
	}
	utils.CliInfo("Kube proxy for %s listening on 127.0.0.1:%d -> %s (Ctrl+C to exit)", clusterName, port, target)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case <-signals:
		_ = relay.Close()
		<-served
		utils.CliInfo("Kube proxy stopped")
	case err := <-served:
		_ = relay.Close()
		if err != nil {
			utils.CliErrorWithExit("Kube proxy stopped: %s.", err)
		}
	}
}

// proxyTarget returns the host:port a context's server is reached at. Only an
// https server is: kubectl tunnels to it with CONNECT, while it would send a
// plain http request to the proxy itself, which this relay does not forward.
func proxyTarget(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("its server %q is not a URL with a host", server)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("its server %q is not https, and only an https server can be reached through the proxy", server)
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

// kubeProxyLockPath names the lock one proxy for a cluster on a port holds.
// The cluster name is hashed so any name makes a safe file name; a different
// port is a different lock, since two listeners cannot share a port anyway.
func kubeProxyLockPath(dir, clusterName string, port int) string {
	sum := sha256.Sum256([]byte(clusterName))
	return filepath.Join(dir, fmt.Sprintf(".kube_proxy_lock.%s.%d", hex.EncodeToString(sum[:])[:16], port))
}
