package llm

import (
	"context"
)

// SettingsSource is the subset of settings.Store the router needs. Defined
// here (not imported) to avoid an import cycle with internal/settings, which
// depends on internal/store.
type SettingsSource interface {
	Get(ctx context.Context, key string) (string, error)
}

// Router picks between Anthropic and a local Ollama server per call, based on
// the "llm_provider" setting, so switching providers is a settings write, not
// a redeploy. When provider is "ollama", the request's Model is replaced with
// the configured Ollama model name, since callers pass Anthropic model IDs.
type Router struct {
	Anthropic Client
	Settings  SettingsSource

	// Keys matching internal/settings, duplicated here as plain strings to
	// avoid the import cycle.
	ProviderKey string
	URLKey      string
	ModelKey    string
}

func NewRouter(anthropic Client, settings SettingsSource) *Router {
	return &Router{
		Anthropic:   anthropic,
		Settings:    settings,
		ProviderKey: "llm_provider",
		URLKey:      "ollama_url",
		ModelKey:    "ollama_model",
	}
}

func (r *Router) resolve(ctx context.Context, req Request) (Client, Request, error) {
	provider, err := r.Settings.Get(ctx, r.ProviderKey)
	if err != nil || provider != "ollama" {
		return r.Anthropic, req, nil
	}
	url, err := r.Settings.Get(ctx, r.URLKey)
	if err != nil {
		return nil, req, err
	}
	model, err := r.Settings.Get(ctx, r.ModelKey)
	if err != nil {
		return nil, req, err
	}
	req.Model = model
	return NewOllama(url), req, nil
}

func (r *Router) Text(ctx context.Context, req Request) (string, error) {
	c, req, err := r.resolve(ctx, req)
	if err != nil {
		return "", err
	}
	return c.Text(ctx, req)
}

func (r *Router) Tool(ctx context.Context, req Request, tool Tool, out any) error {
	c, req, err := r.resolve(ctx, req)
	if err != nil {
		return err
	}
	return c.Tool(ctx, req, tool, out)
}
