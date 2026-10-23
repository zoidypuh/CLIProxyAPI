package registry

import "testing"

func TestEnsureMetaSupplementalModelsAddsWonderFull(t *testing.T) {
	data := &staticModelsJSON{Meta: []*ModelInfo{{ID: "muse-wonder-1.0"}}}
	ensureMetaSupplementalModels(data)
	ids := map[string]*ModelInfo{}
	for _, m := range data.Meta {
		ids[m.ID] = m
	}
	for _, id := range []string{"muse-wonder-1.0", "muse-wonder-full-1.0", "muse-wonder-full-1.0-contributor"} {
		if ids[id] == nil {
			t.Fatalf("missing %s in Meta catalog", id)
		}
	}
	if ids["muse-wonder-full-1.0"].Thinking == nil || ids["muse-wonder-full-1.0"].ContextLength != 1048576 {
		t.Fatalf("unexpected metadata: %+v", ids["muse-wonder-full-1.0"])
	}
	ensureMetaSupplementalModels(data)
	if len(data.Meta) != 4 {
		t.Fatalf("expected idempotent append, got %d models", len(data.Meta))
	}
}

func TestEnsureMetaSupplementalModelsKeepsUpstreamEntry(t *testing.T) {
	upstream := &ModelInfo{ID: "muse-wonder-full-1.0", ContextLength: 42}
	data := &staticModelsJSON{Meta: []*ModelInfo{upstream}}
	ensureMetaSupplementalModels(data)
	if len(data.Meta) != 3 || data.Meta[0] != upstream || data.Meta[0].ContextLength != 42 {
		t.Fatalf("upstream entry should win: %+v", data.Meta)
	}
}

func TestGetMetaModelsIncludesWonderFull(t *testing.T) {
	found := map[string]bool{}
	for _, m := range GetMetaModels() {
		found[m.ID] = true
	}
	if !found["muse-wonder-full-1.0"] || !found["muse-wonder-full-1.0-contributor"] {
		t.Fatalf("GetMetaModels missing Wonder Full models: %v", found)
	}
	if !found["muse-image-1.0"] {
		t.Fatalf("GetMetaModels missing muse-image-1.0: %v", found)
	}
}

func TestGetMetaModelsMarksMuseImageAsImageEndpoint(t *testing.T) {
	for _, m := range GetMetaModels() {
		if m != nil && m.ID == "muse-image-1.0" {
			if m.Type != OpenAIImageModelType {
				t.Fatalf("muse-image-1.0 type = %q, want %q", m.Type, OpenAIImageModelType)
			}
			return
		}
	}
	t.Fatal("muse-image-1.0 missing")
}
