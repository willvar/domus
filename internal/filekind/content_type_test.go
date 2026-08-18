package filekind

import "testing"

func TestContentType(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		want     string
	}{
		{name: "movie.webm", declared: "video/webm; codecs=vp8", want: "video/webm"},
		{name: "recording.webm", declared: "audio/webm", want: "audio/webm"},
		{name: "movie.webm", want: "video/webm"},
		{name: "movie.webm", declared: "application/octet-stream", want: "video/webm"},
		{name: "notes.txt", declared: "Text/Plain; charset=utf-8", want: "text/plain"},
		{name: "notes.txt", declared: "not a media type", want: "text/plain"},
	}

	for _, test := range tests {
		t.Run(test.name+"/"+test.declared, func(t *testing.T) {
			if got := ContentType(test.name, test.declared); got != test.want {
				t.Fatalf("ContentType(%q, %q) = %q, want %q", test.name, test.declared, got, test.want)
			}
		})
	}
}
