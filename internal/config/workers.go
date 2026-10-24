package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const MaxWorkers = 32

// WorkerURL validates a worker origin using the secure default transport policy.
func WorkerURL(raw string) error {
	return ValidateWorkerTransport([]string{raw}, false)
}

// ValidateWorkerTransport applies startup permission separately from saved configuration.
func ValidateWorkerTransport(workers []string, allowHTTP bool) error {
	for i, raw := range workers {
		u, err := workerOrigin(raw)
		if err != nil {
			return fmt.Errorf("workers[%d]: %w", i, err)
		}
		if remoteHTTP(u) && !allowHTTP {
			return fmt.Errorf("workers[%d]: remote worker control requires HTTPS; HTTP requires explicit -allow-http-workers (GOMETER_ALLOW_HTTP_WORKERS=true) at coordinator startup", i)
		}
	}
	return nil
}

func HasRemoteHTTPWorkers(workers []string) bool {
	for _, raw := range workers {
		u, err := workerOrigin(raw)
		if err == nil && remoteHTTP(u) {
			return true
		}
	}
	return false
}

func remoteHTTP(u *url.URL) bool {
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (ip == nil || !ip.IsLoopback())
}

// Saved configurations describe origins; only the coordinator can grant transport permission.
func workerOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("worker URL must be an HTTP(S) origin without credentials, query, or path")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("worker URL port must be between 1 and 65535")
		}
	}
	return u, nil
}

// NormalizeWorkerOrigin identifies equivalent origins without granting transport permission.
func NormalizeWorkerOrigin(raw string) (string, error) {
	u, err := workerOrigin(raw)
	if err != nil {
		return "", err
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", fmt.Errorf("worker URL must contain a hostname or IP")
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	port := u.Port()
	if port != "" {
		n, _ := strconv.Atoi(port)
		port = strconv.Itoa(n)
	}
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return (&url.URL{Scheme: u.Scheme, Host: host}).String(), nil
}

func (c Config) validateWorkers() error {
	if len(c.Workers) == 0 {
		return nil
	}
	if len(c.Workers) > MaxWorkers || c.Mode != "rate" || len(c.Workers) > c.MaxInFlight {
		return fmt.Errorf("distributed runs require rate mode and 1–32 workers, with at least one concurrency slot per worker")
	}
	seen := map[string]bool{}
	for i, raw := range c.Workers {
		key, err := NormalizeWorkerOrigin(raw)
		if err != nil {
			return fmt.Errorf("workers[%d]: %w", i, err)
		}
		if seen[key] {
			return fmt.Errorf("worker origins must be unique")
		}
		seen[key] = true
	}
	return nil
}
