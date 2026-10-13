package registry

import (
	"encoding/json"
	"strings"
)

// metaSupplementalModelsJSON lists Meta Muse models that the Muse OAuth
// account can reach on api.meta.ai but that the upstream model catalogs do not
// publish yet. They are appended to the active Meta catalog when missing so the
// proxy can route them for the meta provider.
const metaSupplementalModelsJSON = `[
  {
    "id": "muse-wonder-full-1.0",
    "object": "model",
    "created": 1791763200,
    "owned_by": "meta",
    "type": "meta",
    "display_name": "Muse Wonder Full 1.0",
    "name": "muse-wonder-full-1.0",
    "description": "Meta Muse Wonder Full 1.0, the full-size Wonder model for complex software engineering and long-horizon agentic work.",
    "context_length": 1048576,
    "max_completion_tokens": 65536,
    "thinking": {"levels": ["minimal", "low", "medium", "high", "xhigh", "max"]},
    "supportedInputModalities": ["text", "image", "video", "audio"],
    "supportedOutputModalities": ["text"]
  },
  {
    "id": "muse-wonder-full-1.0-contributor",
    "object": "model",
    "created": 1791763200,
    "owned_by": "meta",
    "type": "meta",
    "display_name": "Muse Wonder Full 1.0 Contributor",
    "name": "muse-wonder-full-1.0-contributor",
    "description": "Meta Muse Wonder Full 1.0 (contributor mode), the full-size Wonder model for complex software engineering and long-horizon agentic work.",
    "context_length": 1048576,
    "max_completion_tokens": 65536,
    "thinking": {"levels": ["minimal", "low", "medium", "high", "xhigh", "max"]},
    "supportedInputModalities": ["text", "image", "video", "audio"],
    "supportedOutputModalities": ["text"]
  }
]`

func metaSupplementalModels() []*ModelInfo {
	var models []*ModelInfo
	if errUnmarshal := json.Unmarshal([]byte(metaSupplementalModelsJSON), &models); errUnmarshal != nil {
		return nil
	}
	return models
}

// ensureMetaSupplementalModels appends supplemental Meta models that are not
// already present in the catalog. Entries published upstream take precedence.
func ensureMetaSupplementalModels(data *staticModelsJSON) {
	if data == nil {
		return
	}
	seen := make(map[string]struct{}, len(data.Meta))
	for _, model := range data.Meta {
		if model != nil {
			seen[strings.ToLower(strings.TrimSpace(model.ID))] = struct{}{}
		}
	}
	for _, model := range metaSupplementalModels() {
		if _, ok := seen[strings.ToLower(model.ID)]; ok {
			continue
		}
		data.Meta = append(data.Meta, model)
	}
}
