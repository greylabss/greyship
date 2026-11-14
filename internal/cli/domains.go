package cli

import (
	"errors"
	"fmt"
	"net/url"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

type domainRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type domainEntry struct {
	Hostname string         `json:"hostname"`
	Service  string         `json:"service"`
	Custom   bool           `json:"custom"`
	DNS      string         `json:"dns"`
	Records  []domainRecord `json:"records"`
}

const (
	domainWaitEvery = 5 * time.Second
	domainWaitFor   = 10 * time.Minute
)

func printRecords(a *App, d domainEntry) {
	if len(d.Records) == 0 {
		return
	}
	fmt.Fprintln(a.Out, "Create this DNS record at your DNS provider:")
	tw := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "  TYPE\tNAME\tVALUE\t")
	for _, r := range d.Records {
		value := r.Value
		if value == "" {
			value = "(this server's IP address)"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t\n", r.Type, r.Name, value)
	}
	_ = tw.Flush()
}

var dnsText = map[string]string{
	"pointing":     "points here ✓",
	"not_pointing": "points somewhere else",
	"unresolved":   "no DNS record yet",
	"unknown":      "can't tell",
}

func domainsCmd(a *App, asJSON *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domains",
		Short: "List this project's addresses, including custom domains",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, _, err := a.linkedProject()
			if err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			var out struct{ Domains []domainEntry }
			if err := s.api.do(cmd.Context(), "GET", "/v1/projects/"+url.PathEscape(l.ID)+"/domains", nil, &out); err != nil {
				return friendly(err)
			}
			if *asJSON {
				return a.printJSON(out.Domains)
			}
			if len(out.Domains) == 0 {
				fmt.Fprintln(a.Out, "No addresses yet: deploy a web service first.")
				return nil
			}
			tw := tabwriter.NewWriter(a.Out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ADDRESS\tSERVICE\tDNS\t")
			for _, d := range out.Domains {
				dns := "default"
				if d.Custom {
					dns = dnsText[d.DNS]
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t\n", d.Hostname, d.Service, dns)
			}
			return tw.Flush()
		},
	}
	var service string
	add := &cobra.Command{
		Use:   "add <hostname>",
		Short: "Serve a service on your own domain",
		Long: "Points a hostname at one of this project's web services. Create the DNS record yourself: a CNAME to the " +
			"service's default address (or an A record to the server's IP for a name without a subdomain). HTTPS is set up " +
			"automatically the first time the name is visited, once DNS answers. Say --service when the project has several.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, _, err := a.linkedProject()
			if err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			var out struct{ Domain domainEntry }
			body := map[string]string{"hostname": args[0], "service": service}
			if err := s.api.do(cmd.Context(), "POST", "/v1/projects/"+url.PathEscape(l.ID)+"/domains", body, &out); err != nil {
				return friendly(err)
			}
			if *asJSON {
				return a.printJSON(out.Domain)
			}
			fmt.Fprintf(a.Out, "✓ %s now serves %s (DNS: %s).\n", out.Domain.Hostname, out.Domain.Service, dnsText[out.Domain.DNS])
			if out.Domain.DNS != "pointing" {
				printRecords(a, out.Domain)
				fmt.Fprintf(a.Out, "Then run: grey domains verify %s --wait\n", out.Domain.Hostname)
			}
			return nil
		},
	}
	add.Flags().StringVar(&service, "service", "", "the web service, when the project has several")
	remove := &cobra.Command{
		Use:     "remove <hostname>",
		Aliases: []string{"rm"},
		Short:   "Stop serving a custom domain",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, _, err := a.linkedProject()
			if err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			if err := s.api.do(cmd.Context(), "DELETE", "/v1/projects/"+url.PathEscape(l.ID)+"/domains/"+url.PathEscape(args[0]), nil, nil); err != nil {
				return friendly(err)
			}
			if *asJSON {
				return a.printJSON(map[string]any{"removed": args[0]})
			}
			fmt.Fprintf(a.Out, "✓ %s removed.\n", args[0])
			return nil
		},
	}
	var wait bool
	verify := &cobra.Command{
		Use:   "verify <hostname>",
		Short: "Check a custom domain's DNS again",
		Long: "Looks up the domain's DNS record and says whether it points at this Greyship. With --wait it keeps checking " +
			"until it does (up to 10 minutes), like the dashboard. The first time it points here, HTTPS is prepared for it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, _, err := a.linkedProject()
			if err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			path := "/v1/projects/" + url.PathEscape(l.ID) + "/domains/" + url.PathEscape(args[0]) + "/verify"
			deadline := time.Now().Add(domainWaitFor)
			for {
				var out struct{ Domain domainEntry }
				if err := s.api.do(cmd.Context(), "POST", path, nil, &out); err != nil {
					return friendly(err)
				}
				if out.Domain.DNS == "pointing" || !wait || time.Now().After(deadline) {
					if *asJSON {
						return a.printJSON(out.Domain)
					}
					fmt.Fprintf(a.Out, "%s: %s\n", out.Domain.Hostname, dnsText[out.Domain.DNS])
					if out.Domain.DNS != "pointing" {
						printRecords(a, out.Domain)
					}
					return nil
				}
				if err := a.Sleep(cmd.Context(), domainWaitEvery); err != nil {
					return err
				}
			}
		},
	}
	verify.Flags().BoolVar(&wait, "wait", false, "keep checking until the domain points here")
	connect := &cobra.Command{
		Use:   "connect <hostname>",
		Short: "Print a link that adds the DNS record for you, when your DNS provider supports it",
		Long: "For a domain on Cloudflare, prints a link that opens Cloudflare and adds the record after you approve it. " +
			"Not every domain can do this; then add the record from grey domains add by hand.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, _, err := a.linkedProject()
			if err != nil {
				return err
			}
			s, err := a.session()
			if err != nil {
				return err
			}
			var out struct{ Provider, URL string }
			err = s.api.do(cmd.Context(), "GET", "/v1/projects/"+url.PathEscape(l.ID)+"/domains/"+url.PathEscape(args[0])+"/connect", nil, &out)
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.Code == "no_auto_setup" {
				return fmt.Errorf("%s can't be set up in one click; create the record by hand (grey domains add shows it)", args[0])
			}
			if err != nil {
				return friendly(err)
			}
			if *asJSON {
				return a.printJSON(out)
			}
			fmt.Fprintf(a.Out, "Open this link, sign in to %s and approve:\n%s\n", out.Provider, out.URL)
			return nil
		},
	}
	cmd.AddCommand(add, remove, verify, connect)
	return cmd
}
