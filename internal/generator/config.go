// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: MIT

package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// ConfigFile is the consumer declaration at the root of a generated
// repository. LockFile sits beside it.
const (
	ConfigFile = ".template.yaml"
	LockFile   = "template.lock"
)

// DefaultTemplate is the template identity written into a new declaration.
const DefaultTemplate = "github.com/latere-ai/service-template"

// Profiles are the repository shapes the generator can scaffold. A profile is
// fixed at scaffold time because changing it is a repository rewrite.
const (
	ProfileService      = "service"
	ProfileLibrary      = "library"
	ProfileFrontendOnly = "frontend-only"
)

// Features are the optional capabilities a profile can switch on.
const (
	FeatureFrontend   = "frontend"
	FeatureSEO        = "seo"
	FeatureI18n       = "i18n"
	FeatureDatabase   = "database"
	FeatureBackground = "background"
)

// AllFeatures is every feature flag name, in the order documents list them.
var AllFeatures = []string{
	FeatureFrontend,
	FeatureSEO,
	FeatureI18n,
	FeatureDatabase,
	FeatureBackground,
}

// profileFeatures is the set of flags each profile supports. A library
// publishes importable packages only, so it supports none.
var profileFeatures = map[string]map[string]bool{
	ProfileService: {
		FeatureFrontend: true, FeatureSEO: true, FeatureI18n: true,
		FeatureDatabase: true, FeatureBackground: true,
	},
	ProfileLibrary: {},
	ProfileFrontendOnly: {
		FeatureFrontend: true, FeatureSEO: true, FeatureI18n: true,
	},
}

// AllProfiles is every profile name in declaration order.
var AllProfiles = []string{ProfileService, ProfileLibrary, ProfileFrontendOnly}

// License is the terms a generated repository is released under. The
// generator renders its notice at the top of every Go file it writes,
// declares it to the license gate in .lateregate.yaml, and writes LICENSE from
// it where it ships the text, so the notice, the gate, and the root file name
// the same terms.
//
// The notice is ordinary rendered content: it is part of the bytes the lock
// records a digest of, so check, sync, and upgrade compare and rewrite it like
// any other line and need no rule of their own for it. Its year is declared
// rather than read from the clock for the same reason: a render that read the
// clock would report every generated Go file as drift on 1 January.
//
// The skeleton itself carries no notice. It is published under the template
// repository's license, and the terms of a service built from it are that
// service's decision, recorded here.
type License struct {
	// SPDX is the identifier, one of Licenses.
	SPDX string
	// Holder is the copyright holder, written literally in every notice.
	Holder string
	// Year is the year of first publication every notice names. init
	// records the year it runs in, and upgrade records it for a declaration
	// written before the field existed.
	Year string
}

// The license a declaration that names none is released under. A repository
// of an organization starts closed: every right reserved, no license granted,
// until somebody decides otherwise and says so in the declaration.
const (
	DefaultLicense = "LicenseRef-Proprietary"
	DefaultHolder  = "Latere AI"
)

// Licenses are the identifiers a declaration may name: the ones the license
// gate of latere.ai/x/ci-gate can check LICENSE against. Any other identifier
// fails that gate, so it fails here first, at scaffold time.
var Licenses = []string{
	"LicenseRef-Proprietary",
	"MIT",
	"Apache-2.0",
	"AGPL-3.0-only",
	"AGPL-3.0-or-later",
}

// holderPattern keeps the holder a single plain phrase. It is written
// unquoted into YAML and into a comment on every Go file, so a character
// either would read as syntax is refused rather than escaped.
var holderPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9 .,&()'-]*[A-Za-z0-9.)])?$`)

// yearPattern is the one year a notice names. The license gate also accepts a
// range, which a repository writes by hand on the files it owns.
var yearPattern = regexp.MustCompile(`^[0-9]{4}$`)

// Waiver records a generated file the repository deliberately diverges on. A
// waiver suppresses the "edited" verdict for that path until it expires.
type Waiver struct {
	Path    string
	Reason  string
	Expires time.Time
}

// Config is the parsed consumer declaration.
//
// The coverage gate is not declared here. Its threshold and its exemptions
// live in .lateregate.yaml beside every other gate's configuration, because a
// repository has one quality bar and splitting it across two files makes the
// bar hard to read. An exemption there also carries the reason it exists,
// which this file had no place for.
type Config struct {
	Template string
	Version  string
	Module   string
	Name     string
	Profile  string
	Features map[string]bool
	// License is the declared terms. A declaration that names none gets
	// DefaultLicense and DefaultHolder.
	License License
	Waivers []Waiver
}

// Terms returns the declared license with the defaults applied to what the
// declaration left out.
func (c *Config) Terms() License {
	l := c.License
	if l.SPDX == "" {
		l.SPDX = DefaultLicense
	}
	if l.Holder == "" {
		l.Holder = DefaultHolder
	}
	return l
}

var (
	versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.\-]+)?$`)
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	modulePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/\-]*$`)
)

// LoadConfig reads and validates the declaration at dir/.template.yaml.
func LoadConfig(dir string) (*Config, error) {
	path := filepath.Join(dir, ConfigFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ConfigFile, err)
	}
	return ParseConfig(ConfigFile, data)
}

// ParseConfig parses a declaration and applies every validation rule. The file
// name is used only in error messages.
func ParseConfig(file string, data []byte) (*Config, error) {
	root, err := parseYAML(file, data)
	if err != nil {
		return nil, err
	}
	if root.kind != kindMapping {
		return nil, errAt(file, 1, "the declaration must be a mapping of fields")
	}
	cfg := &Config{Features: map[string]bool{}}
	for _, key := range root.keys {
		switch key {
		case "template", "version", "module", "name", "profile", "features", "license", "waivers":
		default:
			return nil, errAt(file, root.get(key).line, "unknown field %q", key)
		}
	}
	if cfg.Template, err = root.scalar(file, "template"); err != nil {
		return nil, err
	}
	if cfg.Template == "" {
		cfg.Template = DefaultTemplate
	}
	if cfg.Version, err = root.scalar(file, "version"); err != nil {
		return nil, err
	}
	if cfg.Module, err = root.scalar(file, "module"); err != nil {
		return nil, err
	}
	if cfg.Name, err = root.scalar(file, "name"); err != nil {
		return nil, err
	}
	if cfg.Profile, err = root.scalar(file, "profile"); err != nil {
		return nil, err
	}
	if err := readFeatures(file, root, cfg); err != nil {
		return nil, err
	}
	if err := readLicense(file, root, cfg); err != nil {
		return nil, err
	}
	if err := readWaivers(file, root, cfg); err != nil {
		return nil, err
	}
	if err := cfg.Validate(file); err != nil {
		return nil, err
	}
	return cfg, nil
}

func readFeatures(file string, root *node, cfg *Config) error {
	f := root.get("features")
	if f == nil {
		return nil
	}
	if f.kind != kindMapping {
		return errAt(file, f.line, "\"features\" must be a mapping of flag names to true or false")
	}
	for _, key := range f.keys {
		v, err := f.boolean(file, key)
		if err != nil {
			return err
		}
		cfg.Features[key] = v
	}
	return nil
}

// readLicense reads the optional license mapping. A declaration written
// before the field existed names none and gets the defaults, so it parses
// unchanged.
func readLicense(file string, root *node, cfg *Config) error {
	l := root.get("license")
	if l == nil {
		return nil
	}
	if l.kind != kindMapping {
		return errAt(file, l.line, "\"license\" must be a mapping with spdx, holder, and year")
	}
	for _, key := range l.keys {
		switch key {
		case "spdx", "holder", "year":
		default:
			return errAt(file, l.get(key).line, "unknown license field %q, the fields are spdx, holder, and year", key)
		}
	}
	var err error
	if cfg.License.SPDX, err = l.scalar(file, "spdx"); err != nil {
		return err
	}
	if cfg.License.Holder, err = l.scalar(file, "holder"); err != nil {
		return err
	}
	if cfg.License.Year, err = l.scalar(file, "year"); err != nil {
		return err
	}
	return nil
}

func readWaivers(file string, root *node, cfg *Config) error {
	w := root.get("waivers")
	if w == nil {
		return nil
	}
	if w.kind != kindSequence {
		return errAt(file, w.line, "\"waivers\" must be a list")
	}
	for _, item := range w.items {
		if item.kind != kindMapping {
			return errAt(file, item.line, "each waiver must hold path, reason, and expires")
		}
		path, err := item.scalar(file, "path")
		if err != nil {
			return err
		}
		reason, err := item.scalar(file, "reason")
		if err != nil {
			return err
		}
		expires, err := item.scalar(file, "expires")
		if err != nil {
			return err
		}
		if path == "" {
			return errAt(file, item.line, "a waiver needs a path")
		}
		if strings.TrimSpace(reason) == "" {
			return errAt(file, item.line, "the waiver for %q needs a reason", path)
		}
		if expires == "" {
			return errAt(file, item.line, "the waiver for %q needs an expiry date as YYYY-MM-DD", path)
		}
		when, perr := time.Parse("2006-01-02", strings.TrimSpace(expires))
		if perr != nil {
			return errAt(file, item.line, "the waiver for %q has an expiry that is not YYYY-MM-DD: %q", path, expires)
		}
		cfg.Waivers = append(cfg.Waivers, Waiver{Path: path, Reason: reason, Expires: when})
	}
	return nil
}

// Validate applies every rule that does not need the skeleton: field shapes,
// the profile name, and the profile-to-feature relationship.
func (c *Config) Validate(file string) error {
	if !versionPattern.MatchString(c.Version) {
		return errAt(file, 0, "\"version\" must be a semantic version tag such as v1.4.0, found %q", c.Version)
	}
	if c.Module == "" || !modulePattern.MatchString(c.Module) {
		return errAt(file, 0, "\"module\" must be a module path such as github.com/acme/widget, found %q", c.Module)
	}
	if !namePattern.MatchString(c.Name) {
		return errAt(file, 0, "\"name\" must be lower case letters, digits, and hyphens, found %q", c.Name)
	}
	supported, ok := profileFeatures[c.Profile]
	if !ok {
		return errAt(file, 0, "\"profile\" must be one of %s, found %q", strings.Join(AllProfiles, ", "), c.Profile)
	}
	names := make([]string, 0, len(c.Features))
	for k := range c.Features {
		names = append(names, k)
	}
	sort.Strings(names)
	known := map[string]bool{}
	for _, f := range AllFeatures {
		known[f] = true
	}
	for _, n := range names {
		if !known[n] {
			return errAt(file, 0, "unknown feature flag %q, the flags are %s", n, strings.Join(AllFeatures, ", "))
		}
		if c.Features[n] && !supported[n] {
			return errAt(file, 0, "the %s profile does not support the %q feature flag", c.Profile, n)
		}
	}
	if c.Profile == ProfileFrontendOnly {
		if v, set := c.Features[FeatureFrontend]; set && !v {
			return errAt(file, 0, "the frontend-only profile always builds a frontend, so the %q flag cannot be false", FeatureFrontend)
		}
		c.Features[FeatureFrontend] = true
	}
	for _, dependent := range []string{FeatureSEO, FeatureI18n} {
		if c.Features[dependent] && !c.Features[FeatureFrontend] {
			return errAt(file, 0, "the %q feature flag needs the %q flag", dependent, FeatureFrontend)
		}
	}
	terms := c.Terms()
	if !slices.Contains(Licenses, terms.SPDX) {
		return errAt(file, 0, "\"license.spdx\" must be one of %s, found %q; the license gate checks LICENSE against no other",
			strings.Join(Licenses, ", "), terms.SPDX)
	}
	if !holderPattern.MatchString(terms.Holder) {
		return errAt(file, 0, "\"license.holder\" must be a plain name of letters, digits, spaces, and .,&()'- found %q",
			terms.Holder)
	}
	if terms.Year != "" && !yearPattern.MatchString(terms.Year) {
		return errAt(file, 0, "\"license.year\" must be a four digit year such as 2026, found %q", terms.Year)
	}
	return nil
}

// Enabled reports whether a feature flag is on.
func (c *Config) Enabled(feature string) bool { return c.Features[feature] }

// WaiverFor returns the waiver covering a generated path, if any.
func (c *Config) WaiverFor(path string) (Waiver, bool) {
	for _, w := range c.Waivers {
		if w.Path == path {
			return w, true
		}
	}
	return Waiver{}, false
}

// Marshal renders the declaration in the canonical field order. The generator
// writes this at init; afterwards the file is consumer-owned text and only the
// version line is rewritten.
func (c *Config) Marshal() []byte {
	var b strings.Builder
	b.WriteString("# The template declaration. Only \"version\" is rewritten by the generator, and\n")
	b.WriteString("# \"license.year\" is recorded once by an upgrade that finds none.\n")
	fmt.Fprintf(&b, "template: %s\n", c.Template)
	fmt.Fprintf(&b, "version: %s\n", c.Version)
	fmt.Fprintf(&b, "module: %s\n", c.Module)
	fmt.Fprintf(&b, "name: %s\n", c.Name)
	fmt.Fprintf(&b, "profile: %s\n", c.Profile)
	b.WriteString("features:\n")
	for _, f := range AllFeatures {
		fmt.Fprintf(&b, "  %s: %t\n", f, c.Features[f])
	}
	terms := c.Terms()
	b.WriteString("license:\n")
	fmt.Fprintf(&b, "  spdx: %s\n", terms.SPDX)
	fmt.Fprintf(&b, "  holder: %s\n", terms.Holder)
	if terms.Year != "" {
		fmt.Fprintf(&b, "  year: %s\n", terms.Year)
	}
	if len(c.Waivers) == 0 {
		b.WriteString("waivers: []\n")
	} else {
		b.WriteString("waivers:\n")
		for _, w := range c.Waivers {
			fmt.Fprintf(&b, "  - path: %s\n", w.Path)
			fmt.Fprintf(&b, "    reason: %s\n", w.Reason)
			fmt.Fprintf(&b, "    expires: %s\n", w.Expires.Format("2006-01-02"))
		}
	}
	return []byte(b.String())
}

// SetVersionLine rewrites the version field of a declaration in place, leaving
// every other byte of the file untouched. The declaration is consumer-owned
// text with comments and ordering worth preserving, so upgrade edits the one
// line it owns rather than re-emitting the document.
func SetVersionLine(data []byte, version string) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	found := -1
	for i, l := range lines {
		trimmed := stripComment(l)
		if key, _, ok := splitKey(strings.TrimLeft(trimmed, " ")); ok && key == "version" &&
			len(l)-len(strings.TrimLeft(l, " ")) == 0 {
			if found >= 0 {
				return nil, fmt.Errorf("%s holds more than one version field", ConfigFile)
			}
			found = i
		}
	}
	if found < 0 {
		return nil, fmt.Errorf("%s holds no version field", ConfigFile)
	}
	comment := ""
	if idx := strings.Index(lines[found], "#"); idx >= 0 && stripComment(lines[found]) != lines[found] {
		comment = " " + strings.TrimSpace(lines[found][idx:])
	}
	lines[found] = "version: " + version + comment
	return []byte(strings.Join(lines, "\n")), nil
}

// SetLicenseYear records the year every license notice names in a declaration
// that holds none, leaving every other byte of the file untouched, and
// reports whether it changed anything. A declaration written before the
// license field existed gets the whole block, spelled out with the defaults
// it was already released under; one with a license block and no year gets
// the year line appended to the block. A declared year is never replaced:
// the year of first publication does not move.
func SetLicenseYear(data []byte, year string) ([]byte, bool, error) {
	if !yearPattern.MatchString(year) {
		return nil, false, fmt.Errorf("the license year must be a four digit year such as 2026, found %q", year)
	}
	lines := strings.Split(string(data), "\n")
	start := -1
	for i, l := range lines {
		if key, _, ok := splitKey(stripComment(l)); ok && key == "license" && !strings.HasPrefix(l, " ") {
			start = i
			break
		}
	}
	if start < 0 {
		text := string(data)
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "license:\n" +
			"  spdx: " + DefaultLicense + "\n" +
			"  holder: " + DefaultHolder + "\n" +
			"  year: " + year + "\n"
		return []byte(text), true, nil
	}
	// The block runs until the next line that is neither indented, blank,
	// nor a comment. The year goes after its last field, at that field's
	// indentation.
	last, indent := start, "  "
	for i := start + 1; i < len(lines); i++ {
		l := lines[i]
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(l, " ") {
			break
		}
		if key, _, ok := splitKey(strings.TrimLeft(stripComment(l), " ")); ok && key == "year" {
			return data, false, nil
		}
		last, indent = i, l[:len(l)-len(strings.TrimLeft(l, " "))]
	}
	if last == start && strings.TrimSpace(stripComment(lines[start])) != "license:" {
		return nil, false, fmt.Errorf("%s writes license on one line; spell it as a block to record the year", ConfigFile)
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:last+1]...)
	out = append(out, indent+"year: "+year)
	out = append(out, lines[last+1:]...)
	return []byte(strings.Join(out, "\n")), true, nil
}
