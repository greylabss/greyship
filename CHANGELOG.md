# Changelog

All notable changes to Greyship, also at [greyship.tech/changelog](https://greyship.tech/changelog). Releases and
their downloads are on [GitHub](https://github.com/greylabss/greyship/releases). Versions before v0.1.8 are listed
there only.

## v0.1.8 — 2026-11-14

### Added

- **Domains page shows the DNS records to create.** Each custom domain has a status (Verification required, Valid
  configuration, or Points somewhere else) and a "View DNS configuration" panel with the exact record, type, name and
  value, each with a copy button. A subdomain gets a CNAME to the service's default address; a bare domain such as
  `example.com` gets an A record to your server.
- **The page keeps checking until the record appears.** While a domain is waiting, the dashboard re-checks DNS every
  few seconds (slower over time, paused while the tab is hidden, for about ten minutes) and turns the domain to Valid
  configuration by itself. A Refresh button checks right away.
- **HTTPS is ready before the first visitor.** The first time a domain is seen pointing at your server, Greyship asks
  its router for the certificate straight away.
- **Configure Cloudflare.** For a domain on Cloudflare, a button opens Cloudflare and adds the record after you
  approve it (the open Domain Connect standard). It appears once Cloudflare has Greyship's templates, which are in
  `deploy/domainconnect`; until then the records and live checking work on their own.
- **CLI:** `grey domains verify <hostname> [--wait]` checks a domain's DNS, and with `--wait` keeps checking until it
  points here. `grey domains connect <hostname>` prints the Cloudflare link when there is one. `grey domains add` now
  prints the record to create.
- **API:** `POST /v1/projects/{id}/domains/{hostname}/verify`, `GET /v1/projects/{id}/domains/{hostname}/connect`, and
  a `records` list on every custom domain in `GET /v1/projects/{id}/domains`.

- **Installer: newsletter question.** After the owner email, the installer asks whether you want Greyship news by
  email (`--newsletter` / `--no-newsletter` to answer ahead). Only a yes sends your address, and you confirm from your
  inbox before anything is sent.

### Changed

- **Installer: anonymous install counts.** The installer tells greyship.tech when an install starts, succeeds, fails
  (and at which step) or upgrades, with a random id, the version, the OS and the CPU type. No email, hostname or IP
  address is kept. It says so when it starts; `GREYSHIP_NO_TELEMETRY=1` turns it off. See https://greyship.tech/privacy.

- Always-on services are labelled "always on · holds memory 24/7" in the dashboard, and the dashboard's sample-data
  screens no longer show prices or billing.
