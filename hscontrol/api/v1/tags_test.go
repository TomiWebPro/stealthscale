package apiv1

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateTag pins the well-formedness rules shared by the node
// setTags and preauthkey create surfaces (hscontrol/api/v1/tags.go).
// The exact messages matter: problems are marshalled into the HTTP
// response verbatim and surfaced by CLI clients and the Web UI.
func TestValidateTag(t *testing.T) {
	tests := []struct {
		name    string
		tag     string
		errText string
	}{
		{
			name:    "valid",
			tag:     "tag:server",
			errText: "",
		},
		{
			name:    "valid with digits dashes and dots",
			tag:     "tag:web-01.k8s",
			errText: "",
		},
		{
			name:    "missing prefix",
			tag:     "server",
			errText: "tag must start with the string 'tag:'",
		},
		{
			name:    "prefix is case sensitive",
			tag:     "TAG:server",
			errText: "tag must start with the string 'tag:'",
		},
		{
			name:    "not lowercase",
			tag:     "tag:Web",
			errText: "tag should be lowercase",
		},
		{
			name:    "embedded space",
			tag:     "tag:web server",
			errText: "tags must not contain spaces",
		},
		{
			name:    "double space",
			tag:     "tag:web  server",
			errText: "tags must not contain spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTag(tt.tag)

			if tt.errText == "" {
				assert.NoError(t, err, "tag %q must validate", tt.tag)

				return
			}

			assert.EqualError(t, err, tt.errText, "tag %q", tt.tag)
		})
	}
}
