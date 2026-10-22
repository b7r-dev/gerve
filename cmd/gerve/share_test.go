package main

import "testing"

func TestNormalizeShareURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		port int
		want string
	}{
		{name: "empty", raw: "", port: 8080, want: ""},
		{name: "bare host uses listen port", raw: "strowger.mule-bramble.b7r.dev", port: 8080, want: "http://strowger.mule-bramble.b7r.dev:8080"},
		{name: "bare host port 80", raw: "example.test", port: 80, want: "http://example.test"},
		{name: "bare host with port", raw: "example.test:9000", port: 8080, want: "http://example.test:9000"},
		{name: "full http keeps default port", raw: "http://example.test", port: 8080, want: "http://example.test"},
		{name: "full https keeps default port", raw: "https://example.test", port: 8080, want: "https://example.test"},
		{name: "full origin with port", raw: "http://example.test:8080", port: 9000, want: "http://example.test:8080"},
		{name: "trailing slash removed", raw: "https://example.test/", port: 8080, want: "https://example.test"},
		{name: "whitespace trimmed", raw: "  https://example.test  ", port: 8080, want: "https://example.test"},
		{name: "uppercase scheme", raw: "HTTPS://example.test", port: 8080, want: "https://example.test"},
		{name: "ipv6 bare host", raw: "[::1]", port: 8080, want: "http://[::1]:8080"},
		{name: "path rejected", raw: "https://example.test/gerve", want: ""},
		{name: "query rejected", raw: "https://example.test/?x=1", want: ""},
		{name: "fragment rejected", raw: "https://example.test/#top", want: ""},
		{name: "credentials rejected", raw: "https://user:pass@example.test", want: ""},
		{name: "non-http scheme rejected", raw: "ftp://example.test", want: ""},
		{name: "missing host rejected", raw: "https://", want: ""},
		{name: "missing port rejected", raw: "http://example.test:", want: ""},
		{name: "invalid port rejected", raw: "http://example.test:99999", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeShareURL(tt.raw, tt.port)
			if tt.want == "" {
				if tt.raw == "" {
					if err != nil || got != "" {
						t.Fatalf("normalizeShareURL(%q) = %q, %v; want empty success", tt.raw, got, err)
					}
					return
				}
				if err == nil {
					t.Fatalf("normalizeShareURL(%q) = %q, nil; want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeShareURL(%q): %v", tt.raw, err)
			}
			if got != tt.want {
				t.Errorf("normalizeShareURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseArgsShareURL(t *testing.T) {
	opts, err := parseArgs([]string{"--share-url", "http://example.test:8080", "."})
	if err != nil {
		t.Fatal(err)
	}
	if opts.shareURL != "http://example.test:8080" || opts.path != "." {
		t.Errorf("opts = %+v, want share URL and path", opts)
	}

	if _, err := parseArgs([]string{"--share-url"}); err == nil {
		t.Error("--share-url without a value did not fail")
	}
}

func TestShareOriginsPutsPublicFirstAndDeduplicates(t *testing.T) {
	got := shareOrigins("http://public.test:8080", []string{
		"http://192.168.1.5:8080",
		"http://public.test:8080",
		"http://192.168.1.5:8080",
	})
	want := []string{"http://public.test:8080", "http://192.168.1.5:8080"}
	if len(got) != len(want) {
		t.Fatalf("origins = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("origins[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
