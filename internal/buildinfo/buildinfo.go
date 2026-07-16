package buildinfo

import (
	"net/url"
	"regexp"
	"strings"
)

var Version = "dev"

var invocationID = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)

func ApplicationName(version, invocation string) string {
	sha := "dev"
	if len(version) == 40 {
		sha = strings.ToLower(version[:12])
	}
	if !invocationID.MatchString(invocation) {
		invocation = "unknown"
	} else {
		invocation = strings.ToLower(invocation)
	}
	return "monera-digital/" + sha + "/" + invocation
}

func DatabaseURL(databaseURL, version, invocation string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("application_name", ApplicationName(version, invocation))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
