package nodegateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ticket/email-mgmt-system/internal/model"
	"github.com/ticket/email-mgmt-system/internal/nodesession"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

func TestTLSServerAcceptsAuthenticatedControlStream(t *testing.T) {
	certificateFile, keyFile, roots := writeTestCertificate(t)
	store := &fakeStateStore{server: model.MailServer{
		ID: 42, NodeUUID: stringPointer(testNodeUUID), EnrollmentState: model.EnrollmentApproved,
		DesiredRevision: 5, AppliedRevision: 5,
	}}
	gateway, err := New(store, nodesession.NewRegistry(), testAuthenticator, Config{})
	if err != nil {
		t.Fatal(err)
	}
	server, listener, err := NewTLSServer("127.0.0.1:0", certificateFile, keyFile, gateway)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		server.Stop()
		_ = listener.Close()
	}()

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12,
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"authorization", "Node node-secret", "x-mailhub-node-uuid", testNodeUUID,
	))
	stream, err := nodev1.NewNodeGatewayClient(conn).Control(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(helloFrame(testNodeUUID, time.Now().UTC(), []uint32{1})); err != nil {
		t.Fatal(err)
	}
	frame, err := stream.Recv()
	if err != nil || frame.GetWelcome() == nil {
		t.Fatalf("TLS Welcome = %#v, %v", frame, err)
	}
}

func writeTestCertificate(t *testing.T) (certificateFile, keyFile string, roots *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mailhub-node-control-test"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	directory := t.TempDir()
	certificateFile = filepath.Join(directory, "control.crt")
	keyFile = filepath.Join(directory, "control.key")
	if err := os.WriteFile(certificateFile, certificatePEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("failed to add test certificate")
	}
	return certificateFile, keyFile, roots
}
