package util

import "testing"

func TestMaskAuthorizationHeaderPreservesLocalSessionLabels(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "hermes", in: "Bearer hermes", want: "Bearer hermes"},
		{name: "honcho", in: "Bearer honcho", want: "Bearer honcho"},
		{name: "qwen delegate", in: "Bearer qwen-delegate", want: "Bearer qwen-delegate"},
		{name: "lola", in: "Bearer lola", want: "Bearer lola"},
		{name: "jwt", in: "Bearer eyJhbGciOiJSUzI1NiIsImtpZCI6IjE5MzQ0", want: "Bearer eyJh...MzQ0"},
		{name: "openai key", in: "Bearer sk-local-codex", want: "Bearer sk-l...odex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MaskAuthorizationHeader(tt.in); got != tt.want {
				t.Fatalf("MaskAuthorizationHeader(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
