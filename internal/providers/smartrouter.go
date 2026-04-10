package providers

import "context"

// SmartRouterProvider is a thin wrapper used for session-level "auto" routing.
// The solver still decides when to use the cheap vs smart backend; this wrapper
// keeps run metadata and capability checks consistent.
type SmartRouterProvider struct {
	Cheap Provider
	Smart Provider
}

func (p *SmartRouterProvider) Name() string { return "smartrouter" }

func (p *SmartRouterProvider) Capabilities() Capabilities {
	var caps Capabilities
	if p.Cheap != nil {
		cc := p.Cheap.Capabilities()
		caps.ToolCalls = caps.ToolCalls || cc.ToolCalls
		caps.JSONMode = caps.JSONMode || cc.JSONMode
		caps.Streaming = caps.Streaming || cc.Streaming
		caps.Images = caps.Images || cc.Images
	}
	if p.Smart != nil {
		sc := p.Smart.Capabilities()
		caps.ToolCalls = caps.ToolCalls || sc.ToolCalls
		caps.JSONMode = caps.JSONMode || sc.JSONMode
		caps.Streaming = caps.Streaming || sc.Streaming
		caps.Images = caps.Images || sc.Images
	}
	return caps
}

func (p *SmartRouterProvider) primary() Provider {
	if p.Smart != nil {
		return p.Smart
	}
	return p.Cheap
}

func (p *SmartRouterProvider) Complete(ctx context.Context, req CompleteRequest) (CompleteResponse, error) {
	return p.primary().Complete(ctx, req)
}

func (p *SmartRouterProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	return p.primary().Embed(ctx, req)
}

func (p *SmartRouterProvider) Metadata(ctx context.Context, model string) (ModelMetadata, error) {
	if p.Smart != nil {
		return p.Smart.Metadata(ctx, model)
	}
	return p.Cheap.Metadata(ctx, model)
}
