package distributed

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"gometer/internal/config"
)

// WorkerCheck is a dated observation, never a reservation or a readiness promise.
// It contains no response bodies, credentials, or unsanitized transport errors.
type WorkerCheck struct {
	State     string    `json:"state"`
	CheckedAt time.Time `json:"checked_at"`
}

// CheckWorker performs one authenticated GET /v1/info. It sends no workload or
// reservation requests and uses the same verified transport policy as a run.
func CheckWorker(ctx context.Context, origin string, opts Options) (check WorkerCheck) {
	defer func() { check.CheckedAt = time.Now().UTC() }()
	check.State = "transport_failed"
	if config.ValidateWorkerTransport([]string{origin}, opts.AllowHTTPWorkers) != nil {
		return
	}
	normalized, err := config.NormalizeWorkerOrigin(origin)
	if err != nil {
		return
	}
	if ValidToken(opts.Token) != nil {
		check.State = "authentication_failed"
		return
	}
	client, err := controlClient(opts)
	if err != nil {
		return
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized+"/v1/info", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+opts.Token)
	response, err := client.Do(req)
	if err != nil {
		var cert *tls.CertificateVerificationError
		var unknown x509.UnknownAuthorityError
		var hostname x509.HostnameError
		var invalid x509.CertificateInvalidError
		var network *net.OpError
		var dns *net.DNSError
		var timeout net.Error
		if errors.As(err, &cert) || errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) {
			return
		}
		if errors.As(err, &dns) || (errors.As(err, &network) && network.Op == "dial") || (errors.As(err, &timeout) && timeout.Timeout()) {
			check.State = "unreachable"
		}
		return
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		check.State = "authentication_failed"
		return
	}
	check.State = "incompatible"
	if response.StatusCode != http.StatusOK {
		return
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		check.State = "transport_failed"
		return
	}
	if len(data) > 64<<10 {
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var info struct {
		Protocol int       `json:"protocol"`
		Name     string    `json:"name"`
		Now      time.Time `json:"now"`
		Busy     *bool     `json:"busy"`
	}
	if decoder.Decode(&info) != nil {
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || info.Protocol != Protocol || info.Now.IsZero() || info.Busy == nil || strings.TrimSpace(info.Name) == "" || len(info.Name) > 256 || strings.ContainsFunc(info.Name, unicode.IsControl) {
		return
	}
	check.State = "reachable"
	if *info.Busy {
		check.State = "busy"
	}
	return
}
