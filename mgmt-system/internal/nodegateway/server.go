package nodegateway

import (
	"crypto/tls"
	"fmt"
	"net"

	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func NewTLSServer(listenAddress, certificateFile, keyFile string, gateway *Gateway) (*grpc.Server, net.Listener, error) {
	if gateway == nil {
		return nil, nil, fmt.Errorf("node gateway is required")
	}
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load node control TLS key pair: %w", err)
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for node control: %w", err)
	}
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{certificate},
			MinVersion:   tls.VersionTLS12,
		})),
		grpc.MaxRecvMsgSize(1<<20),
		grpc.MaxSendMsgSize(1<<20),
	)
	nodev1.RegisterNodeGatewayServer(server, gateway)
	return server, listener, nil
}
