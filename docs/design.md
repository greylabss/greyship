# Greyship — Dashboard & Web Design Brief

> For the coding agent building `web/`. Phase 1 builds every screen as a **clickable mock** on fake data; phase 2 swaps the fake data for the real `/v1` API. The visual reference is the "Greyship Dashboard" design canvas (15 screens). The canvas predates the anti-slop rules (§6); where they disagree, this file wins — e.g. drop the orange kicker labels above landing sections, break the six-equal-card feature grid into one dominant feature plus a quieter list, and replace top tabs with the sidebar (§2).

---

## 1. Principles

1. **Calm, dense, fast.** People open a deploy dashboard when something matters. Nothing on screen competes with the information.
2. **Status first.** Every project, service, deployment and domain shows its state at a glance — and the *reason* when something is wrong, without opening logs.
3. **Sleeping is a feature, not an error.** A scaled-to-zero service is shown calmly in grey ("Sleeping · wakes on request"; workers: "Sleeping · 0 queued"), never as red or "down". Anything always-on carries an "always on · billed 24/7" badge.
4. **Go-native where it counts.** The Go Runtime tab, runtime tuning and govulncheck results are the screens nobody else has; give them the most care.
5. **CLI and dashboard are equal.** Wherever there's an action, show the matching `grey` command next to it (small, mono, copyable).
6. **No slop.** No gradients, glows, illustrations, emoji, or decorative cards. One exception: the Go gopher, once, beside "About Greyship" on the landing page, credited in the footer (decisions.md 035). Nowhere in the dashboard. Second exception: the landing hero sits on one monochrome halftone sea drawn by a small WebGL shader (decisions.md 036, 061): grey dots on the background colour, no colour gradients, no glow; it pauses off screen and is still under reduced motion. Landing sections may rise in on scroll (CSS scroll-driven, off with reduced motion). The dashboard stays flat. On the public site, prefer ruled lists (numbered rows on hairlines, one live mono detail each) and sticky split sections over rows of cards or bentos; every section shows something real from Greyship. Hairline borders, monochrome, one accent.

---

## 2. Patterns borrowed from other platforms

| Pattern | Seen at | How Greyship uses it |
| --- | --- | --- |
| Resizable, hideable **sidebar** instead of top tabs; same nav at team and project level; **floating bottom bar on mobile** | Vercel | App shell = left sidebar (Projects, Usage & billing; Team added in v1.0 with F-4; inside a project: Overview, Deployments, Logs, Metrics, Go Runtime, Env, Domains, Settings). Collapses to icons; on phones a bottom bar with 4–5 key items. |
| Overview separates **production** from **preview** deployments; deployment list is **dense**, grouped by environment, branch/commit easy to scan | Vercel | Project overview shows the live production deployment on top, previews below. Deployments list: one row each — status dot, message, sha, branch, author, duration, age. |
| **Browser tab icon reflects deploy status** (queued, building, error, ready) | Vercel | Favicon switches to grey/orange/red/green variants of the mark while a deploy page is open. |
| Build + runtime logs **inside the deployment page**, filterable, **one-click copy** | Vercel | Deployment page = details + build log; copy and download buttons; filter by service. |
| **Service map** as the project's main view; nodes show health, **name the failure** (build failed, crashed), **cron nodes show last run result**; alerts appear on the map | Railway | "Map" view toggle on the project overview: one node per service (api, worker, cron…) with state, failure reason, last cron run. List view stays the default. |
| **Scan the repo and choose services** when importing | Railway | Onboarding step 2: "We found 4 services" with types detected from `cmd/*`. |
| **Events drawn on metrics charts** (deploys, instance failures), filterable, clickable | Render | Every chart (requests, latency, goroutines, heap) shows markers for deploys, restarts, wake-ups and crashes. Click a marker → details. |
| **Big logs panel, line detail view, live tail on/off**, theme-aware | Render | Logs page: full width, live tail toggle, click a line → side panel with parsed JSON, trace id, instance. |
| **Plan → review → confirm** for scaling changes instead of applying instantly | Fly.io | Settings › Scaling: edits show a "Review changes" diff (e.g. "max instances 2 → 4, est. cost +$x/mo") before Confirm. |
| **Copy buttons everywhere**, including mobile | Fly.io | Every URL, sha, env key, DNS value and CLI command has a copy button. |
| Domain setup with explicit **warning / valid** states | Vercel | Domains: each domain shows Waiting for DNS / Verifying / HTTPS active, with the exact record to add. |
| Near-real-time data without refreshing | Vercel | Poll or stream status, logs and metrics; the UI updates in place. |

---

## 3. Visual system

| Token | Value | Use |
| --- | --- | --- |
| `bg` | `#0A0A0B` | Page ground |
| `surface` | `#121214` | Inputs, tiles, toolbars |
| `surface-2` | `#0F0F11` | Side panels, code blocks |
| `border` | `#232326` | Hairlines |
| `border-strong` | `#2A2A2E` | Inputs, secondary buttons |
| `text` | `#EDEDED` | Primary text |
| `text-muted` | `#A1A1A6` | Secondary text |
| `text-dim` | `#7C7C82` | Timestamps, hints (≥ 4.5:1 on bg) |
| `accent` | `#F2541B` | Attention only: building, warnings, the logo chevron |
| `ok` | `#3FB97A` | Ready / running |
| `error` | `#E5534B` | Failed / crashed |
| `warn` | `#D9A21B` | Log WARN level |

- **Type:** Geist for UI, Geist Mono for anything technical (URLs, shas, env keys, logs, commands, numbers in tables). Chosen deliberately, see decisions.md 021.
- **Radius:** 6px controls (tags, small toggles), 8px inputs and buttons, 12px panels (see decisions.md 021). **Spacing:** 4px grid.
- **Primary button:** light fill (`text` on `bg`). Secondary: transparent with `border-strong`. Destructive: red outline.
- **Light theme:** later; build tokens as CSS variables from day one so it's a token swap.

---

## 4. Screen inventory (phase 1 mocks)

| Area | Screen | Must include |
| --- | --- | --- |
| Marketing | Landing (`/`) | Short: hero + project canvas + routers strip, a short "About Greyship" paragraph with links to Features, Pricing, But… and the blog, closing CTA. Detail lives on those pages, not here. Before launch (`NEXT_PUBLIC_SITE_MODE` unset or `coming-soon`) the sign-up CTAs become "Read how it's built" → `/blog` and "Apply for the private beta", the install command is hidden, and every dashboard route redirects to `/` |
| Marketing | Features (`/features`) | "Write Go like you always do" (deploy terminal + the three steps, a timeline on phones), "Built around Go" (wake demo + feature list) and "Only on Greyship" (four Go-only items; a swipe rail on phones) |
| Marketing | Pricing (`/pricing`) | Plans with the USD/NGN switch (one plan at a time behind a Free · Pro · Team switch on phones), then "How billing works" questions |
| Marketing | But… (`/but`) | Objections from sceptics (Vercel, Render/Railway, Lambda, Cloud Run, Fly, "just Docker", why only Go, trust, cold starts, cost), in the founder's voice. A jump list, then each answer under its own anchor so one can be linked in a reply. FAQPage JSON-LD |
| Marketing | Blog (`/blog`) | One page, "How Greyship works": intro, then every chapter in reading order at 68ch, then moderated questions and comments (each tagged with a chapter or General). A sticky sidebar (lg+) lists the chapters, highlights the one being read and shows its sections; below lg a sticky bar under the header shows the current chapter and opens the same list. Old `/blog/<slug>` links redirect to `/blog#<slug>` |
| Marketing | Blog admin (`/blog/admin`) | Password login; queue of comments with approve / reject / delete / reply as Greyship. `noindex` |
| Auth | Sign in, Sign up, CLI device authorization | GitHub button, email magic link, device code confirm |
| Onboarding | Import repo → Review & deploy | Repo search, templates, CLI alternative, detected services, env vars, summary |
| Team | Projects home, Usage & billing | Project cards with status + request sparkline; spend cap; naira + dollar payments. Team members (invites, roles) waits for v1.0 (spec F-4) |
| Project | Overview (list + map views), Deployments list, Deployment detail | Production vs preview, dense list, build log, rollback |
| Service | Logs, Metrics, Go Runtime, Env, Domains, Settings | As above; events on charts; scaling review-and-confirm |
| Env (project + service) | Shared variables (project level) and per-service variables | Project page edits shared vars; service page shows the **effective** list with a `shared` / `override` / `service` badge per key, "Override for this service" and "Exclude from this service" actions, and a note of which services restart on save |
| System | 404, error boundary, maintenance | Calm, one sentence, a way back |

Every screen also needs: **loading** (skeletons, not spinners), **empty** (what it is + the one action to take + the CLI command), and **error** states.

---

## 5. Mock-first build rules

- All data comes through one typed client (`web/lib/api`) whose function names and response shapes match the `/v1` OpenAPI spec. Phase 1 implementation returns fixtures; phase 2 replaces the implementation only — no screen changes. As built (step 17): with `GREYSHIP_API_URL` set, `web/lib/api/live.ts` calls the API from server components with the visitor's session cookie and maps the JSON to `types.ts`; without it the fixtures and the dev scenario toggle stay. `available` lists what the API can answer yet; screens check it and render `Arrives` ("This arrives with …") rather than fixture numbers, and actions the API can't do yet are hidden in live mode (`liveMode`) in favour of the matching `grey` command.
- Fixtures live in one folder and cover every state: ready, building, failed, sleeping, crashing, waiting for DNS, empty project, over spend cap.
- A dev-only toggle switches fixture scenarios so each state can be reviewed.
- Navigation works end to end with mock data, so the whole flow can be clicked through before the backend exists.
- Interactive primitives (menus, dialogs, select, tooltips, toggle groups, command palette) are shadcn/Radix components in `web/components/primitives/`, restyled to §3 tokens (decisions.md 022).
- Keep components small and shared: `StatusDot`, `CopyButton`, `CommandHint`, `DataTable`, `LogLine`, `Chart` (with event markers), `EmptyState`, `Sidebar`, `PageHeader`.

---

## 6. Anti-slop rules

AI-built UI looks generic when the model fills undecided choices with the statistical average of the web. These rules take those choices away. **If a value or pattern isn't in this file, ask before inventing it.**

### 6.1 Banned outright
| Tell | Do instead |
| --- | --- |
| Purple/indigo/blue gradients, gradient text, gradient buttons | Flat colours from §3 only. Zero gradients anywhere. |
| Glassmorphism, blur panels, glows, neon shadows, blobs, grain, "aurora" backgrounds | Flat surfaces and 1px borders. Depth only where it means something (menus, modals). |
| Emoji in UI, sparkles ✨, rocket/robot illustrations, 3D icons, stock photos | Text, data and the product's own output (terminal, logs, charts) are the visuals. |
| Icon above every heading; rows of identical icon cards | No decorative icons. Icons for actions, status and technical metadata (GitHub repo, branch, commit, docs), 16px, one set. Projects get a two-letter tile until they can upload an icon. |
| Everything in large rounded cards with the same soft shadow; cards inside cards | Rows, tables and dividers for lists. Panels only to group a real unit. Radius by hierarchy: 6px controls, 8px inputs/buttons, 12px panels. No shadows except overlays. |
| Fade/slide-in on every section, bouncy easing, count-up numbers, hover lifts | No scroll animations in the app. Motion only in response to an action, 120–180ms, ease-out. Landing page gets **four** moments: the hero headline fading through Go / Go APIs / Go workers / Go crons, the mark sailing on a wave line under it, the terminal typing `grey deploy`, and the hero project canvas looping one request through wake, run, grace and sleep (pauses offscreen, still under reduced motion). |
| Marketing sections on phones as the desktop cards stacked in one column | Below md, marketing pages are built for the phone: rows divided by hairlines, timelines, a swipe rail for small sets, pricing one plan at a time. Bordered panels and cards start at md. Product fragments (terminal, code window, canvas) keep their frame because they are a real unit |
| Animated diagrams that play on their own in articles | Blog "blackboards": chalk-line SVG boxes and arrows (a slight displacement filter, existing tokens only). Readers step through them with ‹ › or the arrow keys; the lit part follows the step, the rest dims. No autoplay, transitions 150ms ease-out, none under reduced motion. On phones the board scrolls sideways inside its frame and each step pans to the lit part |
| Marketing filler: seamless, supercharge, unleash, elevate, effortless, revolutionary, next-gen, blazing, magic, "Welcome back!" | Say what it does, with a number or a command where possible ("Deploys in 8s", "Sleeps after 30s idle"). |
| Fake testimonials, fake customer logos, invented stats ("10,000+ devs") | Only real quotes and real numbers. Until then, show the product. |
| Tracked ALL-CAPS eyebrow labels and accent-coloured kicker text above every section | Plain section headings. No kicker labels unless the section genuinely needs a category. |
| One accent word in headlines (coloured, italic or bold) | Headlines in one colour and weight. |
| Arrows "→" on every link, pill badges everywhere | Arrows only on links that leave the page; badges only for status. |
| Numbered markers 01 / 02 / 03 on things that aren't a sequence | Numbers only for real sequences (setup steps). |
| Default UI-kit components left unstyled | Every primitive styled from §3 tokens with a deliberate focus state. |

### 6.2 Watch-outs specific to Greyship
Our look (near-black, one bright accent, mono type) is close to a known "second wave" AI default. It's justified by the brand — grey, terminal-first, Go — but it only works if it's executed precisely:
- Mono is for **technical content only** (commands, URLs, shas, env keys, logs, numbers in tables) — never for decoration, labels or headings.
- The orange accent appears at most **once or twice per screen**, always meaning "needs attention" or "in progress". Never decorative.
- Black must not become "dark mode template": use the exact §3 greys so surfaces, borders and text have clear steps.

---

## 7. Better design choices

1. **One focal point per screen.** Decide the single most important thing (the failing deploy, the live URL, the Deploy button) and make it dominant. Everything else is quieter. One primary button per screen.
2. **Layout follows the task, not a template.** Lists → table rows. Logs and deployments → list + detail panel. Onboarding → guided steps. Env vars → inline editor. Not every page is "sidebar + grid of cards".
3. **Spacing rhythm.** Tight inside a group (4–8px), generous between groups (24–40px). Never one gap value everywhere.
4. **Type scale, not ad-hoc sizes.** 12 / 13 / 14 / 16 / 20 / 28 / 44 / 64px. Body 14px in the app, 16px on marketing. Weights 400, 500, 600 only.
5. **Tables for tabular data.** Right-align numbers, use tabular figures, mono for ids and hashes, truncate long values with a copy button.
6. **Designed edge states on every screen.** Empty states say what this is, the one action to take, and the matching `grey` command. Errors say what happened and how to fix it. Loading uses skeletons shaped like the content.
7. **Specific copy.** Buttons say exactly what they do ("Roll back to 7f3a9c2", not "Confirm"). Errors quote the real cause ("Build failed: undefined: handler.Orders in cmd/api/main.go:42").
8. **Real-looking data.** Fixtures use realistic Go service names, commit messages, paths and latencies — never lorem ipsum or "Item 1".
9. **Accessibility as a quality bar.** Text contrast ≥ 4.5:1, visible focus rings, every control reachable by keyboard, 44px touch targets on mobile, colour never the only signal (status always has a label).
10. **Phone width works.** Sidebar becomes a bottom bar, tables scroll inside their box, nothing overflows the page.

**Typefaces:** Geist and Geist Mono are good but widely used (Vercel). Before building, pick deliberately and record it in decisions.md — either keep Geist, or choose a pairing with a more engineered feel such as IBM Plex Sans + JetBrains Mono. Two families maximum.

---

## 8. How agents build screens

1. **Plan before code.** For each screen, first write a short screen spec: purpose, focal point, layout type, components, states, exact copy. Get it approved, then build. Planning and building in one step pulls toward the generic average.
2. **Match references, don't invent.** Build from the design canvas screens and this file. "Make it look good" is not an instruction; "match screen X using tokens from §3" is.
3. **Never invent tokens.** Colours, sizes, radii, spacing and fonts come only from §3 and §7. Need a new one? Ask.
4. **Use the founder's copy verbatim.** If copy is missing, leave a clear placeholder like `[COPY: empty state for domains]` instead of writing filler.
5. **Review every screen before calling it done:** screenshot at 1440px and 390px wide, run the checklist below, fix, re-check. For important screens, a separate reviewer agent runs the checklist without having built the screen.

### Screen review checklist
- [ ] Nothing from §6.1 appears on the screen
- [ ] One clear focal point and at most one primary button
- [ ] Only §3 colours; accent used ≤ 2 times and only for attention/in-progress
- [ ] Mono used only for technical content
- [ ] Spacing follows the rhythm: tight within groups, generous between
- [ ] Sizes from the type scale only; weights 400/500/600
- [ ] Lists and tabular data are rows/tables, not cards
- [ ] Loading, empty and error states exist and are specific
- [ ] Every button label says exactly what it does; no banned words
- [ ] Every URL, id, command and secret field has a copy button
- [ ] Contrast ≥ 4.5:1, focus visible, keyboard works, status has text not just colour
- [ ] Works at 390px wide with no horizontal page scroll
- [ ] Fixture data looks real (Go names, real-looking commits and latencies)

---

## 9. Sources

- Vercel — [Dashboard redesign](https://vercel.com/blog/dashboard-redesign), [Navigation redesign rollout](https://vercel.com/changelog/dashboard-navigation-redesign-rollout)
- Railway — [Smart canvas changelog](https://railway.com/changelog/2025-12-05-smart-canvas)
- Render — [Service events on metrics](https://render.com/changelog/in-dashboard-metrics-now-display-service-events), [Improved logs UX](https://render.com/changelog/improved-logs-ux-in-the-render-dashboard-and-cli)
- Fly.io — [Scale machines via dashboard](https://community.fly.io/t/scale-machines-via-dashboard-and-improved-app-machines-page/26553)
- Anti-slop — [How to Fix AI Slop in Web Design, Tell by Tell](https://capitalandcompute.net/blog/fix-ai-slop-design/), [Why your SaaS UI looks AI-generated](https://www.saasui.design/blog/saas-ui-looks-ai-generated), [How to make AI UI look less generic](https://www.superdesign.dev/blog/how-to-make-ai-ui-look-less-generic), [Why your AI keeps building the same purple gradient website](https://prg.sh/ramblings/Why-Your-AI-Keeps-Building-the-Same-Purple-Gradient-Website)