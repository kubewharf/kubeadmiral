/*
This file is lifted from https://github.com/distribution/reference/blob/main/regexp.go
*/

package reference

import (
	"regexp"
	"strings"
)

const (
	// tag matches valid tag names. From docker/docker:graph/tags.go.
	tag = `[\w][\w.-]{0,127}`

	// digestPat matches well-formed digests, including algorithm (e.g. "sha256:<encoded>").
	//
	// TODO this should follow the same rules as https://pkg.go.dev/github.com/opencontainers/go-digest@v1.0.0#DigestRegexp
	// so that go-digest defines the canonical format. Note that the go-digest is
	// more relaxed:
	//   - it allows multiple algorithms (e.g. "sha256+b64:<encoded>") to allow
	//     future expansion of supported algorithms.
	//   - it allows the "<encoded>" value to use urlsafe base64 encoding as defined
	//     in [rfc4648, section 5].
	//
	// [rfc4648, section 5]: https://www.rfc-editor.org/rfc/rfc4648#section-5.
	digestPat = `[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*[:][[:xdigit:]]{32,}`
)

var (
	// AnchoredTagRegexp matches valid tag names, anchored at the start and
	// end of the matched string.
	AnchoredTagRegexp = regexp.MustCompile(anchored(tag))

	// AnchoredDigestRegexp matches valid digests, anchored at the start and
	// end of the matched string.
	AnchoredDigestRegexp = regexp.MustCompile(anchored(digestPat))
)

// anchored anchors the regular expression by adding start and end delimiters.
func anchored(res ...string) string {
	return `^` + strings.Join(res, "") + `$`
}
