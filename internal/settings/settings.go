// Package settings holds the small set of runtime-editable knobs (LLM
// provider, model names, Ollama URL) in the settings table, so the admin UI
// can change them without a redeploy. Everything else stays in env config.
package settings

import (
	"context"

	"codeberg.org/Safecast/quicky/internal/store"
)

const (
	LLMProvider = "llm_provider" // "anthropic" or "ollama"
	ModelFast   = "model_fast"
	ModelStrong = "model_strong"
	OllamaURL   = "ollama_url"
	OllamaModel = "ollama_model"
)

// Defaults mirror the env-based fallbacks the app shipped with before the
// settings table existed.
func Defaults(envModelFast, envModelStrong string) map[string]string {
	return map[string]string{
		LLMProvider: "anthropic",
		ModelFast:   envModelFast,
		ModelStrong: envModelStrong,
		OllamaURL:   "http://localhost:11434",
		OllamaModel: "llama3.1",
	}
}

// Store reads and writes the settings table. Values are cached in memory and
// refreshed on write, since the LLM router reads them on every request.
type Store struct {
	db       *store.DB
	defaults map[string]string
}

func New(db *store.DB, envModelFast, envModelStrong string) *Store {
	return &Store{db: db, defaults: Defaults(envModelFast, envModelStrong)}
}

// All returns every known setting, falling back to defaults for unset keys.
func (s *Store) All(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.defaults))
	for k, v := range s.defaults {
		out[k] = v
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Get returns a single setting, falling back to its default.
func (s *Store) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if err == nil {
		return v, nil
	}
	if v, ok := s.defaults[key]; ok {
		return v, nil
	}
	return "", err
}

// Set upserts a setting.
func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`,
		key, value)
	return err
}
