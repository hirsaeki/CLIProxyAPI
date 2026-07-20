package executor

import "testing"

func TestVertexBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		location string
		want     string
	}{
		{name: "default", location: "", want: "https://us-central1-aiplatform.googleapis.com"},
		{name: "global", location: "global", want: "https://aiplatform.googleapis.com"},
		{name: "US multi-region", location: "us", want: "https://aiplatform.us.rep.googleapis.com"},
		{name: "EU multi-region", location: "eu", want: "https://aiplatform.eu.rep.googleapis.com"},
		{name: "regular region", location: "europe-west4", want: "https://europe-west4-aiplatform.googleapis.com"},
		{name: "trimmed", location: " us ", want: "https://aiplatform.us.rep.googleapis.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vertexBaseURL(tt.location); got != tt.want {
				t.Fatalf("vertexBaseURL(%q) = %q, want %q", tt.location, got, tt.want)
			}
		})
	}
}
