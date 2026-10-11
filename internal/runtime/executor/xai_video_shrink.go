package executor

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// xaiVideoMaxRequestBytes is the request body ceiling we aim for on xAI video
// requests. The SuperGrok OAuth upstream (cli-chat-proxy.grok.com) rejects
// bodies above 1 MiB with HTTP 400 "Failed to read request body: length limit
// exceeded". loop/first+last-frame requests carry the start image twice, so a
// single full-resolution frame easily blows past that. We keep some headroom.
const xaiVideoMaxRequestBytes = 960 * 1024

// xaiVideoImagePaths are the JSON paths that may hold inline data URL frames.
var xaiVideoImagePaths = []string{"image.url", "last_frame.url"}

// shrinkXAIVideoPayload re-encodes inline data URL frames of an xAI video
// request (downscale to the requested output size, then JPEG with falling
// quality) until the whole body fits under xaiVideoMaxRequestBytes. Payloads
// that already fit, or that carry no decodable data URL images, are returned
// unchanged. The returned note describes what was done (empty if nothing).
func shrinkXAIVideoPayload(payload []byte) ([]byte, string) {
	if len(payload) <= xaiVideoMaxRequestBytes {
		return payload, ""
	}
	type frame struct {
		path string
		img  image.Image
		src  string
	}
	var frames []frame
	decoded := map[string]image.Image{}
	for _, p := range xaiVideoImagePaths {
		v := gjson.GetBytes(payload, p).String()
		if !strings.HasPrefix(v, "data:image/") {
			continue
		}
		img, ok := decoded[v]
		if !ok {
			var err error
			img, err = decodeXAIDataURLImage(v)
			if err != nil {
				continue
			}
			decoded[v] = img
		}
		frames = append(frames, frame{path: p, img: img, src: v})
	}
	if len(frames) == 0 {
		return payload, ""
	}

	maxW, maxH := xaiVideoTargetSize(gjson.GetBytes(payload, "resolution").String(), gjson.GetBytes(payload, "aspect_ratio").String())
	scaled := map[string]image.Image{}
	for _, f := range frames {
		if _, ok := scaled[f.src]; !ok {
			scaled[f.src] = xaiFitImage(f.img, maxW, maxH)
		}
	}
	origLen := len(payload)
	for _, q := range []int{92, 88, 84, 80, 75, 70, 65, 60, 50, 40} {
		out := payload
		encoded := map[string]string{}
		var err error
		for _, f := range frames {
			url, ok := encoded[f.src]
			if !ok {
				var buf bytes.Buffer
				if errEnc := jpeg.Encode(&buf, scaled[f.src], &jpeg.Options{Quality: q}); errEnc != nil {
					err = errEnc
					break
				}
				url = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
				encoded[f.src] = url
			}
			out, err = sjson.SetBytes(out, f.path, url)
			if err != nil {
				break
			}
		}
		if err != nil {
			return payload, ""
		}
		if len(out) <= xaiVideoMaxRequestBytes {
			b := scaled[frames[0].src].Bounds()
			return out, fmt.Sprintf("xai video: shrank inline frames to %dx%d JPEG q%d, body %d -> %d bytes", b.Dx(), b.Dy(), q, origLen, len(out))
		}
	}
	return payload, ""
}

func decodeXAIDataURLImage(dataURL string) (image.Image, error) {
	comma := strings.IndexByte(dataURL, ',')
	if comma < 0 || !strings.Contains(dataURL[:comma], ";base64") {
		return nil, fmt.Errorf("not a base64 data URL")
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[comma+1:])
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	return img, err
}

// xaiVideoTargetSize returns the bounding box of the requested output video.
func xaiVideoTargetSize(resolution, aspect string) (int, int) {
	short := 720
	switch strings.ToLower(strings.TrimSpace(resolution)) {
	case "480p":
		short = 480
	case "1080p":
		short = 1080
	}
	long := short * 16 / 9
	switch strings.TrimSpace(aspect) {
	case "9:16":
		return short, long
	case "16:9":
		return long, short
	case "1:1":
		return short, short
	case "3:4", "2:3":
		return short, long
	case "4:3", "3:2":
		return long, short
	}
	return long, long // auto/unknown: just cap the long side
}

// xaiFitImage downscales img (area averaging) so it fits inside maxW x maxH,
// keeping the aspect ratio. Smaller images are returned unchanged.
func xaiFitImage(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxW && h <= maxH {
		return img
	}
	scale := float64(maxW) / float64(w)
	if s := float64(maxH) / float64(h); s < scale {
		scale = s
	}
	nw, nh := int(float64(w)*scale+0.5), int(float64(h)*scale+0.5)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0 := b.Min.Y + y*h/nh
		y1 := b.Min.Y + (y+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0 := b.Min.X + x*w/nw
			x1 := b.Min.X + (x+1)*w/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, ca := img.At(xx, yy).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					bl += uint64(cb)
					a += uint64(ca)
					n++
				}
			}
			dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), uint8(a / n >> 8)})
		}
	}
	return dst
}
