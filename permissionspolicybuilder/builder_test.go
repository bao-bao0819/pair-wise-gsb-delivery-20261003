package permissionspolicybuilder

import "testing"

func TestBuilderBuild(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		features map[string]FeatureRule
		want     string
		wantErr  bool
	}{
		{
			name:     "no features",
			features: nil,
			want:     "",
		},
		{
			name: "allow all",
			features: map[string]FeatureRule{
				"geolocation": {Type: AllowAll},
			},
			want: "geolocation=*",
		},
		{
			name: "deny all",
			features: map[string]FeatureRule{
				"camera": {Type: AllowNone},
			},
			want: "camera=()",
		},
		{
			name: "self",
			features: map[string]FeatureRule{
				"microphone": {Type: AllowSelf},
			},
			want: "microphone=(self)",
		},
		{
			name: "empty explicit origin list denies all",
			features: map[string]FeatureRule{
				"camera": {Type: AllowOrigins, Origins: []string{}},
			},
			want: "camera=()",
		},
		{
			name: "single explicit origin is quoted",
			features: map[string]FeatureRule{
				"geolocation": {
					Type:    AllowOrigins,
					Origins: []string{"https://example.com"},
				},
			},
			want: `geolocation=("https://example.com")`,
		},
		{
			name: "multiple origins are space separated and quoted",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"https://a.example.com", "https://b.example.com"},
				},
			},
			want: `camera=("https://a.example.com" "https://b.example.com")`,
		},
		{
			name: "origin with non default port keeps the port",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"http://localhost:8080"},
				},
			},
			want: `camera=("http://localhost:8080")`,
		},
		{
			name: "directives are sorted by feature name and comma separated",
			features: map[string]FeatureRule{
				"vibrate":     {Type: AllowNone},
				"camera":      {Type: AllowSelf},
				"geolocation": {Type: AllowAll},
			},
			want: "camera=(self), geolocation=*, vibrate=()",
		},
		{
			name: "invalid feature name",
			features: map[string]FeatureRule{
				"GEO": {Type: AllowAll},
			},
			wantErr: true,
		},
		{
			name: "unknown allowlist type",
			features: map[string]FeatureRule{
				"camera": {Type: "bogus"},
			},
			wantErr: true,
		},
		{
			name: "allow all with origins",
			features: map[string]FeatureRule{
				"camera": {Type: AllowAll, Origins: []string{"https://example.com"}},
			},
			wantErr: true,
		},
		{
			name: "self with origins",
			features: map[string]FeatureRule{
				"camera": {Type: AllowSelf, Origins: []string{"https://example.com"}},
			},
			wantErr: true,
		},
		{
			name: "deny all with origins",
			features: map[string]FeatureRule{
				"camera": {Type: AllowNone, Origins: []string{"https://example.com"}},
			},
			wantErr: true,
		},
		{
			name: "rejects csp style quoted keywords as origins",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"'self'"},
				},
			},
			wantErr: true,
		},
		{
			name: "rejects wildcard origin",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"*"},
				},
			},
			wantErr: true,
		},
		{
			name: "rejects origin with path",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"https://example.com/path"},
				},
			},
			wantErr: true,
		},
		{
			name: "rejects origin with query",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"https://example.com?x=1"},
				},
			},
			wantErr: true,
		},
		{
			name: "rejects origin with fragment",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"https://example.com#frag"},
				},
			},
			wantErr: true,
		},
		{
			name: "rejects non http scheme",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"ftp://example.com"},
				},
			},
			wantErr: true,
		},
		{
			name: "rejects relative origin",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"example.com"},
				},
			},
			wantErr: true,
		},
		{
			name: "reports the invalid origin individually",
			features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"https://good.example.com", "://bad"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := &Builder{Features: tt.features}

			got, err := builder.Build()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Build() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuilderAdd(t *testing.T) {
	t.Parallel()

	t.Run("adds rules", func(t *testing.T) {
		t.Parallel()

		var builder Builder
		if err := builder.Add("camera", FeatureRule{Type: AllowSelf}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := builder.Add("geolocation", FeatureRule{
			Type:    AllowOrigins,
			Origins: []string{"https://example.com"},
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, err := builder.Build()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if want := `camera=(self), geolocation=("https://example.com")`; got != want {
			t.Errorf("Build() = %q, want %q", got, want)
		}
	})

	t.Run("identical duplicate rule is accepted", func(t *testing.T) {
		t.Parallel()

		var builder Builder
		rule := FeatureRule{
			Type:    AllowOrigins,
			Origins: []string{"https://example.com/"},
		}

		if err := builder.Add("camera", rule); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := builder.Add("camera", rule); err != nil {
			t.Fatalf("identical rule should be accepted, got: %v", err)
		}

		got, err := builder.Build()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if want := `camera=("https://example.com")`; got != want {
			t.Errorf("Build() = %q, want %q", got, want)
		}
	})

	t.Run("contradictory duplicate rule is rejected", func(t *testing.T) {
		t.Parallel()

		var builder Builder
		if err := builder.Add("camera", FeatureRule{Type: AllowSelf}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if err := builder.Add("camera", FeatureRule{Type: AllowNone}); err == nil {
			t.Fatal("expected an error for contradictory rules, got nil")
		}
	})

	t.Run("invalid rule is rejected before storing", func(t *testing.T) {
		t.Parallel()

		var builder Builder
		if err := builder.Add("camera", FeatureRule{
			Type:    AllowOrigins,
			Origins: []string{"*"},
		}); err == nil {
			t.Fatal("expected an error for an invalid origin, got nil")
		}

		if _, ok := builder.Features["camera"]; ok {
			t.Fatal("invalid rule should not have been stored")
		}
	})
}

func TestMustBuild(t *testing.T) {
	t.Parallel()

	t.Run("valid policy", func(t *testing.T) {
		t.Parallel()

		builder := &Builder{
			Features: map[string]FeatureRule{
				"camera": {Type: AllowNone},
			},
		}

		if got := builder.MustBuild(); got != "camera=()" {
			t.Errorf("MustBuild() = %q, want %q", got, "camera=()")
		}
	})

	t.Run("invalid policy panics", func(t *testing.T) {
		t.Parallel()

		defer func() {
			if recover() == nil {
				t.Fatal("expected a panic, got none")
			}
		}()

		builder := &Builder{
			Features: map[string]FeatureRule{
				"camera": {
					Type:    AllowOrigins,
					Origins: []string{"*"},
				},
			},
		}
		_ = builder.MustBuild()
	})
}
