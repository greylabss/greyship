package domains

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"greyship.tech/internal/project"
	"greyship.tech/internal/store"
)

// ErrNoAutoSetup means one-click DNS setup isn't available for this name: its DNS isn't on a supported provider, or
// the provider doesn't have Greyship's template yet.
var ErrNoAutoSetup = errors.New("one-click DNS setup isn't available for this domain")

// Link opens the DNS provider's page that adds the records (Domain Connect, decisions.md 059).
type Link struct {
	Provider string
	URL      string
}

// Domain Connect template ids (deploy/domainconnect). Each is registered under the provider id greyship.tech.
const (
	connectProvider = "greyship.tech"
	templateCNAME   = "custom-domain-cname"
	templateA       = "custom-domain-a"
)

type connectSettings struct {
	ProviderName string `json:"providerName"`
	URLSyncUX    string `json:"urlSyncUX"`
	URLAPI       string `json:"urlAPI"`
}

// Connect finds out whether the DNS provider of a hostname can add its records in one click, and returns the link.
// The settings address comes from a TXT record the domain's owner controls, so it is only followed when it is a
// Cloudflare https address; anything else is refused before a request is made.
func (s *Service) Connect(ctx context.Context, c project.Caller, projectID, rawHostname string) (Link, error) {
	p, err := s.Projects.Access(ctx, c, projectID, true)
	if err != nil {
		return Link{}, err
	}
	hostname := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(rawHostname)), ".")
	rows, err := store.New(s.Pool).ListProjectDomains(ctx, p.ID)
	if err != nil {
		return Link{}, err
	}
	for _, r := range rows {
		if r.Hostname == hostname {
			return s.connectLink(ctx, hostname, r.Subdomain+"."+s.AppsDomain)
		}
	}
	return Link{}, ErrNotFound
}

func (s *Service) connectLink(ctx context.Context, hostname, defaultAddr string) (Link, error) {
	zone := s.zoneOf(ctx, hostname)
	txt, err := s.Resolver.LookupTXT(ctx, "_domainconnect."+zone)
	if err != nil || len(txt) == 0 {
		return Link{}, ErrNoAutoSetup
	}
	settingsHost, settingsPath, _ := strings.Cut(strings.TrimSpace(txt[0]), "/")
	if !s.connectHostOK(settingsHost) {
		return Link{}, ErrNoAutoSetup
	}
	var settings connectSettings
	if err := s.getJSON(ctx, "https://"+settingsHost+"/"+strings.Trim(settingsPath, "/")+"/v2/"+url.PathEscape(zone)+"/settings", &settings); err != nil {
		return Link{}, ErrNoAutoSetup
	}
	syncUX, errUX := url.Parse(settings.URLSyncUX)
	api, errAPI := url.Parse(settings.URLAPI)
	if errUX != nil || errAPI != nil || syncUX.Scheme != "https" || api.Scheme != "https" || !s.connectHostOK(syncUX.Host) || !s.connectHostOK(api.Host) {
		return Link{}, ErrNoAutoSetup
	}
	server := s.serverAddrs(ctx)
	records := recordsFor(hostname, zone, defaultAddr, server)
	if len(records) == 0 || records[0].Value == "" {
		return Link{}, ErrNoAutoSetup
	}
	template, params := templateCNAME, url.Values{"domain": {zone}, "target": {records[0].Value}}
	if records[0].Type == "CNAME" {
		params.Set("host", records[0].Name)
	} else {
		template = templateA
		params.Set("ip", records[0].Value)
		params.Del("target")
	}
	service := "/v2/domainTemplates/providers/" + connectProvider + "/services/" + template
	if err := s.getJSON(ctx, strings.TrimSuffix(api.String(), "/")+service, new(json.RawMessage)); err != nil {
		return Link{}, ErrNoAutoSetup
	}
	return Link{Provider: "cloudflare", URL: strings.TrimSuffix(syncUX.String(), "/") + service + "/apply?" + params.Encode()}, nil
}

func (s *Service) connectHostOK(host string) bool {
	if len(s.ConnectHosts) > 0 {
		return slices.Contains(s.ConnectHosts, host)
	}
	return host == "cloudflare.com" || strings.HasSuffix(host, ".cloudflare.com")
}

// getJSON does one GET with a short timeout, no redirects and a small body limit.
func (s *Service) getJSON(ctx context.Context, rawURL string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	}
	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noRedirect.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", req.URL.Host, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(into)
}

// TLSWarmer starts a TLS handshake with the router at addr for a hostname. The router answers by getting a
// certificate for the name if it serves it, which is all that is needed; nothing is sent after the handshake.
func TLSWarmer(addr string) func(ctx context.Context, hostname string) error {
	return func(ctx context.Context, hostname string) error {
		d := tls.Dialer{
			NetDialer: &net.Dialer{Timeout: 5 * time.Second},
			// The certificate isn't checked: the handshake only makes the router obtain it.
			Config: &tls.Config{ServerName: hostname, InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec // see above
		}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("warm certificate for %s: %w", hostname, err)
		}
		return conn.Close()
	}
}
