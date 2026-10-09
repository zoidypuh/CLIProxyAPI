package executor

import (
	"bytes"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestIsOpenRouterImagesBaseURL(t *testing.T) {
	if !isOpenRouterImagesBaseURL("https://openrouter.ai/api/v1") {
		t.Fatal("expected openrouter base url")
	}
	if isOpenRouterImagesBaseURL("https://api.apikey.fun/v1") {
		t.Fatal("apikey.fun must not be treated as openrouter")
	}
}

func TestPrepareOpenRouterImagesPayloadMultipartEdit(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range map[string]string{"model": "x", "prompt": "make it real", "quality": "max", "size": "auto", "background": "auto", "moderation": "low", "n": "1"} {
		_ = w.WriteField(k, v)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="image"; filename="image_0.png"`)
	h.Set("Content-Type", "image/png")
	part, _ := w.CreatePart(h)
	_, _ = part.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	_ = w.Close()

	out, err := prepareOpenRouterImagesPayload(buf.Bytes(), "openai/gpt-image-2.5-sunburst", w.FormDataContentType(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(out, "model").String(); got != "openai/gpt-image-2.5-sunburst" {
		t.Fatalf("model = %q", got)
	}
	if gjson.GetBytes(out, "size").Exists() {
		t.Fatal("size auto should be dropped")
	}
	if gjson.GetBytes(out, "moderation").Exists() || gjson.GetBytes(out, "provider.options.openai.moderation").String() != "low" {
		t.Fatalf("moderation not moved to passthrough: %s", out)
	}
	if gjson.GetBytes(out, "n").Int() != 1 || gjson.GetBytes(out, "quality").String() != "max" {
		t.Fatalf("bad params: %s", out)
	}
	ref := gjson.GetBytes(out, "input_references.0.image_url.url").String()
	if !strings.HasPrefix(ref, "data:image/png;base64,") {
		t.Fatalf("bad reference: %.60s", ref)
	}
}

func TestPrepareOpenRouterImagesPayloadJSONAndMask(t *testing.T) {
	out, err := prepareOpenRouterImagesPayload([]byte(`{"model":"m","prompt":"p","size":"1024x1536","response_format":"b64_json","n":2}`), "openai/gpt-image-2.5-sunburst", "application/json", true)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(out, "size").String() != "1024x1536" || gjson.GetBytes(out, "response_format").Exists() || !gjson.GetBytes(out, "stream").Bool() || gjson.GetBytes(out, "n").Int() != 2 {
		t.Fatalf("unexpected payload: %s", out)
	}
	if _, err := prepareOpenRouterImagesPayload([]byte(`{"prompt":"p","mask":"data:image/png;base64,AA=="}`), "m", "application/json", false); err == nil {
		t.Fatal("expected mask error")
	}
}
