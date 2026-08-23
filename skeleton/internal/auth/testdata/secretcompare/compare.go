// Package fixture is the input of the secret comparison gate's own test. It
// holds deliberate violations, so the scanner is proved to report them, next
// to the forms that are allowed. It lives under testdata and is never built.
package fixture

import "crypto/hmac"

func compare(presented, expected string, sig, want []byte, apiKey, presentedSignature string) bool {
	if presented == "" {
		// Allowed: an emptiness check reveals nothing about the value.
		return false
	}
	if len(sig) == 0 {
		// Allowed: a length check on the raw bytes.
		return false
	}
	if !hmac.Equal(sig, want) {
		// Allowed: a constant time comparison.
		return false
	}
	token := presented
	if token == expected {
		// Flagged: a variable length comparison of a credential.
		return true
	}
	if apiKey != expected {
		// Flagged: the same defect written as an inequality.
		return false
	}
	if string(want) == presentedSignature {
		// Flagged: the secret is on the right of the operator.
		return true
	}
	return false
}
