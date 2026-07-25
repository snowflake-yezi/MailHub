package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TLSDialer(address, caFile, agentVersion string) (DialFunc, error) {
	address = strings.TrimSpace(address)
	if strings.Contains(address, "://") {
		return nil, fmt.Errorf("management.control_url must be host:port without a URL scheme")
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if strings.TrimSpace(caFile) != "" {
		payload, readErr := os.ReadFile(strings.TrimSpace(caFile))
		if readErr != nil {
			return nil, fmt.Errorf("read management CA file: %w", readErr)
		}
		if !roots.AppendCertsFromPEM(payload) {
			return nil, fmt.Errorf("management CA file contains no certificates")
		}
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("management.control_url must be host:port: %w", err)
	}
	tlsConfig := &tls.Config{RootCAs: roots, ServerName: strings.Trim(host, "[]"), MinVersion: tls.VersionTLS12}
	return func(context.Context) (*grpc.ClientConn, error) {
		return grpc.NewClient(address,
			grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig.Clone())),
			grpc.WithUserAgent("mailhub-node/"+strings.TrimSpace(agentVersion)),
		)
	}, nil
}
