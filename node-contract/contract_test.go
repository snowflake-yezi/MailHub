package nodecontract_test

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	nodecontract "github.com/ticket/email-node-contract"
	nodev1 "github.com/ticket/email-node-contract/gen/mailhub/node/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const expectedDescriptorSHA256 = "3c1c21719ec464d8b1382944bea29380d0dedcb9a9cb82e09420c68f07889e8d"

func TestProtocolDescriptorCompatibility(t *testing.T) {
	descriptor := protodesc.ToFileDescriptorProto(nodev1.File_mailhub_node_v1_node_proto)
	descriptor.SourceCodeInfo = nil
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	actual := sha256.Sum256(data)
	got := hex.EncodeToString(actual[:])
	if got != expectedDescriptorSHA256 {
		t.Fatalf("node protocol descriptor changed: got %s, want %s", got, expectedDescriptorSHA256)
	}
}

func TestNodeGatewayUsesIndependentBidirectionalStreams(t *testing.T) {
	services := nodev1.File_mailhub_node_v1_node_proto.Services()
	service := services.ByName("NodeGateway")
	if service == nil {
		t.Fatal("NodeGateway service is missing")
	}
	for _, name := range []protoreflect.Name{"Control", "Data"} {
		method := service.Methods().ByName(name)
		if method == nil {
			t.Fatalf("NodeGateway.%s is missing", name)
		}
		if !method.IsStreamingClient() || !method.IsStreamingServer() {
			t.Fatalf("NodeGateway.%s must be bidirectional streaming", name)
		}
	}
}

func TestFrozenV1NamesAreUniqueAndVersioned(t *testing.T) {
	commands := toStrings(nodecontract.CommandTypesV1)
	notifications := toStrings(nodecontract.NotificationTypesV1)
	dataRequests := toStrings(nodecontract.DataRequestTypesV1)
	assertVersionedUnique(t, "command", commands)
	assertVersionedUnique(t, "notification", notifications)
	assertVersionedUnique(t, "data request", dataRequests)
	routes := make([]string, len(nodecontract.EnrollmentRoutesV1))
	for i, route := range nodecontract.EnrollmentRoutesV1 {
		routes[i] = route.Method + " " + route.Path
	}
	assertUnique(t, "enrollment route", routes)

	assertExact(t, "commands", commands, []string{
		"domain.apply.v1",
		"domain.remove.v1",
		"domain.inspect.v1",
		"mailbox.create.v1",
		"mailbox.password.v1",
		"mailbox.delete.v1",
		"mailbox.restore.v1",
		"message.delete.v1",
		"message.retention.purge.v1",
		"quarantine.release.v1",
		"quarantine.gc.v1",
	})
	assertExact(t, "notifications", notifications, []string{
		"config.revision.changed.v1",
		"filter.revision.changed.v1",
	})
	assertExact(t, "data requests", dataRequests, []string{
		"message.list.v1",
		"message.body.v1",
		"message.raw.v1",
		"message.attachment.v1",
		"message.attachment.preview.v1",
		"quarantine.message.v1",
		"quarantine.attachment.v1",
	})
	assertExact(t, "enrollment routes", routes, []string{
		"POST /api/v1/admin/node-enrollments",
		"GET /api/v1/admin/node-enrollments",
		"POST /api/v1/admin/node-enrollments/:id/revoke",
		"DELETE /api/v1/admin/node-enrollments/:id",
		"GET /api/v1/admin/node-enrollment-requests",
		"GET /api/v1/admin/node-enrollment-requests/:id",
		"POST /api/v1/admin/node-enrollment-requests/:id/approve",
		"POST /api/v1/admin/node-enrollment-requests/:id/reject",
		"GET /api/v1/admin/servers/:id/credentials",
		"POST /api/v1/admin/servers/:id/credentials/rotate",
		"POST /api/v1/admin/servers/:id/credentials/revoke",
		"POST /api/v1/admin/servers/:id/credentials/:credential_id/revoke",
		"DELETE /api/v1/admin/servers/:id/credentials/:credential_id",
		"POST /api/v1/admin/servers/:id/disconnect",
		"POST /api/v1/node-enrollments/claim",
		"GET /api/v1/node-enrollments/requests/:id",
		"POST /api/v1/node-enrollments/requests/:id/complete",
	})
}

func toStrings[T ~string](values []T) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result
}

func assertVersionedUnique(t *testing.T, kind string, values []string) {
	t.Helper()
	for _, value := range values {
		if !strings.HasSuffix(value, ".v1") {
			t.Errorf("%s %q is not explicitly versioned", kind, value)
		}
	}
	assertUnique(t, kind, values)
}

func assertUnique(t *testing.T, kind string, values []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			t.Errorf("duplicate %s %q", kind, value)
		}
		seen[value] = struct{}{}
	}
}

func assertExact(t *testing.T, kind string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("frozen %s changed:\n got: %q\nwant: %q", kind, got, want)
	}
}
