package handler

import (
	"net/http"

	"github.com/ticket/email-mgmt-system/internal/nodetransport"
)

func newTestNodeTransport(sharedSecret string, client *http.Client) *nodetransport.LegacyHTTPTransport {
	return nodetransport.NewLegacyHTTPTransportWithOptions(sharedSecret, nodetransport.LegacyHTTPOptions{
		Client: client, RawClient: client,
	})
}
