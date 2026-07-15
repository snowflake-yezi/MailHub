package store

import (
	"reflect"
	"testing"
)

func TestLegacyScopesToPermissions(t *testing.T) {
	tests := []struct {
		name   string
		scopes string
		want   []string
	}{
		{name: "ticket center", scopes: "mailbox:create,mailbox:read", want: []string{"mailbox:create", "mailbox:disable", "mailbox:read"}},
		{name: "email reader", scopes: "email:read", want: []string{"email:attachment", "email:body", "email:list"}},
		{name: "deduplicate", scopes: "email:read, email:read", want: []string{"email:attachment", "email:body", "email:list"}},
		{name: "wildcard", scopes: "mailbox:read,*", want: []string{"*"}},
		{name: "unknown ignored", scopes: "unknown", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LegacyScopesToPermissions(tt.scopes); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("LegacyScopesToPermissions(%q) = %#v, want %#v", tt.scopes, got, tt.want)
			}
		})
	}
}
