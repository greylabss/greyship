package cli

import (
	"fmt"
	"net/url"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

type domainEntry struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Custom   bool   `json:"custom"`
	DNS      string `json:"dns"`
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
				fmt.Fprintf(a.Out, "Point it at this service: add a CNAME from %s to the service's default address (see grey domains).\n", out.Domain.Hostname)
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
	cmd.AddCommand(add, remove)
	return cmd
}
