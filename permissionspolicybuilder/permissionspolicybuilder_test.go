package permissionspolicybuilder

import (
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*Builder) error
		want    string
		wantErr bool
	}{
		{
			name:  "no features yields empty policy",
			setup: func(_ *Builder) error { return nil },
			want:  "",
		},
		{
			name:  "empty allow list denies everywhere",
			setup: func(b *Builder) error { return b.BlockFeature("geolocation") },
			want:  "geolocation=()",
		},
		{
			name:  "allow all",
			setup: func(b *Builder) error { return b.AllowFeature("camera") },
			want:  "camera=*",
		},
		{
			name:  "self only",
			setup: func(b *Builder) error { return b.AllowSelf("geolocation") },
			want:  "geolocation=(self)",
		},
		{
			name:  "empty explicit origin list falls back to self",
			setup: func(b *Builder) error { return b.AllowOrigins("geolocation") },
			want:  "geolocation=(self)",
		},
		{
			name: "multiple origins are quoted and keep insertion order",
			setup: func(b *Builder) error {
				return b.AllowOrigins("camera", "https://example.com", "https://docs.example.com")
			},
			want: `camera=(self "https://example.com" "https://docs.example.com")`,
		},
		{
			name: "features sorted lexicographically regardless of insertion order",
			setup: func(b *Builder) error {
				if err := b.AllowSelf("vibrate"); err != nil {
					return err
				}

				if err := b.BlockFeature("geolocation"); err != nil {
					return err
				}

				return b.AllowFeature("camera")
			},
			want: "camera=*, geolocation=(), vibrate=(self)",
		},
		{
			name: "uses commas not csp semicolons",
			setup: func(b *Builder) error {
				if err := b.BlockFeature("camera"); err != nil {
					return err
				}

				return b.BlockFeature("microphone")
			},
			want: "camera=(), microphone=()",
		},
		{
			name: "repeated identical allow list is a no op",
			setup: func(b *Builder) error {
				if err := b.AllowOrigins("camera", "https://example.com"); err != nil {
					return err
				}

				return b.AllowOrigins("camera", "https://example.com")
			},
			want: `camera=(self "https://example.com")`,
		},
		{
			name: "conflicting allow list for same feature",
			setup: func(b *Builder) error {
				if err := b.AllowFeature("camera"); err != nil {
					return err
				}

				return b.BlockFeature("camera")
			},
			wantErr: true,
		},
		{
			name:    "invalid feature name",
			setup:   func(b *Builder) error { return b.BlockFeature("Camera") },
			wantErr: true,
		},
		{
			name: "wildcard cannot combine with self",
			setup: func(b *Builder) error {
				return b.AddFeature("camera", AllowList{AllowAll: true, Self: true})
			},
			wantErr: true,
		},
		{
			name: "wildcard cannot combine with origins",
			setup: func(b *Builder) error {
				return b.AddFeature("camera", AllowList{AllowAll: true, Origins: []string{"https://example.com"}})
			},
			wantErr: true,
		},
		{
			name:    "origin with path rejected",
			setup:   func(b *Builder) error { return b.AllowOrigins("camera", "https://example.com/") },
			wantErr: true,
		},
		{
			name:    "origin with query rejected",
			setup:   func(b *Builder) error { return b.AllowOrigins("camera", "https://example.com?x=1") },
			wantErr: true,
		},
		{
			name:    "origin with non http scheme rejected",
			setup:   func(b *Builder) error { return b.AllowOrigins("camera", "data:text/plain") },
			wantErr: true,
		},
		{
			name:    "origin missing host rejected",
			setup:   func(b *Builder) error { return b.AllowOrigins("camera", "https://") },
			wantErr: true,
		},
		{
			name:    "empty origin rejected",
			setup:   func(b *Builder) error { return b.AllowOrigins("camera", "") },
			wantErr: true,
		},
		{
			name:    "origin with embedded quote rejected",
			setup:   func(b *Builder) error { return b.AllowOrigins("camera", `https://example".com`) },
			wantErr: true,
		},
		{
			name: "each origin is validated and first bad one errors",
			setup: func(b *Builder) error {
				return b.AllowOrigins("camera", "https://example.com", "ftp://example.com")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Builder{}

			setupErr := tt.setup(b)

			got, err := b.Build()
			if tt.wantErr {
				if err == nil && setupErr == nil {
					t.Fatalf("expected error, got policy %q", got)
				}

				return
			}

			if setupErr != nil {
				t.Fatalf("unexpected setup error: %v", setupErr)
			}

			if err != nil {
				t.Fatalf("unexpected build error: %v", err)
			}

			if got != tt.want {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMustBuildPanicsOnConflict(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustBuild did not panic on conflicting configuration")
		}
	}()

	b := &Builder{}
	b.Features = map[string]AllowList{
		"camera": {AllowAll: true, Self: true},
	}
	b.MustBuild()
}

func TestBuildDoesNotUseCSPQuoting(t *testing.T) {
	b := &Builder{}
	if err := b.AllowOrigins("camera", "https://example.com", "https://foo.example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := b.Build()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(got, "'self'") {
		t.Errorf("self must be unquoted in Permissions-Policy, got %q", got)
	}

	if !strings.Contains(got, `"https://example.com"`) {
		t.Errorf("origin must be double quoted, got %q", got)
	}

	if strings.Contains(got, ";") {
		t.Errorf("Permissions-Policy must not use CSP semicolon separators, got %q", got)
	}
}
