package config

import "log/slog"

// Redacted is the placeholder every rendering of a Secret produces.
const Redacted = "[redacted]"

// Secret is a configuration value that must never reach a log, an error
// message, or a serialized document.
//
// Redaction is a property of the type rather than a rule each call site
// remembers: String, MarshalJSON, and LogValue all return the placeholder, so
// the value survives fmt verbs that honor Stringer, encoding/json, and
// log/slog. The underlying text is reachable only through Reveal, which makes
// every real use greppable and visible in review.
//
// Two paths still bypass the type, and neither is a defect of this code: the
// %#v verb prints the struct literal, and an explicit conversion such as
// string(s) produces an ordinary string that carries no methods.
type Secret string

// String reports the placeholder. It satisfies fmt.Stringer, so %v, %s, and %q
// render a Secret redacted.
func (s Secret) String() string { return Redacted }

// GoString reports the placeholder for the %#v verb, which ignores String.
func (s Secret) GoString() string { return Redacted }

// MarshalJSON reports the placeholder, so a Secret inside a marshalled struct
// carries no value.
func (s Secret) MarshalJSON() ([]byte, error) {
	return []byte(`"` + Redacted + `"`), nil
}

// MarshalText reports the placeholder. encoding/json and any other text-based
// encoder that prefers TextMarshaler reaches this instead of the raw string.
func (s Secret) MarshalText() ([]byte, error) { return []byte(Redacted), nil }

// UnmarshalText accepts the raw value. The loader uses it, so a Secret field
// parses like any other declared field.
func (s *Secret) UnmarshalText(text []byte) error {
	*s = Secret(text)
	return nil
}

// LogValue reports the placeholder, so slog renders a Secret redacted whether
// it is logged directly or reached through a struct that groups it.
func (s Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// Reveal returns the underlying value. Every caller is a deliberate use of the
// secret, which is why the accessor is explicit rather than a conversion.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether the secret is unset. It answers "is this configured"
// without revealing the value.
func (s Secret) IsZero() bool { return s == "" }
