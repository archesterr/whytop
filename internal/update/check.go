package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// Release is one published whytop release.
type Release struct {
	Tag     string
	Notes   string
	URL     string
	Assets  map[string]string // file name -> download URL
	Version string            // the tag without its v, as the archives spell it
}

// latestURL is the GitHub API's own "what is the current release" endpoint.
// It already excludes drafts and pre-releases, so a release candidate is
// never offered to someone running a stable build.
const latestURL = "https://api.github.com/repos/archesterr/whytop/releases/latest"

// BuiltWithoutUpdateCheck is set at build time, through the linker, by
// anyone shipping whytop through a channel that owns updates itself:
//
//	go build -ldflags "-X github.com/archesterr/whytop/internal/update.BuiltWithoutUpdateCheck=1"
//
// Detect() already refuses to replace a packaged binary at runtime, so this
// is belt and braces — but it is the belt a distribution asks for. A package
// in an archive should not contain code paths that reach the network at all,
// and a maintainer should not have to take a runtime check's word for it.
// debian/rules sets this.
var BuiltWithoutUpdateCheck = ""

// Disabled reports whether the operator has turned the update check off.
// Two spellings, because the people who want this off are the people running
// whytop on machines with no route to the internet, and they set it in
// different places: an environment variable for a shell profile, and a flag
// for a systemd unit or an ansible template.
//
// NO_UPDATE_CHECK and the widely-honoured DO_NOT_TRACK are respected too: a
// machine that has declared it does not phone home should not have to
// declare it again per program.
func Disabled() bool {
	if truthy(BuiltWithoutUpdateCheck) {
		return true
	}
	for _, k := range []string{"WHYTOP_NO_UPDATE_CHECK", "NO_UPDATE_CHECK", "DO_NOT_TRACK"} {
		if truthy(os.Getenv(k)) {
			return true
		}
	}
	return false
}

func truthy(v string) bool {
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// Check asks GitHub for the current release. It is deliberately the only
// network call whytop makes, it sends nothing but the request itself, and it
// is given a short deadline: this runs unattended behind a UI, and a hung
// connection must expire rather than accumulate.
func Check(ctx context.Context) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "whytop")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Release{}, errors.New("no published release")
	case resp.StatusCode == http.StatusForbidden:
		// Unauthenticated GitHub API requests are rate limited per address,
		// which a NAT'd office can exhaust between them. Not an error worth
		// showing anybody.
		return Release{}, errors.New("rate limited by GitHub")
	case resp.StatusCode != http.StatusOK:
		return Release{}, fmt.Errorf("github returned %s", resp.Status)
	}

	var body struct {
		TagName    string `json:"tag_name"`
		Body       string `json:"body"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Release{}, err
	}
	if body.Draft || body.Prerelease || body.TagName == "" {
		return Release{}, errors.New("no stable release")
	}

	rel := Release{
		Tag:     body.TagName,
		Notes:   body.Body,
		URL:     body.HTMLURL,
		Version: strings.TrimPrefix(body.TagName, "v"),
		Assets:  make(map[string]string, len(body.Assets)),
	}
	for _, a := range body.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// ArchiveName is the release archive for this machine, named the way
// .goreleaser.yaml's archives.name_template spells it. If that template
// changes, this has to change with it — which is what TestArchiveNameMatches
// GoreleaserTemplate is for.
func (r Release) ArchiveName() string {
	return fmt.Sprintf("whytop_%s_%s_%s.tar.gz", r.Version, runtime.GOOS, runtime.GOARCH)
}
