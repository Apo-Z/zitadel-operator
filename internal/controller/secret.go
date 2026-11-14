package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"text/template"
)

// SecretData is the data available to the Secret templates.
type SecretData struct {
	ClientID       string
	ClientSecret   string
	KeyJSON        string
	Issuer         string
	DiscoveryURL   string
	ProjectID      string
	AppID          string
	OrganizationID string
	AppName        string
}

// AnnotationConfigHash stores the hash of the non-secret inputs used to render
// a Secret. When it changes the credentials are regenerated.
const AnnotationConfigHash = "zitadel.zitadel-operator.io/config-hash"

// renderSecret renders the Secret data from the template (or the default keys).
func renderSecret(tmpl map[string]string, d SecretData) (map[string][]byte, error) {
	out := map[string][]byte{}
	if len(tmpl) == 0 {
		out["CLIENT_ID"] = []byte(d.ClientID)
		out["ISSUER"] = []byte(d.Issuer)
		out["DISCOVERY_URL"] = []byte(d.DiscoveryURL)
		out["PROJECT_ID"] = []byte(d.ProjectID)
		out["APP_ID"] = []byte(d.AppID)
		out["ORGANIZATION_ID"] = []byte(d.OrganizationID)
		if d.ClientSecret != "" {
			out["CLIENT_SECRET"] = []byte(d.ClientSecret)
		}
		if d.KeyJSON != "" {
			out["key.json"] = []byte(d.KeyJSON)
		}
		return out, nil
	}
	for key, text := range tmpl {
		t, err := template.New(key).Option("missingkey=error").Parse(text)
		if err != nil {
			return nil, fmt.Errorf("parsing template for key %q: %w", key, err)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, d); err != nil {
			return nil, fmt.Errorf("rendering template for key %q: %w", key, err)
		}
		out[key] = buf.Bytes()
	}
	return out, nil
}

// validateTemplate checks that the templates parse and execute.
func validateTemplate(tmpl map[string]string) error {
	_, err := renderSecret(tmpl, SecretData{})
	return err
}

// configHash hashes the non-secret inputs of the Secret.
func configHash(tmpl map[string]string, d SecretData, authMethod, rotate string) string {
	d.ClientSecret, d.KeyJSON = "", ""
	payload := struct {
		Template   map[string]string
		Data       SecretData
		AuthMethod string
		Rotate     string
	}{maps.Clone(tmpl), d, authMethod, rotate}
	b, _ := json.Marshal(payload) // maps are marshalled with sorted keys
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
