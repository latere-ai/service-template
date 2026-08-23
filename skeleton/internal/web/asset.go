package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Cache policy per asset class. A hashed name changes when the content
// changes, so its response can be cached for a year and never revalidated. A
// document keeps its name across content changes, so it carries a short
// freshness lifetime and an entity tag for cheap revalidation. Metadata that
// crawlers read is small and its staleness is visible, so it sits between the
// two.
const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheDocument  = "public, max-age=60, must-revalidate"
	cacheMetadata  = "public, max-age=300"
)

// encodings are the precompressed variants the handler serves, in the order
// the server prefers them. Brotli is first because it is smaller at equal
// decode cost for the clients that accept it.
var encodings = []struct {
	coding string
	suffix string
}{
	{coding: "br", suffix: ".br"},
	{coding: "gzip", suffix: ".gz"},
}

// hashedPattern matches a name whose last segment before the extension is a
// content hash, for example index-C3xK9pQ2.js or app.9f1c2ab4.css. The hash
// must also hold a digit, which separates a real hash from an ordinary word
// such as apple-touch-icon.png. A false negative costs one revalidation, so
// the rule is deliberately conservative.
var hashedPattern = regexp.MustCompile(`[-.]([A-Za-z0-9_-]{8,})\.[A-Za-z0-9]+$`)

// hashedName reports whether a file name carries a content hash.
func hashedName(name string) bool {
	m := hashedPattern.FindStringSubmatch(path.Base(name))
	if m == nil {
		return false
	}
	return strings.ContainsAny(m[1], "0123456789")
}

// metadataNames are the documents crawlers fetch by a fixed name.
var metadataNames = map[string]bool{
	"robots.txt":  true,
	"sitemap.xml": true,
}

// cacheControl returns the policy for one asset class.
func cacheControl(name string) string {
	switch {
	case hashedName(name):
		return cacheImmutable
	case metadataNames[path.Base(name)]:
		return cacheMetadata
	default:
		return cacheDocument
	}
}

// asset is one file of the bundle held in memory with everything a response
// needs. The bundle is embedded in the binary, so it is already resident and
// hashing it once at start-up keeps the request path free of file reads.
type asset struct {
	// name is the path from the document root, without an encoding suffix.
	name string
	// body is the bytes served, in the coding named by encoding.
	body []byte
	// encoding is the content coding of body, empty for identity.
	encoding string
	// etag is the entity tag, derived from the hash of body, so a variant in a
	// different coding never shares the tag of the identity representation.
	etag string
	// contentType is the media type of the decoded content.
	contentType string
	// cache is the Cache-Control value for the asset class.
	cache string
	// order is the server preference among the codings of one file, lowest
	// first. It is meaningful on a variant only.
	order int
}

// bundle is the served frontend: one identity representation per path and the
// precompressed variants beside it.
type bundle struct {
	identity map[string]*asset
	variants map[string][]*asset
}

// newBundle reads every file of the tree into memory and classifies it. It
// returns a usable bundle even on error, so a partially readable tree serves
// what it has instead of serving nothing.
func newBundle(fsys fs.FS) (*bundle, error) {
	b := &bundle{identity: map[string]*asset{}, variants: map[string][]*asset{}}
	if fsys == nil {
		return b, nil
	}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		b.add(p, body)
		return nil
	})
	if err != nil {
		return b, fmt.Errorf("read the frontend bundle: %w", err)
	}
	for name := range b.variants {
		// A variant with no identity representation cannot be negotiated,
		// because its media type and its class come from the decoded name.
		if _, ok := b.identity[name]; !ok {
			delete(b.variants, name)
			continue
		}
		sort.SliceStable(b.variants[name], func(i, j int) bool {
			return b.variants[name][i].order < b.variants[name][j].order
		})
	}
	return b, nil
}

// add classifies one file as an identity representation or as a
// precompressed variant of one.
func (b *bundle) add(p string, body []byte) {
	for i, e := range encodings {
		if !strings.HasSuffix(p, e.suffix) {
			continue
		}
		name := strings.TrimSuffix(p, e.suffix)
		b.variants[name] = append(b.variants[name], &asset{
			name:     name,
			body:     body,
			encoding: e.coding,
			etag:     entityTag(body),
			order:    i,
		})
		return
	}
	b.identity[p] = &asset{
		name:        p,
		body:        body,
		etag:        entityTag(body),
		contentType: mediaType(p, body),
		cache:       cacheControl(p),
	}
}

// lookup returns the identity representation at a document-root path.
func (b *bundle) lookup(name string) (*asset, bool) {
	a, ok := b.identity[name]
	return a, ok
}

// entityTag derives a strong entity tag from the content hash. The tag is
// computed over the bytes actually served, so two codings of one file never
// share a tag and a cache cannot hand a client a representation it did not
// accept.
func entityTag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// mediaType resolves the media type from the extension, and sniffs the
// content when the extension is unknown.
func mediaType(name string, body []byte) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return http.DetectContentType(body)
}

// serve writes one asset, negotiating a precompressed variant and delegating
// the range, the conditional request, and the status to the standard content
// server. The modification time is zero on purpose: an embedded file has no
// meaningful one, and a build time would make revalidation depend on when the
// binary was built rather than on what it holds.
func (b *bundle) serve(w http.ResponseWriter, r *http.Request, a *asset) {
	served := a
	if v := b.negotiate(a.name, r.Header.Get("Accept-Encoding")); v != nil {
		served = v
		w.Header().Set("Content-Encoding", v.encoding)
	}
	// The response varies by request header whether or not a variant exists,
	// because a cache must not reuse an identity response for a request that
	// would have been answered with a variant.
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", a.cache)
	w.Header().Set("ETag", served.etag)
	// The name is empty so the content server keeps the media type set above
	// instead of resolving it again from the extension.
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(served.body))
}

// negotiate picks the best precompressed variant the client accepts, or nil
// when the identity representation is the right answer.
func (b *bundle) negotiate(name, accept string) *asset {
	weights := acceptedEncodings(accept)
	if len(weights) == 0 {
		return nil
	}
	for _, v := range b.variants[name] {
		if acceptable(weights, v.encoding) {
			return v
		}
	}
	return nil
}

// acceptedEncodings parses an Accept-Encoding header into coding weights. A
// weight of zero rejects the coding, which is how a client that cannot decode
// a variant says so.
func acceptedEncodings(header string) map[string]float64 {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	weights := map[string]float64{}
	for part := range strings.SplitSeq(header, ",") {
		fields := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		if coding == "" {
			continue
		}
		q := 1.0
		for _, f := range fields[1:] {
			f = strings.TrimSpace(f)
			if !strings.HasPrefix(strings.ToLower(f), "q=") {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(f[2:]), 64)
			if err != nil {
				// An unparsable weight is treated as a rejection rather than
				// as full acceptance, so a malformed header cannot force a
				// coding on a client that may not decode it.
				q = 0
				continue
			}
			q = v
		}
		weights[coding] = q
	}
	return weights
}

// acceptable reports whether the client accepts one coding, honouring the
// wildcard form.
func acceptable(weights map[string]float64, coding string) bool {
	if q, ok := weights[coding]; ok {
		return q > 0
	}
	if q, ok := weights["*"]; ok {
		return q > 0
	}
	return false
}
