// YAML manifest loader.
//
// The single source of truth for an App's metadata is its
// manifest.yaml. The loader reads it from disk (or any io.Reader),
// decodes the YAML into a Manifest, and applies the small mapping
// rules that bridge YAML conventions and Go struct compat:
//
//   * YAML key `identifier:` → Go Manifest.Identifier (slug, preferred)
//                                AND Manifest.Name (legacy field used
//                                for routing and unique-name keying)
//   * YAML key `name:`        → Go Manifest.Title (display name)
//   * YAML key `author:`      → accepts either string ("Acme") or
//                                object ({name, url, public_key});
//                                string form populates Author only,
//                                object form fills Author + AuthorURL
//                                + AuthorPublicKey
//
// The loader is forgiving: unknown YAML keys are ignored (not an
// error) so a manifest from a future SDK version still loads on an
// older client. The validator (validator.go) is the strict layer —
// run it explicitly on production paths.

package biuapp

import (
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadManifest reads a YAML manifest from the given file path.
// Returns the parsed Manifest with all v1.5 mapping rules applied.
// Use ParseManifest if you have a byte slice or io.Reader.
func LoadManifest(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open manifest %s: %w", path, err)
	}
	defer f.Close()
	return ParseManifest(f)
}

// MustLoadManifest is the panic-on-error variant. Bundled apps that
// embed their manifest at compile time use this in their Manifest()
// method so a malformed bundled manifest fails service startup
// (loud) rather than silently degrading at request time.
//
// Third-party Apps should NOT use this — they should LoadManifest
// and surface errors through their Init.
func MustLoadManifest(path string) Manifest {
	m, err := LoadManifest(path)
	if err != nil {
		panic("biuapp: " + err.Error())
	}
	return *m
}

// ParseManifest decodes from any io.Reader (file / embed.FS / bytes).
func ParseManifest(r io.Reader) (*Manifest, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	return ParseManifestBytes(raw)
}

// ParseManifestBytes decodes from a byte slice. Returns the populated
// Manifest with the v1.5 mapping rules applied.
func ParseManifestBytes(raw []byte) (*Manifest, error) {
	if len(raw) == 0 {
		return nil, errors.New("biuapp: empty manifest")
	}

	// Decode into an intermediate shape that captures BOTH the legacy
	// field set (Name/Author as plain string) AND the v1.5 keys
	// (identifier/name pair, author as object). We then resolve the
	// canonical Manifest from this shape.
	var doc rawManifest
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse manifest yaml: %w", err)
	}
	return doc.toManifest()
}

// rawManifest is the on-the-wire YAML shape. The fields here include
// only what differs from Manifest's own yaml tags — i.e. the disjoint
// or polymorphic ones. Everything else is filled by yaml's default
// inline decoding via the embedded Manifest.
type rawManifest struct {
	// Capture YAML's `identifier:` and `name:` keys explicitly so we
	// can route them: identifier → both Manifest.Name (legacy slug)
	// and Manifest.Identifier; name → Manifest.Title.
	Identifier string `yaml:"identifier"`
	Name       string `yaml:"name"` // display name in YAML; maps to Title

	// Author can be either a bare string or an object. yaml.Node lets
	// us distinguish at decode time without a custom unmarshaler on
	// the struct itself (which would fight the embedded Manifest's
	// own yaml tags).
	Author yaml.Node `yaml:"author"`

	// Everything else lives inline so we don't repeat field names.
	// Note: this aliases Manifest.Name — gopkg.in/yaml.v3 picks the
	// outer field with the same yaml tag, which is why Manifest.Name
	// has tag `yaml:"-"` (we feed it from Identifier here instead).
	Manifest `yaml:",inline"`
}

func (r *rawManifest) toManifest() (*Manifest, error) {
	m := r.Manifest

	// identifier / name routing (see file-level doc).
	if r.Identifier != "" {
		m.Name = r.Identifier
		m.Identifier = r.Identifier
	}
	if r.Name != "" {
		m.Title = r.Name
	}

	// author polymorphism: scalar string vs mapping.
	switch r.Author.Kind {
	case 0:
		// not present
	case yaml.ScalarNode:
		m.Author = r.Author.Value
	case yaml.MappingNode:
		var obj struct {
			Name      string `yaml:"name"`
			URL       string `yaml:"url"`
			PublicKey string `yaml:"public_key"`
		}
		if err := r.Author.Decode(&obj); err != nil {
			return nil, fmt.Errorf("parse author: %w", err)
		}
		m.Author = obj.Name
		m.AuthorURL = obj.URL
		m.AuthorPublicKey = obj.PublicKey
	default:
		return nil, fmt.Errorf("manifest.author must be string or object, got %v", r.Author.Kind)
	}

	return &m, nil
}
