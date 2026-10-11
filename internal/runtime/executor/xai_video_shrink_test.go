package executor

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestShrinkXAIVideoPayloadFitsUnderLimit(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2160, 3840))
	r := rand.New(rand.NewSource(1))
	for y := 0; y < 3840; y++ {
		for x := 0; x < 2160; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(r.Intn(256)), uint8(x), uint8(y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
	payload := []byte(`{"model":"grok-imagine-video-1.5","prompt":"x","resolution":"720p","aspect_ratio":"9:16"}`)
	payload, _ = sjson.SetBytes(payload, "image.url", url)
	payload, _ = sjson.SetBytes(payload, "last_frame.url", url)
	out, note := shrinkXAIVideoPayload(payload)
	if note == "" || len(out) > xaiVideoMaxRequestBytes {
		t.Fatalf("not shrunk: %d bytes, note=%q", len(out), note)
	}
	first := gjson.GetBytes(out, "image.url").String()
	if first != gjson.GetBytes(out, "last_frame.url").String() {
		t.Fatal("first and last frame differ after shrink")
	}
	got, err := decodeXAIDataURLImage(first)
	if err != nil {
		t.Fatal(err)
	}
	if b := got.Bounds(); b.Dx() != 720 || b.Dy() != 1280 {
		t.Fatalf("size %dx%d, want 720x1280", b.Dx(), b.Dy())
	}
}

func TestShrinkXAIVideoPayloadSmallUnchanged(t *testing.T) {
	payload := []byte(`{"model":"m","image":{"url":"data:image/jpeg;base64,AAAA"}}`)
	out, note := shrinkXAIVideoPayload(payload)
	if note != "" || !bytes.Equal(out, payload) {
		t.Fatal("small payload changed")
	}
}
