package build

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"openheron/llm/common"
)

var ErrNoBaseURL = errors.New("llm: no endpoint configured; set OPENAI_BASE_URL or ARK_BASE_URL")

func ConfigFromEnv() (*common.Config, error) {
	providerID := strings.TrimSpace(os.Getenv("MODEL_TYPE"))
	if providerID == "" {
		providerID = "openai"
	}

	c := &common.Config{
		ProviderID: providerID,
		BaseURL:    firstEnv("OPENAI_BASE_URL", "ARK_BASE_URL"),
		APIKey:     firstEnv("OPENAI_API_KEY", "ARK_API_KEY"),
		Model:      firstEnv("OPENAI_MODEL", "OPENAI_MODEL_ID", "ARK_MODEL", "ARK_MODEL_ID"),
		Timeout:    5 * time.Minute,
	}

	if c.BaseURL == "" {
		return nil, ErrNoBaseURL
	}
	if d, ok := envDuration("MODEL_TIMEOUT"); ok {
		c.Timeout = d
	}
	//	  MODEL_EXTRA_JSON='{"thinking":{"type":"disabled"}}'
	if v := strings.TrimSpace(os.Getenv("MODEL_EXTRA_JSON")); v != "" {
		var fields map[string]any
		if err := json.Unmarshal([]byte(v), &fields); err != nil {
			return nil, errors.New("llm: MODEL_EXTRA_JSON is not a JSON object: " + err.Error())
		}
		c.ExtraFields = fields
	}
	return c, nil
}

func envDuration(key string) (time.Duration, bool) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, false
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}
