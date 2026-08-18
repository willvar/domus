package filekind

import (
	"mime"
	"path"
	"strings"
)

const genericBinary = "application/octet-stream"

// ContentType returns a normalized media type for a logical file name. A
// specific, syntactically valid type supplied by the browser wins because
// extensions such as .webm can represent either audio or video. Generic or
// missing declarations fall back to deterministic extension inference for
// browser-independent behavior, including files created through FUSE.
func ContentType(name, declared string) string {
	if normalized := normalize(declared); normalized != "" && normalized != genericBinary {
		return normalized
	}

	switch strings.ToLower(path.Ext(name)) {
	case ".webm":
		return "video/webm"
	}

	return normalize(mime.TypeByExtension(path.Ext(name)))
}

func normalize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(mediaType)
}
