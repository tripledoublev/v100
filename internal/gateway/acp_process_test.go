package gateway

import (
	"context"
	"reflect"
	"testing"

	"github.com/tripledoublev/v100/internal/acp"
)

type fakeCallClient struct {
	method string
	params any
}

func (c *fakeCallClient) Call(_ context.Context, method string, params any, out any) error {
	c.method = method
	c.params = params
	if res, ok := out.(*acp.InitializeResult); ok {
		res.ProtocolVersion = acp.ProtocolVersion
	}
	return nil
}

func TestACPProcessArgs(t *testing.T) {
	tests := []struct {
		name     string
		cfgPath  string
		provider string
		want     []string
	}{
		{name: "minimal", want: []string{"acp"}},
		{name: "config", cfgPath: "/tmp/v100.toml", want: []string{"acp", "--config", "/tmp/v100.toml"}},
		{name: "provider", provider: "glm", want: []string{"acp", "--provider", "glm"}},
		{name: "both", cfgPath: "/tmp/v100.toml", provider: "glm", want: []string{"acp", "--config", "/tmp/v100.toml", "--provider", "glm"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ACPProcessArgs(tt.cfgPath, tt.provider); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ACPProcessArgs() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestInitializeACPUsesGatewayClientInfo(t *testing.T) {
	client := &fakeCallClient{}
	if err := InitializeACP(context.Background(), client, ACPProcessOptions{}); err != nil {
		t.Fatalf("InitializeACP returned error: %v", err)
	}
	if client.method != acp.MethodInitialize {
		t.Fatalf("method = %q, want %q", client.method, acp.MethodInitialize)
	}
	params, ok := client.params.(acp.InitializeParams)
	if !ok {
		t.Fatalf("params type = %T", client.params)
	}
	if params.ProtocolVersion != acp.ProtocolVersion {
		t.Fatalf("protocol = %d, want %d", params.ProtocolVersion, acp.ProtocolVersion)
	}
	if params.ClientInfo.Name != "v100-gateway" || params.ClientInfo.Version != "dev" {
		t.Fatalf("client info = %#v", params.ClientInfo)
	}
}

func TestInitializeACPAllowsClientInfoOverride(t *testing.T) {
	client := &fakeCallClient{}
	err := InitializeACP(context.Background(), client, ACPProcessOptions{
		ClientInfoName:    "custom",
		ClientInfoVersion: "v1",
	})
	if err != nil {
		t.Fatalf("InitializeACP returned error: %v", err)
	}
	params := client.params.(acp.InitializeParams)
	if params.ClientInfo.Name != "custom" || params.ClientInfo.Version != "v1" {
		t.Fatalf("client info = %#v", params.ClientInfo)
	}
}
