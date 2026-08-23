package httpx

import (
	"crypto/rand"
	"encoding/binary"
	"net/http"
	"time"
)

// HeaderRequestID carries the request identifier in both directions. An
// inbound value is adopted so a caller can join its own logs to this service's.
const HeaderRequestID = "X-Request-Id"

// requestIDPrefix marks the identifier as this service's, so a value copied
// into a support ticket is recognisable.
const requestIDPrefix = "req_"

// maxInboundRequestID bounds an adopted identifier. An unbounded value would
// be copied into every log record and every error body of the request.
const maxInboundRequestID = 64

// crockford is the base32 alphabet with the letters that read as digits
// removed, so an identifier survives being read aloud or retyped.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// AssignRequestID gives every request an identifier and echoes it in the
// response. It sits directly inside recovery so that every later stage, the
// access log and the error envelope included, can name the request.
func AssignRequestID() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := sanitizeRequestID(r.Header.Get(HeaderRequestID))
			if id == "" {
				id = NewRequestID()
			}
			w.Header().Set(HeaderRequestID, id)

			ctx, state := withState(r.Context())
			state.setRequestID(id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// sanitizeRequestID accepts an inbound identifier only when it is short and
// printable ASCII. The value is echoed in a response header and written to
// logs, so a control character in it is a header-splitting and log-forging
// vector.
func sanitizeRequestID(v string) string {
	if v == "" || len(v) > maxInboundRequestID {
		return ""
	}
	for i := range len(v) {
		c := v[i]
		if c < 0x21 || c > 0x7e {
			return ""
		}
	}
	return v
}

// NewRequestID returns a fresh identifier. The first six bytes are the
// millisecond timestamp, so identifiers sort by creation time, and the
// remaining ten are random, so two processes never collide.
func NewRequestID() string {
	var raw [16]byte
	binary.BigEndian.PutUint64(raw[:8], uint64(time.Now().UnixMilli()))
	// The timestamp occupies six bytes; shift it into place and fill the rest.
	copy(raw[0:6], raw[2:8])
	// crypto/rand.Read never returns an error; the signature keeps the
	// io.Reader shape.
	_, _ = rand.Read(raw[6:])
	return requestIDPrefix + encodeCrockford(raw[:])
}

// encodeCrockford renders the bytes as base32 without padding.
func encodeCrockford(b []byte) string {
	out := make([]byte, 0, (len(b)*8+4)/5)
	var acc, bits uint32
	for _, c := range b {
		acc = acc<<8 | uint32(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, crockford[(acc>>bits)&0x1f])
		}
	}
	if bits > 0 {
		out = append(out, crockford[(acc<<(5-bits))&0x1f])
	}
	return string(out)
}
