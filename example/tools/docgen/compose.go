package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// pinnedImageRE matches a fully pinned image reference: a repository, a
// version tag, and the digest the tag resolved to. The tag states which
// version a reader is running; the digest states which bytes.
var pinnedImageRE = regexp.MustCompile(`^([^\s:@]+(?::[0-9]+)?(?:/[^\s:@]+)*):([A-Za-z0-9._-]+)@sha256:([0-9a-f]{64})$`)

// versionTagRE matches a tag that names at least a major and a minor version.
var versionTagRE = regexp.MustCompile(`(^|[^0-9])[0-9]+\.[0-9]+`)

// imageLineRE matches the image key of a compose service.
var imageLineRE = regexp.MustCompile(`^\s*image:\s*(?:"([^"]*)"|'([^']*)'|([^\s#]+))`)

// Image is one image reference in the dependency stack.
type Image struct {
	Line       int
	Reference  string
	Repository string
	Tag        string
	Digest     string
}

// CollectImages returns the image references in a compose file.
func CollectImages(text string) []Image {
	var images []Image
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		m := imageLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ref := m[1] + m[2] + m[3]
		image := Image{Line: i + 1, Reference: ref}
		if p := pinnedImageRE.FindStringSubmatch(ref); p != nil {
			image.Repository, image.Tag, image.Digest = p[1], p[2], p[3]
		}
		images = append(images, image)
	}
	return images
}

// CheckImages applies the parity rules to the dependency stack: every image is
// pinned by digest, and every tag names the version it is pinned to. A local
// stack that runs a different version from the deployed one produces defects
// that reproduce nowhere, and a floating tag makes the local version a
// property of when the image was last pulled.
func CheckImages(file, text string) []string {
	images := CollectImages(text)
	if len(images) == 0 {
		return []string{fmt.Sprintf("%s: declares no image; the dependency stack cannot be checked", file)}
	}
	var problems []string
	for _, img := range images {
		switch {
		case img.Digest == "":
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s is not pinned by digest; write it as repository:tag@sha256:<digest>",
				file, img.Line, img.Reference))
		case img.Tag == "latest":
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s uses the latest tag; the tag states which version a reader is running",
				file, img.Line, img.Reference))
		case !versionTagRE.MatchString(img.Tag):
			problems = append(problems, fmt.Sprintf(
				"%s:%d: tag %q names no major and minor version; local and deployed parity cannot be read from it",
				file, img.Line, img.Tag))
		}
	}
	return problems
}

// ResolveImages asks the registry which digest each tag points at now and
// reports every reference whose digest no longer matches. It reaches the
// network, so it runs where the network is available rather than in every
// build.
func ResolveImages(ctx context.Context, file, text string, registry *Registry) []string {
	var problems []string
	for _, img := range CollectImages(text) {
		if img.Digest == "" {
			continue
		}
		digest, err := registry.Digest(ctx, img.Repository, img.Tag)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s:%d: %s: %v", file, img.Line, img.Reference, err))
			continue
		}
		if digest != "sha256:"+img.Digest {
			problems = append(problems, fmt.Sprintf(
				"%s:%d: %s:%s now resolves to %s; the pin is stale, update it and the tag together",
				file, img.Line, img.Repository, img.Tag, digest))
		}
	}
	return problems
}

// defaultRegistry is the host an image reference with no host belongs to, and
// the namespace a single-segment repository sits in.
const (
	defaultRegistry  = "registry-1.docker.io"
	defaultNamespace = "library"
)

// manifestTypes are the media types a manifest request accepts. An index and a
// single-platform manifest are both valid answers, and the digest of whichever
// one the registry serves is the value a pin holds.
const manifestTypes = "application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json"

// Registry reads image digests over the registry API. It speaks the API
// directly rather than shelling out to a container engine, because the engines
// disagree about which digest they print, and the pin is one exact string.
type Registry struct {
	// Client sends the requests. A nil client is a client with a timeout.
	Client *http.Client
	// BaseURL overrides the registry host. It is the seam a test drives.
	BaseURL string
}

// Digest reports the digest a tag currently resolves to.
func (r *Registry) Digest(ctx context.Context, repository, tag string) (string, error) {
	host, path := splitReference(repository)
	base := r.BaseURL
	if base == "" {
		base = "https://" + host
	}
	target := strings.TrimSuffix(base, "/") + "/v2/" + path + "/manifests/" + url.PathEscape(tag)

	resp, err := r.request(ctx, target, "")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		token, err := r.token(ctx, resp.Header.Get("WWW-Authenticate"))
		if err != nil {
			return "", err
		}
		_ = resp.Body.Close()
		if resp, err = r.request(ctx, target, token); err != nil {
			return "", err
		}
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the registry answered %d", resp.StatusCode)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("the registry reported no manifest digest")
	}
	return digest, nil
}

// request sends one manifest request, with a bearer token when one is held.
func (r *Registry) request(ctx context.Context, target, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", manifestTypes)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return r.client().Do(req)
}

// token completes the anonymous authentication a public registry asks for. The
// challenge names the service that issues the token and the scope it covers.
func (r *Registry) token(ctx context.Context, challenge string) (string, error) {
	params := challengeParams(challenge)
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("the registry asked for authentication without naming a token endpoint")
	}
	target, err := url.Parse(realm)
	if err != nil {
		return "", fmt.Errorf("unusable token endpoint %q: %w", realm, err)
	}
	query := target.Query()
	for _, key := range []string{"service", "scope"} {
		if v := params[key]; v != "" {
			query.Set(key, v)
		}
	}
	target.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the token endpoint answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var issued struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &issued); err != nil {
		return "", fmt.Errorf("read the issued token: %w", err)
	}
	if issued.Token == "" {
		issued.Token = issued.AccessToken
	}
	if issued.Token == "" {
		return "", fmt.Errorf("the token endpoint issued no token")
	}
	return issued.Token, nil
}

func (r *Registry) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// challengeParams reads the key="value" pairs of a Bearer challenge.
func challengeParams(challenge string) map[string]string {
	params := map[string]string{}
	rest, ok := strings.CutPrefix(strings.TrimSpace(challenge), "Bearer ")
	if !ok {
		return params
	}
	for part := range strings.SplitSeq(rest, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		params[key] = strings.Trim(value, `"`)
	}
	return params
}

// splitReference separates the registry host from the repository path and
// applies the defaults an unqualified reference carries.
func splitReference(repository string) (host, path string) {
	parts := strings.SplitN(repository, "/", 2)
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		return parts[0], parts[1]
	}
	if !strings.Contains(repository, "/") {
		return defaultRegistry, defaultNamespace + "/" + repository
	}
	return defaultRegistry, repository
}
