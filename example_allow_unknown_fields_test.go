package toml_test

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func ExampleDecoder_AllowUnknownFields() {
	type Plugin struct {
		Name string
	}
	type Config struct {
		Version string
		Plugins Plugin
	}

	doc := `
version = "1.0"

[plugins]
name = "core"

# Managed by an external component, unknown to this program.
[plugins.extra]
enabled = true
`

	var cfg Config
	err := toml.NewDecoder(strings.NewReader(doc)).
		DisallowUnknownFields().
		AllowUnknownFields(toml.Key{"plugins", "extra"}).
		Decode(&cfg)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Printf("version=%s plugin=%s\n", cfg.Version, cfg.Plugins.Name)
	// Output:
	// version=1.0 plugin=core
}
