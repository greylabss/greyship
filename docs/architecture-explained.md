# Architecture — explained like you're a kid

This explains how the Go serverless platform works, using one big story: **a city of sleeping robots.** Every technical piece gets a kid-friendly role first, then its real name.

---

## The big idea in one paragraph

Your Go app is a **robot**. We build the robot in a **factory**, keep it in a **closet**, and put it in a **room** in a **building** when someone needs it. A **receptionist** at the front door greets every visitor (every HTTP request) and takes them to the right robot. When no visitors come for a while, the robot **goes to sleep**, and you stop paying. When a visitor shows up again, the robot is **woken up super fast**. That's "serverless": you never think about the buildings, only about your robot.

---

## The cast of characters

| In the story | Real name | What it does |
| --- | --- | --- |
| 🤖 The robot | Your Go app (a static binary) | Does the actual work: answers requests |
| 🧑‍💻 You at the counter | CLI **or** Dashboard | Two doors into the same office. Type `deploy` or click "Deploy" — same result |
| 🏢 The manager's office | Control plane API | Knows everything: every app, owner, setting, domain |
| 📒 The manager's notebook | Postgres | Where the office writes everything down |
| 🗺️ The planner | Scheduler | Decides which building and room each robot goes into |
| 🏭 The factory | Builder | Turns your code into a robot (`go build`) |
| 🗄️ The closet | Object storage | Keeps finished robots and their "photos" (snapshots) |
| 🏠 A building | Runtime node (a server) | Where robots actually live and work |
| 🚪 A room | Sandbox (container + gVisor now, Firecracker microVM later) | A locked room so robots can't touch each other |
| 🧹 The caretaker | Node agent | Lives in each building; wakes robots, puts them to sleep, cleans up |
| 👩‍🍼 The nanny | Guest init | Lives inside each room; starts the robot, gives it its settings, writes down what it says |
| 💁 The receptionist | Edge router | Greets every visitor and walks them to the right robot |
| 📞 The intercom | NATS (event bus) | How the office, receptionist and caretakers talk to each other |
| 📔 The diary library | Logs + metrics (Postgres/Loki now, ClickHouse later) | Everything the robots said and did |
| 🩻 The X-ray machine | pprof + Go runtime metrics | Looks inside a robot: goroutines, memory, slow parts |
| 🔐 The sealed envelope | Secrets / env vars | Passwords and keys, opened only inside the robot's room |

---

## Why the CLI and the dashboard are the same thing

Think of the manager's office having **two doors**:

- **Door 1 (CLI):** for people who like typing. `grey deploy`, `grey logs -f`.
- **Door 2 (Dashboard):** for people who like clicking. Connect GitHub, pick a repo, press Deploy, watch logs scroll.

Behind both doors is the **same office** (the control plane API). So anything you can do in one, you can do in the other. Build the API first, then both doors are just different ways to talk to it.

```
CLI ───────┐
           ├──► Control plane API ──► everything else
Dashboard ─┘
```

---

## Story 1: What happens when you deploy

1. **You hand in your instructions.** You type `deploy` or click "Deploy". Your code goes to the manager's office.
2. **The office writes it in the notebook.** "New version of `alice-api`, building now." (Postgres)
3. **The factory builds the robot.** The builder runs `go build` inside its own locked room, because your code might do weird things while building. Out comes one small robot (a static binary).
4. **The factory finds all your robots.** If your repo has `cmd/api` and `cmd/worker`, that's two robots: one that talks to visitors, one that does background chores.
5. **The robot goes in the closet.** The finished robot is stored in object storage.
5½. **(If you have one) The setup robot runs first.** Before the new robots start work, a one-time setup robot (your migration program) rearranges the shelves in your database. If it trips up, the whole new version is cancelled and the old robots keep working, so customers never notice.
6. **(Later stage) Someone takes a photo.** The robot is switched on once, and when it's ready, we take a **photo** of it awake (a snapshot). More on why below.
7. **The planner picks a building.** The scheduler chooses a server with enough free space and asks that building's caretaker (the agent) to open a room for each new robot. It waits for every new robot to say hello (the health check). Only then does it point visitors at the new robots and send the old ones home. If a new robot never says hello, the new ones are sent home instead, and **the old robots keep working** as if nothing happened. There's only ever one planner in charge: it holds a special key (a database lock), and a spare planner waits until the key is free.
8. **You get your link.** `api-alice-api.greyship.app` is live.

---

## Story 2: What happens when someone visits your app

A visitor (a request) arrives at the front door asking for `alice-api`. The receptionist checks: **what is the robot doing right now?**

### How a visitor finds your robot (the receptionist and the padlock)
Every web robot gets its own address, like `api-shop.greyshipapp.tech`, chosen the first time it's deployed and kept from then on. When a visitor arrives, the receptionist (the router) reads the **name badge** they're wearing (the hostname), looks it up in the **guest book** (the routing table: which robots are on duty right now, kept in memory and updated the moment a new version goes live), and walks them to one of the robots. If there are two copies, visitors take turns between them.

The **padlock** (HTTPS) means nobody in between can read or change what the visitor and your robot say to each other. The receptionist gets a padlock certificate for your address automatically the first time someone visits, but only for addresses that really belong to a robot, so nobody can trick us into making certificates for anything else. Anyone who knocks on the plain, unlocked door (HTTP) is sent round to the padlocked one.

### Why robots live on their own street (greyshipapp.tech)
Greyship's office (the dashboard and the API) is on **greyship.tech**. The robots live on a **different street**, **greyshipapp.tech**. Browsers treat everything on one street as neighbours who trust each other a little: a neighbour can knock on the office door while you're signed in, and the office might think it was you. Anyone can deploy a robot, so a sneaky one living on the office's street could try exactly that, or put up a sign saying `login-greyship` to fool people. With the robots on their own street, the browser treats them as strangers to the office, so neither trick works. Later the street becomes `greyship.app`.

### Case A — The robot is awake 🟢
The receptionist walks the visitor straight in. Super fast. One robot can talk to **many visitors at once** (Go is great at this).

### What the robot's room really looks like
A robot's room holds exactly two things: the robot (your program) and the **nanny** (guest init). No furniture, no tools, nothing a burglar could use. The robot isn't the boss of the room either: it runs as a nobody user with no special powers.

The nanny gets the robot's instructions, including its secrets, **whispered through the door** (stdin) when the room opens. They're never written on a sign on the door, because signs (container settings) get stored on disk. Then the nanny wakes the robot, passes on "please stop" messages, and cleans up after it.

Every room has **its own corridor** (network), so robots can't knock on each other's doors, and there's **no door into the building's own offices** (the node): a robot can phone out to the internet, but it can't walk into the machinery that runs everything. Before anyone sends visitors, the caretaker checks the robot is really answering (the health check). If it never answers, or falls over straight away, the room is cleared and you're shown the robot's last words.

### Case B — The robot is napping (paused) 😴
The robot is frozen like a statue, but still in its room. The receptionist rings the caretaker, the caretaker taps the robot, and it unfreezes in a few milliseconds — with all its memory still there. (Measured on the development Mac: about 40 thousandths of a second, including setting up the padlock. A robot that was packed away completely took about a second.) The receptionist doesn't even wait for the visitor to finish knocking: as soon as the visitor says which robot they want, while the padlock is still being fitted, the caretaker is already tapping the robot.

### Case C — The robot is in the closet (stopped) 📦
Nobody visited for a long time, so the robot was packed away. The receptionist says "one moment please" and **holds the visitor at the door**. The caretaker takes the robot out and starts it. This is a **cold start**. Go robots start fast, so the wait is small. Then the visitor goes in.

### What happens after the visitors leave
- **Stay awake a bit (grace window, ~30s).** The robot keeps running so it can finish chores it started in the background (goroutines). This is the "goroutines that don't die" feature.
- **Nap (pause).** Then it freezes. **You stop paying here.**
- **Back to the closet (only when needed).** Napping Go robots are tiny, so they keep napping in their rooms for up to a day. Only if the building runs out of space does the caretaker pack away the robot that's been napping longest.

This sleeping-and-waking is called **scale to zero**: zero robots running when zero visitors come.

### What if LOTS of visitors come?
The receptionist counts how many visitors each robot is handling. When one robot gets too busy, she asks the planner for **another copy of the same robot** in another room. That's **autoscaling**.

---

## The tricky words, explained simply

### Who gets a key? (accounts and passwords)
Think of Greyship as a building. The first person to move in is the **owner** and holds the master key (an **admin**). Everyone else needs the owner to let them in. The owner can cut a new key for someone, a name and a secret password, and say whether it is a master key or a normal one. We never keep the password itself, only a scrambled fingerprint of it, so even someone who steals our notes can't read it. If someone guesses wrong too many times in a row, the door stays shut for a few minutes. At least one master key always exists.

### How does Greyship send email?
Greyship doesn't run its own post office. It hires one called Resend. The owner gives Greyship the Resend key (like a membership card) and the street address it should send from (the domain) on the Email page. Greyship locks the card in a safe (encrypted) and takes it out only when it needs to post a letter. With no card, no letters go out; sign-in codes are left in a drawer on the server instead.

### What's a "kernel"?
Every building has a **building boss** who controls the electricity, water, doors and elevators. Robots can't touch those directly — they have to ask the boss. In a computer, that boss is the **kernel**: it controls CPU, memory, files and network.

### Container vs gVisor vs Firecracker microVM
These are three kinds of rooms, from weakest to strongest walls:

| Room type | Kid version | Real meaning |
| --- | --- | --- |
| **Container** | A room with thin walls; all robots share the same building boss | Shares the host kernel. Fast, but a sneaky robot might bother the boss |
| **gVisor** | Same room, but a **security guard** stands at the door and handles every request to the building boss | A fake kernel in between that checks everything. Safer, no special hardware needed |
| **Firecracker microVM** | Every robot gets its **own tiny house with its own building boss** | A real mini virtual machine with its own kernel. Safest and can be snapshotted |

### What's KVM, and why can't my VPS do Firecracker?
To build tiny houses fast, the computer's CPU needs a **special power** called **KVM**. Most VPS providers hide that power from you (your VPS is already a tiny house inside *their* building). So:
- **On your 12GB VPS:** use rooms with a security guard (containers + gVisor). Works fine.
- **Later on bare metal servers:** you get KVM, so every robot gets its own tiny house (Firecracker).

### What's a snapshot, and why does it make cold starts fast?
Starting a robot from scratch = unpack it, switch it on, wait for it to boot.
A **snapshot** is a **magic photo** of the robot already switched on and ready. Restoring from the photo gives you back the robot *exactly* as it was — already awake. That's much faster than booting. (One catch: two robots made from the same photo would have the same "random dice" and the same clock time, so the nanny re-rolls the dice and fixes the clock right after waking.)

### What does the nanny (guest init) do?
She lives inside each room and:
- turns the robot on,
- hands it its settings and secrets (env vars),
- writes down everything the robot says (logs),
- reads the robot's health (goroutine count, memory) for the X-ray machine,
- tells the robot "time to stop" nicely when the room is closing (SIGTERM),
- works as the **doorman**: visitors can only knock on one door of the room, door 8080. If the robot set up its desk somewhere else (say at desk 3000), the nanny stands at door 8080 and walks every visitor to desk 3000 and every answer back. So builders never have to think about which door their robot uses.

### What's the intercom (NATS) for?
So helpers don't have to keep walking to the office. The office announces "new version of `alice-api` ready!" and every caretaker hears it. The receptionist announces "`alice-api` is too busy!" and the planner hears it.

### How does billing work?
- The **receptionist counts visitors** (requests).
- The **caretaker counts how long each robot was awake** (compute time).
- Every 10 seconds the caretaker reports the counts to the office, which adds them up into your bill. Napping robots aren't counted.

### How does Greyship know which robots to build? (finding services)
Before building, Greyship walks through your repo like a librarian. Every folder with a `package main` is a robot it could build, and the folder's name becomes the robot's name (`cmd/api` → `api`). Folders full of examples, test fixtures or tools you only run by hand are left on the shelf, and Greyship tells you which ones and why.

To decide what kind of robot each one is, it reads the instructions starting at `main()` and follows them into your own packages. If somewhere along the way the robot **opens a door for visitors** (starts a web server, like `http.ListenAndServe` or gin's `r.Run()`), it's a **web robot** and gets a web address. If it never does, it's a **worker robot** that does chores. It never guesses "cron": that one you write down in `greyship.toml`. Every deploy prints its reasoning (`api  web  starts a server in cmd/api/main.go:22`), so a wrong guess is easy to spot and fix with one line.

### How do I hear when something happens? (notifications)
When your robot goes live, when it fails to start, or when you make a new project, Greyship tells you three ways: a dot on the bell in the dashboard, an email, and, if you said yes in your browser, a little pop-up on your computer or phone even when the dashboard is closed. Only the person who started it is told, so teammates aren't flooded. A failure message says why, and that the old robot is still doing the work. You can switch emails or pop-ups off for each kind of news in Settings.

### What if a robot needs papers from its toolbox? (runtime files)
Normally a robot's room holds only the robot itself, no boxes from your workshop. If the robot needs to read some papers at work (an install script, page templates), you write them on a list in `greyship.toml`: `files = ["scripts/install.sh", "templates/"]`. Greyship copies exactly those papers into the room, on a shelf the robot can read but not scribble on, in the same places they sit in your workshop. Nothing else from the workshop comes along, and secret papers (`.env` files) never do.

### What about background robots? (workers)
Some robots don't talk to visitors — they do chores, like sending emails. They have a **mailbox** (the queue).
- When someone drops a chore note in the mailbox, the caretaker **wakes the chore robot**.
- The robot takes notes one by one, ticks each off when done, and if it drops one, the note goes back in the mailbox to try again.
- When the mailbox is empty, the robot **naps** — and you stop paying.
- A robot that must stay awake all the time (because it listens to something outside the building) can, but it gets a big label: **"always on · you pay 24/7."**

### What about robots that work as a team? (microservices)
Some companies split their app into many small robots: a **front-desk robot** (the API gateway) that talks to visitors, and **back-room robots** (orders, payments) that only the front-desk robot talks to.
- **Back-room robots have no front door.** They're "private": visitors from outside can't reach them at all.
- **Robots call each other by name.** The front-desk robot just says "orders, please" (`orders.internal`) and the **building's inner receptionist** (the internal router) walks the message over.
- **Back-room robots can nap too.** Because every message goes through the inner receptionist, she can wake a napping back-room robot first — so you don't pay for robots that are rarely needed.
- **Only your team's robots can talk.** Robots from other projects never hear these calls.

### Why are Go robots so quick to wake?
Because every robot is a Go robot, we can cheat a little:
- **Tiny robots nap instead of going to the closet.** A sleeping Go robot takes up very little space, so we let hundreds of them nap in their rooms. Waking a napping robot takes a blink.
- **The receptionist listens at the door.** The moment a visitor says which robot they want (while they're still wiping their feet — the TLS handshake), she rings the caretaker, so the robot is awake by the time the visitor walks in.
- **No luggage.** A Go robot is one solid piece (a static binary). There's no suitcase of parts to unpack before it can work.
- **The nanny sets the robot's dials.** She tells it exactly how many arms (CPU) and how much stomach (memory) it has, so it never overworks or overeats.
- **Later: the robot learns from its own work.** We watch which parts of the robot work hardest in real life, and the factory builds the next version tuned for exactly that (PGO).

### How do you get into the office? (logging in)
There are no passwords to remember. At the front desk you either:
- **get a 6-digit code in your email** and read it back to the guard (it only works once, for 10 minutes), or
- **show your Google or GitHub badge**, and the guard checks with Google or GitHub that the badge is really yours.

Once you're in, the guard gives you a **visitor sticker** (a session) so you don't have to show your badge on every door. The office only keeps a fingerprint of the sticker, so even someone who reads the office notebook can't copy it. The CLI gets its own sticker by asking you to approve it in the browser. `grey login` shows a short code and opens the dashboard; you check the code matches and press Approve, and the CLI gets its sticker. Only the dashboard's own pages can press that button: the office refuses any request that didn't come from the dashboard's front door (greyship.tech), so a sneaky robot on the robots' street can't approve things for you.

The CLI's sticker only opens doors for **one team**. If you belong to two teams, a sticker made for team A can't open team B's rooms, even though it's still you.

### How does a folder know which project it is? (grey init)
A **project** is one app's room in the office: it will hold its services, settings and web addresses. `grey init` asks the office for a new room (named after your Go module in `go.mod`, or your folder if there isn't one, unless you pick another name) and leaves a small **name tag** in the folder: `.greyship/project.json`. The tag has the team, the room's name and its id number, and nothing secret, so it's fine to commit to git. After that, every `grey` command run in that folder (or any folder inside it) reads the tag and knows which room you mean. Renaming the room later doesn't break the tag, because the id number never changes.

You don't even have to run `grey init`. If you just type `grey deploy`, it guesses the name the same way, gets the room and leaves the tag by itself. The only time it stops to ask is when your team already has a room with that name: it won't move into someone else's room without your "yes", and if you say no it makes a new room called `<name>-2`.

### The to-do board (the job queue)
When the office wants something done ("build this deployment"), it pins a **card on the to-do board** (a `jobs` table in Postgres) and rings a little bell (`NOTIFY`) so the factory workers look up. A worker takes the card off the board and writes their name and a **time limit** on it, like "Sam has this until 3:05". While Sam works, Sam keeps pushing the time limit forward.

If Sam's computer crashes, Sam stops pushing the time. When the limit passes, the card counts as free again and another worker takes it, so **no job is ever lost**. The catch: a job can occasionally be done twice, so every job is written to be safe to repeat. If a job keeps failing, it waits a bit longer each time and, after five tries, it goes in the "failed" pile with a note saying why.

The card and the change it belongs to are written together: "deployment queued" and "please build it" either both happen or neither does.

### Deploying from GitHub (the robot watches your code shelf)
If your code lives on GitHub, you can give Greyship a **key to just the shelves you choose** (you install the Greyship GitHub app and tick the repositories). From then on, whenever you push new code to your main branch, GitHub rings Greyship's doorbell (a webhook, with a secret handshake so nobody can fake the ring). Greyship takes the exact version you pushed off the shelf, leaves out anything secret like `.env` files, and sends it to the fast workshop to be built. GitHub then shows a little ✓ (or ✗) next to your commit, with a link to the deploy. If the new version doesn't work, the old one keeps running, as always.

**Just want to share a link?** If the code is public, you can paste its GitHub link instead, with no app to install. Greyship builds it straight away. Because nobody rings our doorbell for public links, new versions arrive when you press **Deploy latest**, a bit like asking the library for the newest edition yourself.

### The factory moves to a fast workshop (builds on Fly)
*(Being built, decisions 039–041.)* When your computer doesn't have Go, the factory builds your robot for you. That factory used to be a corner of the same building where the robots live, so a big build took space and energy from robots that were already working. Now the factory is a **separate, very fast workshop** (Fly Machines) with strong workers who only come in when there's a build. When there's nothing to build, the workshop is closed and the lights are off, so it costs almost nothing.

Each team gets **its own workbench with its own drawer of spare parts** (a cache volume), so your second build is quicker, and no other team can ever put a bad part in your drawer. The workshop never gets your secrets. It gets a one-time pickup slip for your code box and drop-off slips for the finished robots (presigned links that expire), and that's all.

How big the workbench is, and how many minutes of building a month you get, depends on your plan: Free gets a small bench and 100 minutes, Pro a bigger one and 500, Team the biggest and 1,500. Building on your own computer is still free and doesn't use any minutes.

**The warehouse moves too.** Code boxes and finished robots used to sit in a warehouse inside our building (Garage). They now go to a big shared warehouse, Cloudflare R2, so the workshop can reach them, and so nothing important is lost if our building ever has a problem.

### The factory's locked room (building)
When a factory worker takes a "build this" card, they fetch your box from the warehouse and check its fingerprint. Then they read your code to find the robots (without running it), and put the box into a **locked room** (a gVisor sandbox inside a small Linux computer, the Lima VM on a Mac).

The room has **one door, and it only opens to the parts shop** (the module proxy). The parts shop sells Go modules and nothing else: it can't be used to reach the internet, other people's rooms, or the factory itself. It keeps copies of every part it has sold, so the next build that needs the same part gets it instantly, and the go command still checks each part's official fingerprint so nobody can slip in a fake.

Inside, the room builds each robot (`go build`), shouts every line of what it's doing (the build log, which you watch live in your terminal), and hands the finished robots out through a slot. If the build takes longer than 10 minutes, the room is shut. If your code has a mistake, you see the compiler's message, the new version is marked failed, and **the version already running keeps running**. The finished robots go into the warehouse, labelled with their own fingerprints.

### Hearing what your robot says (logs)
Everything your robot says out loud (whatever your program prints) is written down by the caretaker in a **diary** (the logs table), a few lines at a time. `grey logs -f` lets you read the diary live as new lines appear, and if your robot speaks in tidy structured notes (JSON logs), grey reads them back to you neatly. A robot that shouts thousands of lines a second only gets its first thousand each second written down, plus a note saying how many were skipped, so one noisy robot can't fill the whole diary. Old pages are thrown away after a day on the free plan.

### Why the second build is faster than the first (the factory's spare parts)
Every Go robot is made with the same standard parts (Go's standard library). The first time the factory builds a robot, it has to make all those parts from scratch, and that takes a while. So the factory keeps a **shelf of ready-made standard parts** for each Go version, made by the factory itself, never by a customer. When a new project arrives, it gets **its own copy** of that shelf, so its first build skips straight to your code. Nobody else's project can touch the shelf, so nobody can slip a bad part into your robot. After the first build, your project keeps its own shelf, and later builds only make the parts you changed. The factory has **two workbenches**, so two robots can be built at the same time, and each team gets one workbench at a time: if you send five robots in a row, they wait behind each other, not in front of everybody else. While you wait, grey tells you how many builds are ahead of yours.

### Building your robot at home (local builds)
Most people who write Go robots already have a **robot workshop at home** (Go installed on their computer), with all the parts they used last time still on the bench. So when you type `grey deploy` and your computer has Go, grey **builds the robots right there on your computer**, the way they need to be for Greyship's building (Linux, the right kind of processor). Your bench already has the parts, so this usually takes seconds instead of minutes.

Then grey only sends the **finished robots**, not your code. Each robot has a fingerprint, so if a robot is exactly the same as last time, grey doesn't send it again; the warehouse already has it.

At the factory door, a guard **checks each robot**: is it really a Go robot, was it made for this building, and is it the robot you said you'd send (same fingerprint and size)? If you accidentally built it for your Mac instead of Linux, the guard tells you plainly and nothing changes. The guard is about catching mistakes, not about trust: every robot, from home or from the factory, still lives in its own locked room (gVisor), so a sneaky robot can't hurt anyone either way.

If your computer doesn't have Go, or you say `grey deploy --remote`, grey does what it always did: packs your code in a box and the factory builds it (below).

### Where does your code go when you deploy? (packing and the locker key)
`grey deploy` first **packs your folder into one box** (a `.tar.gz`). It leaves out anything your `.gitignore` or `.greyshipignore` says to skip, and it **always** leaves out the `.git` history, `node_modules`, shortcuts (symlinks) and every `.env` file. Your secrets never travel inside the box. The same files always make exactly the same box, so its fingerprint (sha256) tells us it's really your code.

Before sending anything, grey also looks at your code and prints the robots it found (`api` is web, `worker` is a worker) and why, so you can spot a surprise early.

Then it asks the office: "I have a box this big with this fingerprint." The office writes a new deployment in the notebook and hands back a **locker key**: a special link to one locker in the warehouse (object storage). The key works for 15 minutes, only for a box of exactly that size, and only to put it in, not to take anything out. Your code goes straight from your computer to the warehouse without passing through the office. Then grey tells the office "it's in", the office checks the locker, and the deployment joins the queue for the factory.

The warehouse is **Garage**, a small storage program that speaks the same language as Amazon S3, so we could move to another warehouse later without changing the code.

### How are secrets kept safe?
Secrets are kept in a **sealed envelope** (encrypted). The envelope is only opened **inside the robot's room**, right when it starts. They're never written into the snapshot photo in plain form.

Each robot gets **its own envelope**. There's a **family envelope** with things every robot needs (like the database address), and each robot can have **its own private notes** on top. If a private note says something different from the family envelope, the private note wins — for that robot only. And a robot never gets another robot's private notes, so the payments robot can keep the bank key to itself.

How the envelope works for real: every project has its **own key** to its envelopes, and that key is itself locked in a safe that only Greyship's master key opens. Each secret is sealed with a label that says exactly which project and name it belongs to, so a sealed secret can't be swapped into somebody else's box. When you run `grey deploy`, grey compares your `.env.prod` with what's stored using **fingerprints** of the values, not the values themselves, shows you just the names that would change, and sends only those. Changing a value with `grey env set` swaps your running robots for fresh ones that read the new value, a couple of seconds later and without rebuilding anything. Only `grey env pull` ever brings values back to your computer, and only for people who can change the project.

---

### What about the website and the blog? (the shop window)
Before the office opens, there's a **shop window** on the street: the landing page and a blog that explains, with chalkboard drawings, exactly how the office works inside. A sign on the door says **"coming soon"**, so the back rooms (the dashboard) stay locked until opening day. Flip one switch and the sign changes to "waitlist", then to "open".

People can leave questions on a little notice board under each blog post. Notes don't go up straight away: the founder reads them first and pins up the good ones, sometimes with an answer. That notice board has its own small drawer of paper (its own database), so nothing on it ever mixes with the office's real files.

## Stage 0: everything on one 12GB VPS

On the real server there is **one front door**: the receptionist (router) answers ports 80 and 443 and nothing else from the outside gets in. Visitors for apps are walked to their robots; anyone asking for `api.greyship.tech` (the office) or `s3.greyship.tech` (the warehouse, where your code is uploaded) is walked there instead. The notebook (Postgres) and the warehouse (Garage) only accept visitors from inside the building. All the secret keys for the building live in one locked drawer (`/etc/greyship`), which only the building manager and the Greyship staff can open.

At the start, the **whole city is one building**:

```
┌──────────────────── your 12GB VPS ────────────────────┐
│  Manager's office (API + scheduler)   Postgres         │
│  Receptionist (router)                Factory (builder)│
│  Rooms with security guards (gVisor containers)        │
│    🤖 🤖 🤖 😴 😴 😴 😴 (many sleeping, few awake)          │
└────────────────────────────────────────────────────────┘
```

- The factory is only allowed a fixed amount of memory (~1.5GB) so building a robot never squeezes the robots already working.
- Each robot gets a memory limit (e.g. 128MB on free).
- Because most robots sleep most of the time, one building can hold **far more apps** than "always on" hosts like Render.

### Later: a real city
```
             Visitors
                │
        ┌───────▼───────┐
        │ Receptionists │  (edge routers, one set per region)
        └───────┬───────┘
     ┌──────────┼──────────┐
 ┌───▼───┐  ┌───▼───┐  ┌───▼───┐
 │Bldg 1 │  │Bldg 2 │  │Bldg 3 │   (bare-metal runtime nodes,
 │🏠🏠🏠 │  │🏠🏠🏠 │  │🏠🏠🏠 │    each robot in its own
 └───────┘  └───────┘  └───────┘    Firecracker tiny house)
        ▲
   Manager's office + notebook + factory + closet
   (control plane, Postgres, builders, object storage)
```

---

## Cheat sheet

| If you hear… | Think… |
| --- | --- |
| Control plane | The manager's office |
| Data plane | The buildings and rooms where robots actually run |
| Edge router / reverse proxy | The receptionist |
| Node agent | The building caretaker |
| Guest init | The nanny inside the room |
| Cold start | Taking the robot out of the closet and switching it on |
| Warm instance | A robot that's already awake |
| Scale to zero | Everyone sleeps when nobody visits |
| Snapshot / restore | Magic photo of an awake robot |
| Kernel | The building boss |
| Sandbox / isolation | Locked rooms so robots can't bother each other |
| Metering | Counting visitors and awake time for the bill |