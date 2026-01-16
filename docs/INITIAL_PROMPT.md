
╭─── Claude Code v2.0.76 ──────────────────────────────────────────────────────╮
│                                           │ Tips for getting started         │
│            Welcome back Jesse!            │ Run /init to create a CLAUDE.md… │
│                                           │ ──────────────────────────────── │
│                * ▐▛███▜▌ *                │ Recent activity                  │
│               * ▝▜█████▛▘ *               │ No recent activity               │
│                *  ▘▘ ▝▝  *                │                                  │
│                                           │                                  │
│   Opus 4.5 · Claude Max · Jesse Vincent   │                                  │
│              ~/git/stockyard              │                                  │
╰──────────────────────────────────────────────────────────────────────────────╯

> We're trying to build out a tool to allow us to spin up anywhere between 1
 and, if the hardware supported it, hundreds of lightweight containers or 
micro-containers to run coding agents like Claude Code.

The intent here is that developers might be able to spin up between 1-5 of 
them in parallel with their credentials with the coding agent and a 
reasonable environment pre-installed, and then be able to execute the coding
 agent in Dangerously Skip Permissions mode where it already has GitHub 
credentials and Anthropic or OpenAI credentials and just run. We're going to
 want to be able to pull logs and telemetry out of these instances, both at 
runtime and at shutdown time. We are going to want to mount the user's.cloud
 directory, or at least one that the user specifies, into the container. I 
can believe there is a world where we're going to want to be able to take 
file system snapshots of the part of the container that work is being done 
in. 

Maybe eventually we run this in AWS with our own Firecracker instances. But 
for prototyping, we're going to run it here locally on this machine, 
flower-garden, which has Docker pre-installed. 

Authorized developers should be able to remotely spin up and connect to 
containers over SSH, possibly using Tailscale magic authentication. 

It would be really nice if we didn't lose everything if a container shut 
down. I don't want to lose 36 hours of coding work just because there was a 
glitch. 

We're going to want a nice, tight little CLI that can run remotely that can 
list containers that can start a container that can stop a container, and 
probably that can pull data from a container. I don't know, we want 
something like file system snapshots, maybe it's just that it can run an SSH
 command against the container. 

What am I missing? What do you need to know? 

● This is a new project design session, so I should invoke the brainstorming skill first.

● /superpowers:brainstorming

● Using brainstorming skill to help design this container orchestration system.

  Let me first understand what's already in this project:

● Bash(ls -la /home/jesse/git/stockyard)
  ⎿  total 12                                        
     drwxrwxr-x  3 jesse jesse 4096 Jan 16 21:16 .
     drwxrwxr-x 23 jesse jesse 4096 Jan 16 21:16 ..
     drwxrwxr-x  7 jesse jesse 4096 Jan 16 21:17 .git

● Bash(git -C /home/jesse/git/stockyard log --oneline -10 2>/dev/null || echo
      "No commits yet")
  ⎿  No commits yet

● Fresh repo - true greenfield. Good, that means we can design this cleanly.

  You've given me a solid picture, Jesse. Let me start clarifying the fuzzy edges. First question:

  How does a task get specified to the agent?

  A. Git-centric: Developer provides a repo URL + branch + task description (e.g., "clone obra/foo, checkout feature-x, implement the TODO in auth.py")

  B. Workspace-mounted: Developer has a local checkout they want to mount into the container, agent works on that directly

  C. Task ticket: Agent gets pointed at a GitHub issue or some task spec and figures out the repo/context itself

  D. Hybrid: Support multiple modes depending on the use case

  Which of these matches your mental model? Or is it something else entirely?

> A or C probably explicitly not B. 

● Got it - the container owns its workspace, no mounting the developer's live checkout. That's cleaner for parallelism anyway.

  How does completed work get out of the container?

  A. Push to remote: Agent commits and pushes to a branch on GitHub (e.g., agent/task-123). Work is safe as soon as it's pushed.

  B. PR creation: Agent opens a draft PR when it thinks it's done, developer reviews from there.

  C. Export on demand: Work stays in-container, CLI has a command to pull a tarball/patch/diff when the developer wants it.

  D. Continuous sync: Container's workspace is backed by persistent storage that survives container death, developer can grab work anytime.

  I'm guessing A or B fits the "don't lose 36 hours" requirement best since work escapes the container early. But D might be useful as a safety net regardless. What's your preference?

> A and D by default.  (B is a thing that will happen but is outside the 
scope of this tool) 

● Smart - belt and suspenders. Agent pushes frequently, but persistent storage catches anything in-flight if things go sideways.

  How should credentials get into the container?

  A. Baked into image: Build images with credentials already in place. Simple but means different images per developer, and credential rotation requires rebuilds.

  B. Mounted at runtime: Pass credentials as mounted files or environment variables when starting the container. Single image, credentials stay outside.

  C. Secrets manager: Container fetches credentials from Vault/AWS Secrets Manager/1Password CLI at startup. Most secure, but adds infrastructure dependency.

  D. Inherited from host: Container gets credentials from the host's environment (e.g., host's ~/.config/gh, Anthropic API key from host env). Simple for local dev.

  For local prototyping, D or B seem pragmatic. For eventual multi-tenant/AWS deployment, C might matter. What's your leaning for the prototype, and do you want to architect for C from the start?

> I'd prefer C but B is ok too. what's the lightweight way to do C without 
AWS? 

● Good question. A few lightweight options:

  1Password CLI (op) - If you already use 1Password, this is nearly free. Container runs op read op://vault/item/field or op run injects env vars. Team sharing built in.

  SOPS + age - Mozilla's SOPS encrypts secrets in files (YAML/JSON), age handles the encryption. No server, secrets can live in git encrypted. Container decrypts at startup with a key.

  Doppler - SaaS secrets manager with a generous free tier. doppler run -- command injects secrets. Easy team management, no self-hosting.

  Infisical - Open source, self-hostable or cloud. Similar UX to Doppler.

  HashiCorp Vault dev mode - The "real" thing but vault server -dev runs in-memory for prototyping. Not for production but lets you build against the Vault API now, swap in real Vault later.

  My recommendation: 1Password CLI if you use it already, otherwise Doppler for lowest friction. Both work locally and scale to teams without architecture changes.

  Do you have an existing password manager / secrets tool you'd want to lean on? Or should we design an abstraction layer so the secrets backend is pluggable?

> let's use 1password cli but also plan to be able to use the aws secret 
store down the road 

● Perfect - 1Password CLI now, abstraction layer so we can swap in AWS Secrets Manager later. The interface is simple: "give me the secret named X" - implementation details stay behind that.

  What language/stack for the CLI and orchestration layer?

  A. Go - Single binary, good Docker SDK, easy cross-compilation. The "boring correct" choice for CLI tools.

  B. Rust - Same benefits as Go, better error handling, steeper learning curve. Overkill unless you want it.

  C. Python - Fastest to prototype, Docker SDK is solid, but distribution is messier (venv, dependencies).

  D. Shell + Docker CLI - Scrappy prototype, just wrap docker commands. Fast to start but hits walls quickly.

  Given you mentioned "nice tight little CLI" and eventual AWS/Firecracker, I'd lean Go - it's the lingua franca for this kind of infra tooling and has good libraries for everything we need. But if you have a preference or existing ecosystem, that matters more.

  What's your call?

> Go or Rust. if one of them has a better sdk for this, so much the better. 
(I think we want to start with firecracker locally, rather than full 
containers locally) 

● Ah, Firecracker from the start - that's actually cleaner than migrating later. Micro-VMs give you real isolation, which matters when running untrusted agent code.

  SDK comparison for Firecracker:

  Go: firecracker-go-sdk is the official AWS-maintained client library. Mature, well-documented, actively developed. This is what most Firecracker tooling uses (Weave Ignite, Flintlock, etc.).

  Rust: Firecracker itself is Rust, but ironically the Rust client libraries are less mature. You'd end up wrapping the API yourself or using the Go SDK via FFI, which defeats the purpose.

  Recommendation: Go. The Firecracker ecosystem has standardized on Go for client tooling. Fighting that upstream would burn time for no benefit.

  ---
  Follow-up question: Do you want to use Firecracker directly or go through a higher-level wrapper?

  A. Raw Firecracker - We manage VM lifecycle, rootfs images, network config ourselves. Full control, more code.

  B. Ignite (Weaveworks) - Wraps Firecracker with Docker-like UX (ignite run, ignite ps). Uses OCI images as rootfs. Less maintained lately but works.

  C. Flintlock - More recent, designed for Kubernetes integration but can run standalone. Might be overkill.

  I'd lean A (raw Firecracker) since we're building our own orchestration anyway and don't want someone else's abstractions in the way. But if you want faster time-to-prototype, B gets you running VMs in an afternoon.

> I want you to assign a couple subagents to go study the current state and 
tradeoffs of the three. I hear good things about flintlock. but i've never 
touched it.    or does ignite or flintlock do all this for us? 

● Good call - let me dispatch agents to research each option in parallel.

● 3 Explore agents finished (ctrl+o to expand)
   ├─ Research raw Firecracker · 13 tool uses · 28.2k tokens
   │  ⎿  Done
   ├─ Research Weaveworks Ignite · 15 tool uses · 27.2k tokens
   │  ⎿  Done
   └─ Research Flintlock · 15 tool uses · 27.9k tokens
      ⎿  Done

● Excellent research came back. Here's the synthesis:

  Research Summary

  Ignite: ❌ Dead

  - Archived December 2023, last release July 2021
  - Official recommendation: use Flintlock instead
  - Was exactly what we'd want (Docker-like UX for Firecracker) but it's abandoned
  - No persistent volume support anyway

  Raw Firecracker: ⚠️ Viable but painful

  - firecracker-go-sdk is stable but last release was Sept 2022 (v1.0.0)
  - You build everything yourself: rootfs images, kernel, TAP networking per VM, storage, process supervision
  - Networking is the biggest pain point - manual TAP device setup per VM
  - Snapshotting exists but complex (memory files, network state loss, block device flushing)
  - Estimate from research: 8-12 weeks for production-ready orchestration
  - Good if you need extreme control or extreme scale (thousands of VMs)

  Flintlock: ✅ Best option

  - Actively maintained - v0.9.0 released November 2025
  - Community-owned after Weaveworks shut down (Liquid Metal project)
  - gRPC daemon with HTTP wrapper - clean API
  - Supports both Firecracker AND Cloud Hypervisor
  - Uses containerd for OCI image management (so you can use Docker images as VM bases)
  - Full cloud-init support for SSH keys, credentials, startup scripts
  - Works standalone - Kubernetes integration exists but not required
  - Good Go SDK for embedding
  - Gaps: CNI networking still "coming soon" (you manage TAP/macvtap yourself), snapshotting not first-class

  ---
  My take: Flintlock is the clear winner. It handles the orchestration layer (VM lifecycle, OCI images, cloud-init) while we build the agent-specific parts (credential injection from 1Password, task assignment, log extraction, CLI).

  The main work we'd own:
  1. Networking setup (TAP devices, routing) - Flintlock doesn't do this yet
  2. Secrets integration (1Password CLI → cloud-init injection)
  3. Our CLI wrapper
  4. Persistent storage strategy (bind-mounts or dedicated volumes)

  ---
  One alternative the research flagged: Managed services like E2B or Hopx are purpose-built for running AI agents in Firecracker VMs. If "buy vs build" is on the table, they solve this problem out of the box. But if you want to own the infrastructure, Flintlock + custom orchestration is the path.

  Does Flintlock sound right, or do you want to explore the managed options first?

> i do not want to explore managed options. I want you to have a look at my 
packnplay project. git clone https://github.com/obra/packnplay - It's for a 
similar but different workflow. *local* devcontainers for agentic dev. but 
using full-on docker or orbstack. it does a lot of the permissions and env 
config (but no 1pw integration) it sets up a standard container with the 
coding agents. it has no filesystem snapshot magic. it's intended to mount a
 local path into the container. but it probably has stuff we should at least
 steal unless there's a reason to adapt it. clone it into a gitignored work 
directory and have a subagent explore it for you 

● Bash(mkdir -p /home/jesse/git/stockyard/work && cd
      /home/jesse/git/stockyard/work && git clone
      https://github.com/obra/packnplay)
  ⎿  Cloning into 'packnplay'...

● Bash(echo "work/" >> /home/jesse/git/stockyard/.gitignore)
  ⎿  (No content)

● Explore(Explore packnplay project)
  ⎿  Done (29 tool uses · 92.8k tokens · 1m 37s)                           

● Excellent analysis. Here's the executive summary of what we can steal from packnplay:

  Steal As-Is (No Changes)

  - User detection system (pkg/userdetect/) - Priority-based detection with caching by image hash
  - Agent abstraction (pkg/agents/) - Interface for Claude, Codex, Gemini, etc. with per-agent config directories
  - Configuration architecture (pkg/config/) - JSON config + BubbleTea TUI + CLI flag overrides
  - AWS credential_process handling (pkg/aws/) - Works with SSO, granted.dev, aws-vault
  - Git worktree management (pkg/git/) - XDG-compliant paths, branch-aware
  - DevContainer parser (pkg/devcontainer/) - 100% spec compliance
  - CLI structure - Cobra framework, same subcommands (run, attach, stop, list)

  Adapt (Keep Interface, Change Backend)

  - Mount builder - Same abstraction, but output VM mount instructions instead of Docker -v flags
  - Credential mounting pattern - Same sources (SSH, GH, GPG, etc.), different injection mechanism (cloud-init vs. Docker mounts)
  - Port mapping syntax - Parse same syntax, implement via VM networking

  Replace Completely

  - Docker client → Flintlock gRPC client
  - Image handling → OCI images via containerd (Flintlock handles this)
  - TTY/terminal → SSH pseudo-terminal

  ---
  This changes the picture a bit. Packnplay already has solid Go code for most of the non-container-specific logic.

  Next brainstorming question:

  Should stockyard be:

  A. A fork/adaptation of packnplay - Start from packnplay, swap out the Docker layer for Flintlock, reuse everything else

  B. A new project that imports packnplay packages - Keep packnplay as a dependency, import pkg/agents, pkg/config, pkg/userdetect, etc. and build the VM layer fresh

  C. A fresh codebase that copies patterns - Start clean, manually port the patterns worth stealing, avoid carrying packnplay's Docker-specific baggage

  My lean: B if packnplay's packages are cleanly separated, C if there's too much Docker coupling in the reusable parts. Want me to check how coupled the packages are?

> I am fine with A or C. B is a non-starter 

● Got it - no dependency relationship. Let me check how tangled the reusable parts are with Docker to make a recommendation between A and C.

● Explore(Analyze Docker coupling)
  ⎿  Done (34 tool uses · 58.1k tokens · 1m 34s)                           

● Good news: Fork (A) is the right call.

  Coupling Summary

  | Package              | Docker Coupling    | Verdict        |
  |----------------------|--------------------|----------------|
  | agents               | None               | Use as-is      |
  | aws                  | None               | Use as-is      |
  | git                  | None               | Use as-is      |
  | devcontainer         | None               | Use as-is      |
  | config               | 1 subprocess call  | Trivial fix    |
  | userdetect           | Subprocess calls   | ~2hr refactor  |
  | runner/mount_builder | String format only | ~2hr refactor  |
  | runner (main)        | Heavy              | Main work area |

  The runner package is the only real work. It already has DockerClient interface abstractions in ImageManager and LifecycleExecutor - the pattern exists, it just wasn't applied consistently. We'd generalize that to a VMRuntime interface and implement it for Flintlock.

  Estimated migration effort: ~4-5 days to have a working Flintlock backend

  ---
  So we've landed on:
  - Runtime: Flintlock (Firecracker micro-VMs)
  - Secrets: 1Password CLI now, abstracted for AWS Secrets Manager later
  - Language: Go
  - Starting point: Fork packnplay, replace Docker layer with Flintlock

  ---
  Next question: The project is currently called "stockyard" (based on the directory). Is that the name, or do you want something else? Names matter for:
  - CLI binary name (stockyard run vs corral run vs whatever)
  - Go module path
  - Repo/package identity

  Do you have a name in mind, or should we stick with stockyard?

> stockyard is the name. 

● Stockyard it is. Good name - fits the "managing a herd of agents" vibe.

  ---
  Next question: You mentioned SSH access with possibly Tailscale magic authentication. Let me understand the access model:

  Who connects to these VMs, and how?

  A. Developer → VM directly: Developer SSHs into the VM to watch the agent work, debug, or intervene. Tailscale gives each VM an IP on your tailnet.

  B. CLI-mediated access: Developer runs stockyard attach <vm> which handles the SSH connection under the hood. No direct network exposure needed.

  C. Both: VMs are on the tailnet for direct access, but CLI also provides convenience commands for common operations.

  And for the Tailscale piece specifically:

  - Should each VM join the tailnet automatically on startup (requires Tailscale auth key injection)?
  - Or is this optional/configurable per deployment?

  This affects how we handle networking setup (which Flintlock doesn't do for us).

> C. and...maybe we have each vm join the tailnet automatically 

● Good - that gives us a clean model. VMs join the tailnet, get a hostname like stockyard-<task-id>.tail-abc123.ts.net, and developers can either:
  - SSH directly: ssh stockyard-task-42
  - Use CLI: stockyard attach task-42 (convenience wrapper)

  For the Tailscale auth key, that's another secret we'll pull from 1Password and inject via cloud-init at VM startup.

  ---
  Next question: You were emphatic about not losing 36 hours of work. Let's nail down the persistent storage strategy.

  Flintlock/Firecracker gives us block devices. The question is what to persist and how:

  Option A: Whole workspace volume
  - VM gets a dedicated persistent volume mounted at /workspace (or similar)
  - Git clone goes there, all agent work happens there
  - Volume survives VM restarts/crashes
  - Simple but volumes accumulate

  Option B: Workspace + periodic snapshots
  - Same as A, plus we snapshot the volume periodically (e.g., every 30 min)
  - Can restore to earlier state if something goes wrong
  - More complex, uses more storage

  Option C: Git is the persistence layer
  - Agent is configured to commit and push frequently (every N minutes or on checkpoints)
  - VM workspace is ephemeral - if it dies, we re-clone and the only loss is uncommitted work
  - Relies on agent discipline, but work escapes to GitHub fast

  Option D: A + C combined
  - Persistent volume for crash protection
  - Agent also pushes frequently so work is replicated off-machine
  - Belt + suspenders

  My lean: D - the volume catches crashes, frequent pushes replicate work to GitHub. Snapshots (B) feel like overkill for this use case.

  What's your preference?

> how expensive are snapshots? 

● Depends on what kind of "expensive" you mean:

  Storage cost:
  - Firecracker snapshots are copy-on-write - the initial snapshot is essentially free (just metadata)
  - Subsequent changes accumulate as diffs
  - For a coding workspace that's mostly text files, we're talking megabytes per snapshot, not gigabytes
  - 100 snapshots of a 10GB workspace might only use 10.5GB total if changes are small

  Time cost:
  - Fly.io reports snapshot creation takes "hundreds of milliseconds"
  - Memory snapshot + block device flush
  - Non-blocking to the VM (can snapshot while agent keeps working)

  Complexity cost:
  - This is the real expense
  - Memory file must remain immutable after snapshot
  - Network connections don't survive restore (agent would need to reconnect to APIs)
  - Snapshot format versioning - old snapshots may not restore on newer Firecracker versions
  - We'd need to manage snapshot lifecycle (retention, cleanup, indexing)

  My revised take:

  If snapshots are mostly cheap on storage and time, the question is whether the complexity is worth it. For "I want to restore to 30 minutes ago because the agent went off the rails" - snapshots are the only way to do that.

  Simpler alternative: Just snapshot on-demand via CLI (stockyard snapshot task-42) rather than automatic periodic snapshots. Gives you the escape hatch without the complexity of managing automatic snapshot schedules and retention.

  Want automatic periodic snapshots, on-demand only, or skip them entirely for v1?

> What I'm actually wondering about is how hard it would be for us to allow 
the coding agents to do really frequent snapshots for later auditing, like 
in between every tool call. 

● Ah, that's a very different use case - audit trail / replay, not crash recovery.

  For that frequency (potentially hundreds of snapshots per hour), full Firecracker VM snapshots are the wrong tool. They capture memory + block device state, which is overkill when you just want "what did the filesystem look like after this tool call?"

  Better options for high-frequency workspace snapshots:

  1. Git commits (simplest)
  - After each tool call, auto-commit with metadata: [audit] tool=Edit file=src/foo.ts timestamp=...
  - Git is literally designed for this - efficient, diffable, searchable
  - Downside: pollutes git history (but could use a separate audit branch or orphan commits)

  2. Filesystem-level snapshots (ZFS/btrfs)
  - If the workspace volume uses ZFS or btrfs, snapshots are:
    - Nearly instant (milliseconds)
    - Almost free (copy-on-write, only changed blocks cost space)
    - Unlimited (can have thousands)
  - zfs snapshot workspace@tool-call-1234 takes <10ms
  - This is what I'd recommend for this use case

  3. Overlay filesystem layers
  - Each "snapshot" is a frozen overlay layer
  - New writes go to a new layer
  - Can stack hundreds of layers
  - Similar efficiency to ZFS but more DIY

  My recommendation:

  Format the persistent workspace volume as ZFS (or btrfs). Then snapshotting between tool calls is trivial:

  zfs snapshot tank/stockyard/task-42@$(date +%s)-tool-call-edit

  ~5ms, near-zero storage overhead for small changes, and you get full diff capability between any two snapshots.

  Does ZFS/btrfs on the host work for your setup? Or do you need something that works with plain ext4?

> zfs is fine. "when you say "zfs on the host" does this mean I want a new 
block volume on the host? (If that's the requirement, that's the 
requirement. ) 

● Good question - let me clarify the architecture:

  Option 1: ZFS pool on host, workspaces shared into VMs
  - Host (flower-garden) has a ZFS pool (could be a dedicated disk, partition, or even a file-backed pool for prototyping)
  - Each VM's workspace is a ZFS dataset: tank/stockyard/task-42
  - Workspace is shared into the VM via virtio-fs or 9pfs (Firecracker supports both)
  - Snapshots happen on the host: zfs snapshot tank/stockyard/task-42@tool-call-1234
  - Pros: One pool to manage, centralized snapshots, efficient
  - Cons: Shared filesystem into VM (slight complexity)

  Option 2: Each VM gets a zvol as a block device
  - Host has ZFS pool
  - Each VM workspace is a zvol (ZFS volume = block device): tank/stockyard/task-42-vol
  - zvol attached to VM as virtio-blk, VM formats it as ext4
  - Snapshots still happen on host (zvol snapshots)
  - Pros: VM sees a normal block device, simpler from VM perspective
  - Cons: Can't snapshot individual files, just whole volume

  Option 3: ZFS inside each VM
  - Each VM has its own ZFS pool
  - Pros: Fully isolated
  - Cons: ZFS overhead per VM, more complex image, wasteful

  ---
  My recommendation: Option 1 (ZFS pool on host, virtio-fs into VMs)

  For the host setup, you have choices:
  - Dedicated disk: Best performance, but requires spare disk
  - Partition on existing disk: Good balance
  - File-backed pool (zpool create tank /var/lib/stockyard/zfs.img): Works fine for prototyping, can migrate to real disk later

  For prototyping on flower-garden, a file-backed pool gets you started with zero hardware changes. You can always migrate to a dedicated disk later if performance matters.

  Does that architecture make sense? And do you want to start with a file-backed pool or do you have a spare disk to dedicate?

> ok. great. i'll see if i can find a disk. start with file backed. Will 
there be a way from inside the container to snapshot? 

● Yes - and this is a key design point. The VM needs a way to signal "snapshot now" to the host.

  Best option: virtio-vsock

  Firecracker has built-in support for vsock - a virtual socket for host↔guest communication that doesn't require network setup.

  How it would work:
  1. Stockyard daemon on host listens on a vsock port
  2. VM has a tiny client (stockyard-snapshot or similar) that connects to host via vsock
  3. Agent (or Claude Code hook) runs: stockyard-snapshot "tool-call-edit-src-foo"
  4. Host daemon receives request, runs zfs snapshot tank/stockyard/task-42@tool-call-edit-src-foo
  5. Returns success/failure to VM

  The UX from inside the VM:
  # Simple command the agent or hook can call
  stockyard-snapshot "after-edit-main.py"

  # Or with automatic naming
  stockyard-snapshot  # auto-generates timestamp-based name

  Integration with Claude Code:

  Claude Code has a hooks system. We could ship a hook that auto-snapshots:
  {
    "hooks": {
      "PostToolUse": [{
        "command": "stockyard-snapshot \"$TOOL_NAME-$TOOL_ID\""
      }]
    }
  }

  This gives you automatic snapshots after every tool call with zero agent effort.

  ---
  Summary of the snapshot architecture:
  - Host: ZFS pool + stockyard daemon listening on vsock
  - VM: stockyard-snapshot CLI that talks to host via vsock
  - Trigger: Claude Code PostToolUse hook (or manual)
  - Result: Near-instant, near-free snapshots with full audit trail

  Sound good?

> great 

● We've covered a lot of ground. Let me check if there's anything else critical before I write up the design.

  What we've decided:
  - Name: stockyard
  - Runtime: Flintlock + Firecracker micro-VMs
  - Language: Go (fork of packnplay)
  - Secrets: 1Password CLI → abstract for AWS Secrets Manager later
  - Task input: Git-centric (repo + branch + task) or GitHub issue
  - Task output: Push to remote frequently + persistent ZFS volume as backup
  - Access: Tailscale (VMs auto-join tailnet) + CLI convenience commands
  - Storage: ZFS pool on host (file-backed initially), shared via virtio-fs
  - Snapshots: vsock API to host, triggered by Claude Code hooks for audit trail

  One more question: How does the developer specify what the agent should work on?

  A. CLI flag: stockyard run --repo github.com/foo/bar --branch feature-x --task "implement auth"

  B. GitHub issue URL: stockyard run --issue https://github.com/foo/bar/issues/42 (agent reads issue and figures out repo/branch)

  C. Interactive: stockyard run prompts for repo, branch, task description

  D. Config file: .stockyard.yaml in the repo defines default tasks/setup

  Or some combination? This affects the CLI design and what gets passed to the agent at startup.

> None of this feels quite right. Don't forget the things running inside it 
are agentic. A is probably closest. I think we specify a repo, a branch or 
other identifier and a Unix command line for the coding agent, which may or 
may not have a prompt on it. 

● You're right - I was overcomplicating it. We're infrastructure, not a task manager. The agent is agentic; we just set up the environment and run it.

  So something like:

  stockyard run --repo github.com/obra/foo --ref feature-x -- claude-code --dangerously-skip-permissions -p "implement the auth system"

  Or without a prompt (agent figures it out from context):

  stockyard run --repo github.com/obra/foo --ref issue-42 -- claude-code --dangerously-skip-permissions

  Or a different agent entirely:

  stockyard run --repo github.com/obra/foo --ref main -- codex "fix the tests"

  Stockyard's job:
  1. Spin up VM
  2. Clone repo at ref
  3. Inject credentials (GitHub, API keys, etc.)
  4. Run the specified command
  5. Provide infrastructure (snapshots, persistence, Tailscale access, log extraction)

  Not stockyard's job:
  - Understanding the task
  - Parsing prompts
  - Managing agent behavior

  The -- delimiter separates stockyard's flags from the command+args to run. Clean Unix semantics.

  Does that match your mental model?

> great 

● I think we have enough to write up the design. Following the brainstorming process, I'll present it in sections for validation.

  ---
  Stockyard Design - Section 1: Overview & Architecture

  Stockyard is a CLI tool for running coding agents (Claude Code, Codex, etc.) in isolated Firecracker micro-VMs with full audit trail capability.

  Core architecture:

  ┌─────────────────────────────────────────────────────────────┐
  │                     Host (flower-garden)                     │
  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐  │
  │  │  stockyard  │  │  flintlockd │  │    ZFS Pool         │  │
  │  │    CLI      │──│   (gRPC)    │  │  tank/stockyard/    │  │
  │  └─────────────┘  └─────────────┘  │    ├── task-1/      │  │
  │         │              │           │    ├── task-2/      │  │
  │         │              │           │    └── ...          │  │
  │         │         ┌────▼────┐      └─────────────────────┘  │
  │         │         │ Micro-VM │◄───── virtio-fs ─────────────┤
  │         └────────►│ (FC)     │◄───── vsock (snapshots) ─────┤
  │                   │          │◄───── Tailscale ─────────────┤
  │                   └──────────┘                              │
  └─────────────────────────────────────────────────────────────┘

  Key components:
  - stockyard CLI: User interface for managing VMs
  - Flintlock: Manages Firecracker VM lifecycle via gRPC
  - ZFS pool: Persistent workspace storage with instant snapshots
  - virtio-fs: Shares workspace from host ZFS into VM
  - vsock: Host↔VM communication channel for snapshot triggers
  - Tailscale: VMs auto-join tailnet for direct SSH access

  Does this high-level architecture look right before I continue to the next section?

> Sure! 

● Stockyard Design - Section 2: CLI Interface

  Primary commands:

  # Start a VM with a coding agent
  stockyard run --repo github.com/org/repo --ref branch-name -- claude-code [args...]

  # List running VMs
  stockyard list

  # Attach to a running VM (SSH wrapper)
  stockyard attach <task-id>

  # Stop a VM (workspace persists)
  stockyard stop <task-id>

  # Stop and remove workspace
  stockyard destroy <task-id>

  # Manual snapshot
  stockyard snapshot <task-id> [label]

  # List snapshots for a task
  stockyard snapshots <task-id>

  # Restore to a snapshot
  stockyard restore <task-id> <snapshot>

  # Pull logs/files from VM
  stockyard logs <task-id>
  stockyard cp <task-id>:/path/to/file ./local/path

  # Interactive configuration (TUI, inherited from packnplay)
  stockyard configure

  Run command flags:
  stockyard run \
    --repo github.com/org/repo      # Required: git repo to clone
    --ref main                       # Branch, tag, or commit (default: main)
    --name my-task                   # Optional: human-readable name
    --cpus 2                         # CPU cores (default: 2)
    --memory 4G                      # RAM (default: 4G)
    --no-tailscale                   # Skip Tailscale join
    --env KEY=value                  # Pass environment variables
    --                               # Delimiter
    claude-code [args...]            # Command to run

  Does this CLI surface feel right?

> sure 

● Stockyard Design - Section 3: Secrets & Credential Management

  Secrets flow:

  ┌──────────────┐     ┌─────────────┐     ┌─────────────┐
  │  1Password   │────►│  stockyard  │────►│  cloud-init │────► VM
  │  (or AWS SM) │     │   CLI       │     │  user-data  │
  └──────────────┘     └─────────────┘     └─────────────┘

  Secrets pulled at VM startup:
  - ANTHROPIC_API_KEY - Claude API access
  - OPENAI_API_KEY - OpenAI/Codex access (if needed)
  - GITHUB_TOKEN - Git operations, PR creation
  - TAILSCALE_AUTH_KEY - Auto-join tailnet
  - SSH keys - For git operations

  1Password integration:
  # Secrets referenced by 1Password path
  stockyard configure
  # → Prompts for 1Password vault paths:
  #   Anthropic API Key: op://Private/Anthropic/api-key
  #   GitHub Token: op://Private/GitHub/token
  #   Tailscale Auth: op://Private/Tailscale/stockyard-auth-key

  Abstraction layer:
  type SecretProvider interface {
      GetSecret(name string) (string, error)
  }

  // Implementations:
  // - OnePasswordProvider (v1)
  // - AWSSecretsManagerProvider (future)

  Injection method:
  - Secrets fetched on host at VM start time
  - Injected via cloud-init user-data as environment variables
  - Never written to disk in VM (env vars only)

  Does this secrets model work?

> I think all of those keys want to be clearly Stockyard keys and they may 
want some kind of Stockyard instance identifier so that the keys for the 
Flower Garden Stockyard instance and the keys for the AWS Stockyard instance
 are different. 

● Good call. Namespaced and instance-scoped secrets:

  Revised secrets model:

  1Password Vault Structure:
  op://Stockyard/
    ├── flower-garden/           # Instance identifier
    │   ├── anthropic-api-key
    │   ├── openai-api-key
    │   ├── github-token
    │   ├── tailscale-auth-key
    │   └── ssh-private-key
    └── aws-prod/                # Future AWS instance
        ├── anthropic-api-key
        ├── ...

  Instance configuration:
  # First-time setup names the instance
  stockyard init --instance flower-garden

  # Stores in ~/.config/stockyard/config.json
  {
    "instance_id": "flower-garden",
    "secrets": {
      "provider": "1password",
      "vault": "Stockyard",
      "prefix": "flower-garden"  # op://Stockyard/flower-garden/*
    }
  }

  Secret resolution:
  // Request for "anthropic-api-key" becomes:
  // op://Stockyard/{instance_id}/anthropic-api-key
  // e.g., op://Stockyard/flower-garden/anthropic-api-key

  Benefits:
  - Clear audit trail per instance
  - Different API keys per environment (cost tracking, rate limits)
  - No cross-contamination between dev/prod
  - Easy to revoke one instance's access without affecting others

  Does this namespacing approach work?

> Great. 

● Stockyard Design - Section 4: Storage & Snapshots

  ZFS layout on host:

  tank/stockyard/                    # Pool root
    ├── workspaces/                  # Active task workspaces
    │   ├── task-abc123/             # Dataset per task
    │   │   └── repo/                # Cloned git repo
    │   └── task-def456/
    └── images/                      # Cached VM base images

  Snapshot architecture:

  Host                                    VM
  ┌─────────────────────┐                ┌──────────────────────┐
  │  stockyard daemon   │◄───vsock──────►│  stockyard-snapshot  │
  │  (listens CID 3)    │                │  (tiny CLI client)   │
  │         │           │                └──────────────────────┘
  │         ▼           │                         │
  │  zfs snapshot       │                         │
  │  tank/.../task@name │                   Called by Claude Code
  └─────────────────────┘                   PostToolUse hook

  Snapshot naming:
  tank/stockyard/workspaces/task-abc123@2024-01-16T14:32:01-edit-src-main.py
  tank/stockyard/workspaces/task-abc123@2024-01-16T14:32:45-bash-npm-test
  tank/stockyard/workspaces/task-abc123@2024-01-16T14:33:12-manual-checkpoint

  Claude Code hook (auto-installed in VM):
  {
    "hooks": {
      "PostToolUse": [{
        "command": "stockyard-snapshot \"$CLAUDE_TOOL_NAME\""
      }]
    }
  }

  Snapshot retention:
  - Keep all snapshots by default (storage is cheap with CoW)
  - stockyard snapshots <task-id> --prune --keep 100 for cleanup
  - Snapshots deleted when task is destroyed

  Does this snapshot model cover the audit trail use case?

> I think the snapshot model is great. The ZFS layout I want you to talk to 
me a little bit more about. 

● Good - let me dig in. A few questions about the layout:

  Dataset granularity:

  Option A: One dataset per task
  tank/stockyard/workspaces/task-abc123/    # Dataset
    └── repo/                                # Directory inside
  - Snapshots capture entire task workspace
  - Simple, one snapshot command per task

  Option B: One dataset per repo clone
  tank/stockyard/workspaces/task-abc123/repo/  # Dataset
  - More granular if a task ever has multiple repos
  - Probably overkill?

  Pool location:

  For file-backed pool:
  # Option 1: Under /var/lib
  /var/lib/stockyard/zpool.img

  # Option 2: Dedicated mount point
  /stockyard/zpool.img

  Other considerations:

  - Quotas: Should each task have a max size? (e.g., 50GB per workspace)
  - Compression: ZFS compression (lz4) is essentially free and saves space
  - The images/ dataset: Is this for caching VM base images, or do we let Flintlock/containerd handle that?

  What aspects are you thinking about? Or is there a different structure you had in mind?

> A task corresponds to a single Firecracker VM, right? We want one dataset 
per task, and I think we want the external system (i.e. Stockyard) to not 
really know anything about how the file system is laid out internally? Does 
that seem right? 

● Yes, that's cleaner. Task = VM = dataset, and stockyard is agnostic about what's inside.

  Simplified model:

  tank/stockyard/
    └── workspaces/
        ├── task-abc123/     # Dataset, mounted into VM at /workspace
        ├── task-def456/     # Another dataset
        └── ...

  Stockyard's view:
  - Create dataset when task starts
  - Mount it into VM via virtio-fs at a known path (e.g., /workspace)
  - Snapshot the dataset when asked
  - Destroy dataset when task is destroyed
  - That's it

  VM's view:
  - "I have a persistent /workspace directory"
  - Clone repos there, create files, whatever
  - Don't care how it's implemented

  What stockyard does NOT know:
  - Whether there's one repo or five
  - Directory structure inside /workspace
  - What the agent is doing with the files

  The only contract is: /workspace is persistent, snapshotable, and survives VM restarts.

  Does that match what you're thinking?

> Awesome. 

● Stockyard Design - Section 5: VM Base Image

  Pre-installed in the base image:

  Base: Ubuntu 24.04 (minimal cloud image)

  Core tools:
  - Git, SSH client
  - Build essentials (gcc, make, etc.)
  - Common languages: Node.js LTS, Python 3.11+, Go, Rust
  - Editors: vim, nano

  Coding agents:
  - Claude Code (claude-code)
  - Codex CLI (codex)
  - Other agents as needed

  Stockyard integration:
  - stockyard-snapshot (vsock client for triggering snapshots)
  - Tailscale (for auto-join)
  - Claude Code hooks pre-configured for auto-snapshots

  Cloud CLIs (optional, could trim):
  - gh (GitHub CLI)
  - aws cli

  Image management:

  - Built with Packer (or similar) from a Dockerfile-like spec
  - Stored in containerd (Flintlock pulls from there)
  - Versioned: ghcr.io/obra/stockyard-vm:latest
  - Can reuse packnplay's existing Dockerfile as starting point - similar requirements

  Customization path:

  Users could eventually specify custom images:
  stockyard run --image ghcr.io/myorg/custom-vm:v1 --repo ... -- command

  But v1 ships with one opinionated image that works.

  Does this image approach work, or do you want it leaner/fatter?

> instead, start with the packnplay container image. customize from there. 

● Makes sense - packnplay's image already has the agents, tools, and environment dialed in. No need to redo that work.

  Image approach:

  packnplay devcontainer image (OCI)
    └── ghcr.io/obra/packnplay/devcontainer:latest
          │
          ▼ Convert/extend for VM boot
          │
  stockyard VM image
    └── Add:
        - Kernel (vmlinux)
        - Init system (systemd or simple init)
        - stockyard-snapshot (vsock client)
        - Tailscale daemon
        - virtio-fs/9p mount setup
        - Cloud-init for credential injection

  Flintlock compatibility:

  Flintlock can boot OCI images via containerd - it extracts the rootfs and boots it with a kernel. So we may be able to use the packnplay image nearly directly, just adding a kernel and the stockyard-specific bits as an additional layer.

  Build process:
  FROM ghcr.io/obra/packnplay/devcontainer:latest

  # Add stockyard VM requirements
  RUN apt-get install -y cloud-init tailscale
  COPY stockyard-snapshot /usr/local/bin/
  COPY claude-hooks.json /etc/stockyard/

  # VM boot setup
  COPY init-scripts/ /etc/init.d/

  This way packnplay improvements flow downstream automatically.

  Sound right?

> sorry. I mean you should go steal the raw dockerfile we have today and 
then adopt it and customize it 

● Got it - copy the Dockerfile into stockyard and own it, not layer on top. Same approach as the Go code: fork and evolve independently.

  Image approach (revised):

  stockyard/
    └── vm-image/
        ├── Dockerfile          # Copied from packnplay, customized
        ├── kernel/             # Linux kernel for Firecracker
        └── scripts/
            ├── stockyard-snapshot
            └── init-setup.sh

  What we copy from packnplay:
  - Base image selection (Ubuntu)
  - Language installs (Node, Python, Go, Rust)
  - Coding agent installs (Claude Code, Codex, etc.)
  - Dev tooling (git, gh, build-essential, etc.)

  What we add/change:
  - Cloud-init for credential injection
  - Tailscale daemon
  - stockyard-snapshot vsock client
  - Claude Code hooks for auto-snapshots
  - VM boot requirements (kernel, init)
  - Remove anything container-specific that doesn't apply

  What we might trim:
  - Cloud CLIs if not needed (AWS, Azure, GCloud)
  - Anything that bloats the image unnecessarily

  This way we control the full image and can optimize for VM boot time.

  Sound right?

> great. 

● Stockyard Design - Section 6: Networking & Tailscale

  Network architecture:

  ┌─────────────────────────────────────────────────────────┐
  │  Host (flower-garden)                                    │
  │                                                          │
  │  ┌──────────┐    ┌──────────┐    ┌──────────┐          │
  │  │ VM task-1│    │ VM task-2│    │ VM task-3│          │
  │  │ tap0     │    │ tap1     │    │ tap2     │          │
  │  └────┬─────┘    └────┬─────┘    └────┬─────┘          │
  │       │               │               │                 │
  │       └───────────────┴───────────────┘                 │
  │                       │                                  │
  │                   NAT/bridge                             │
  │                       │                                  │
  │               host network + internet                    │
  └─────────────────────────────────────────────────────────┘

  Each VM gets:
  - TAP device on host (Flintlock manages this)
  - Private IP via DHCP or static assignment
  - NAT to internet (for git clone, API calls, package installs)
  - Tailscale interface (joins tailnet at boot)

  Tailscale setup:
  # At VM boot (via cloud-init):
  tailscale up --authkey=${TAILSCALE_AUTH_KEY} --hostname=stockyard-${TASK_ID}

  Access paths:
  # Direct via Tailscale (from anywhere on tailnet)
  ssh stockyard-task-abc123

  # Via CLI (from host)
  stockyard attach task-abc123  # Wraps SSH

  Firewall considerations:
  - VMs can reach internet (outbound)
  - VMs reachable via Tailscale (inbound from tailnet only)
  - No direct inbound from public internet

  Does this networking model work?

> great 

● Stockyard Design - Section 7: Fork & Migration Path from packnplay

  What we fork:

  packnplay/                    stockyard/
  ├── cmd/                  →   ├── cmd/
  │   ├── run.go                │   ├── run.go (adapt)
  │   ├── attach.go             │   ├── attach.go (adapt)
  │   ├── list.go               │   ├── list.go (adapt)
  │   ├── stop.go               │   ├── stop.go (adapt)
  │   └── configure.go          │   ├── configure.go (adapt)
  │                             │   ├── snapshot.go (new)
  │                             │   └── restore.go (new)
  ├── pkg/
  │   ├── agents/           →   │   ├── agents/ (as-is)
  │   ├── aws/              →   │   ├── aws/ (as-is)
  │   ├── git/              →   │   ├── git/ (as-is)
  │   ├── config/           →   │   ├── config/ (adapt)
  │   ├── devcontainer/     →   │   ├── devcontainer/ (as-is)
  │   ├── userdetect/       →   │   ├── userdetect/ (adapt)
  │   ├── runner/           →   │   ├── runner/ (heavy adapt)
  │   ├── docker/           ✗   │   ├── flintlock/ (new)
  │   └── compose/          ✗   │   ├── zfs/ (new)
  │                             │   ├── secrets/ (new - 1pw abstraction)
  │                             │   ├── tailscale/ (new)
  │                             │   └── vsock/ (new - snapshot API)
  └── .devcontainer/        →   └── vm-image/ (adapt to Dockerfile)

  New abstractions to create:

  // pkg/runtime/runtime.go
  type VMRuntime interface {
      Create(config VMConfig) (VM, error)
      Start(id string) error
      Stop(id string) error
      Destroy(id string) error
      List() ([]VM, error)
      Exec(id string, cmd []string) error
  }

  // pkg/secrets/provider.go
  type SecretProvider interface {
      GetSecret(name string) (string, error)
  }

  // pkg/snapshots/snapshots.go
  type SnapshotManager interface {
      Create(taskID, label string) error
      List(taskID string) ([]Snapshot, error)
      Restore(taskID, snapshotName string) error
  }

  Migration order:
  1. Fork repo, rename module to github.com/obra/stockyard
  2. Gut pkg/docker/, create pkg/flintlock/ implementing VMRuntime
  3. Add pkg/secrets/, pkg/zfs/, pkg/vsock/
  4. Adapt pkg/runner/ to use new abstractions
  5. Update CLI commands
  6. Build VM image from adapted Dockerfile

  Does this migration approach make sense?

> I don't think you want a proper fork. I think you should just study the 
code and then implement the right thing from scratch 

● You're right - that's cleaner. Study packnplay for patterns, implement stockyard fresh. No git lineage, no baggage.

  Stockyard Design - Section 7 (Revised): Implementation Approach

  Reference packnplay for:
  - Agent abstraction pattern (pkg/agents/)
  - AWS credential_process handling (pkg/aws/)
  - Configuration TUI patterns (pkg/config/)
  - DevContainer parsing if we want that later (pkg/devcontainer/)
  - CLI structure and UX

  Implement fresh:
  stockyard/
  ├── cmd/
  │   ├── root.go
  │   ├── run.go
  │   ├── attach.go
  │   ├── list.go
  │   ├── stop.go
  │   ├── destroy.go
  │   ├── snapshot.go
  │   ├── snapshots.go
  │   ├── restore.go
  │   ├── logs.go
  │   ├── cp.go
  │   └── configure.go
  ├── pkg/
  │   ├── flintlock/      # VM lifecycle via Flintlock gRPC
  │   ├── zfs/            # ZFS dataset/snapshot management
  │   ├── secrets/        # 1Password (+ future AWS SM)
  │   ├── vsock/          # Host↔VM communication
  │   ├── tailscale/      # Tailscale auth key injection
  │   ├── config/         # Configuration management
  │   └── agents/         # Agent definitions (steal pattern from packnplay)
  └── vm-image/
      └── Dockerfile      # Based on packnplay's, customized

  Benefit: Right abstractions from day one, no refactoring Docker assumptions out of inherited code.

  Does this approach work better?

> ok. 

● Stockyard Design - Section 8: Logging & Telemetry

  Log sources:

  VM
  ├── /var/log/cloud-init.log      # Boot/credential injection
  ├── /var/log/tailscale.log       # Network join
  ├── Agent output                  # Captured from command stdout/stderr
  └── /workspace/.claude/           # Claude Code's own logs (if any)

  Log capture strategy:

  # stockyard run captures agent stdout/stderr
  stockyard run --repo ... -- claude-code "do stuff"
           │
           └── Streams to terminal AND saves to host:
               /var/lib/stockyard/logs/task-abc123/agent.log

  Log retrieval:

  # Stream logs from running VM
  stockyard logs task-abc123

  # Stream and follow
  stockyard logs -f task-abc123

  # Get boot/system logs
  stockyard logs --system task-abc123

  # Copy specific files
  stockyard cp task-abc123:/workspace/.claude/logs ./local-dir/

  Telemetry (future consideration):
  - Agent token usage (if extractable)
  - Snapshot frequency/count
  - Task duration
  - Git activity (commits, pushes)

  For v1, focus on logs. Telemetry can come later when we know what metrics matter.

  Does this cover the logging needs you mentioned?

> ok. 

● Stockyard Design - Section 9: V1 Scope & Out of Scope

  V1 delivers:
  - Single-host deployment (flower-garden)
  - stockyard run/attach/list/stop/destroy commands
  - stockyard snapshot/snapshots/restore commands
  - Flintlock + Firecracker VMs
  - ZFS workspace persistence + instant snapshots
  - 1Password secrets integration
  - Tailscale auto-join
  - vsock snapshot trigger from VM
  - Claude Code auto-snapshot hooks
  - Basic logging (stockyard logs)

  Out of scope for V1 (future):
  - Multi-host / AWS deployment
  - AWS Secrets Manager integration
  - Web UI / dashboard
  - Task queuing (more tasks than VM slots)
  - Cost tracking / API usage telemetry
  - Custom VM images per task
  - DevContainer spec support (packnplay has this, defer for stockyard)
  - Multi-agent coordination

  Open questions (can defer):
  - Exact Flintlock configuration details (discover during implementation)
  - VM resource defaults (start with 2 CPU / 4GB RAM, tune later)
  - Snapshot retention policy defaults

  ---
  That's the full design. Does this cover everything, or is there something I've missed? If it looks good, I'll write it to docs/plans/ and commit it.

> One more thing that I forgot. I think we probably want to have an 
authenticated web services API for everything stockyard can do, along with a
 web ui with dashboards and a control plane. I'm fine with that being a 
later phase of the project. i just want you to think hard about whether that
 changes your design. 

● Good catch - this does change the design. If there's a web API, the CLI shouldn't embed all the logic; it should be a client to a daemon.

  Revised architecture:

                      ┌─────────────────────────────────────────┐
                      │           stockyard daemon              │
  CLI ───────────────►│  ┌─────────────────────────────────┐   │
                      │  │  gRPC/REST API (authenticated)   │   │
  Web UI ────────────►│  └─────────────────────────────────┘   │
                      │         │                               │
  Remote API ────────►│         ▼                               │
                      │  ┌─────────────┐  ┌──────────────┐     │
                      │  │ Task Manager│  │ State (SQLite)│     │
                      │  └─────────────┘  └──────────────┘     │
                      │         │                               │
                      │    ┌────┴────┬──────────┐              │
                      │    ▼         ▼          ▼              │
                      │ Flintlock   ZFS    Tailscale           │
                      │  (VMs)   (snapshots)  (auth)           │
                      └─────────────────────────────────────────┘

  What changes:

  1. Daemon is the control plane - stockyardd runs on host, manages everything
  2. CLI is a thin client - stockyard run calls the daemon's API
  3. State in SQLite - Tasks, snapshots, logs metadata persisted (not just ephemeral)
  4. API-first design - Every operation is an API call; CLI and Web UI are equal clients
  5. Authentication layer - API keys, or Tailscale identity, or both

  What stays the same:
  - ZFS for storage/snapshots
  - Flintlock for VMs
  - vsock for in-VM snapshot triggers (talks to daemon)
  - 1Password for secrets
  - Tailscale for VM networking

  V1 adjusted:
  - Build the daemon from day one
  - CLI talks to daemon (local socket for v1, can go remote later)
  - Web UI deferred, but API is there when we want it

  Does this daemon-centric architecture make sense?

> ok. 

● Let me write up the complete design document.

● Bash(mkdir -p /home/jesse/git/stockyard/docs/plans)
  ⎿  (No content)

● Write(~/git/stockyard/docs/plans/2026-01-16-stockyard-design.md)
  ⎿  Wrote 484 lines to ../docs/plans/2026-01-16-stockyard-design.md     
      1 # Stockyard Design
      2 Stockyard is a CLI and daemon for running coding agents (Claude C
        ode, Codex, etc.) in isolated Firecracker micro-VMs with full aud
        it trail capability.
      3 ## Overview
      4 ### Goals
      5 - Run 1-50+ coding agents in parallel on a single host
      6 - Complete isolation via Firecracker micro-VMs (not containers)
     … +474 lines (ctrl+o to expand)

● Bash(cd /home/jesse/git/stockyard && git add
      docs/plans/2026-01-16-stockyard-design.md .gitignore && git status)
  ⎿  On branch main               

     No commits yet
     … +5 lines (ctrl+o to expand)

● Bash(git commit -m "Add initial stockyard design document…)
  ⎿  [main (root-commit) 1014ebc] Add initial stockyard design document
      2 files changed, 484 insertions(+)
      create mode 100644 .gitignore
      create mode 100644 docs/plans/2026-01-16-stockyard-design.md

● Design committed. Here's where we landed:

  Stockyard Summary

  What it does: Runs coding agents in isolated Firecracker micro-VMs with instant ZFS snapshots for full audit trails.

  Key architecture:
  - stockyardd daemon as control plane (API-first for future web UI)
  - stockyard CLI as thin client
  - Flintlock manages Firecracker VMs
  - ZFS pool for persistent workspaces + instant snapshots
  - vsock for in-VM snapshot triggers (Claude Code hooks)
  - Tailscale for direct SSH access
  - 1Password for secrets (abstracted for AWS SM later)

  Core workflow:
  stockyard run --repo github.com/org/repo --ref branch \
    -- claude-code --dangerously-skip-permissions -p "do the thing"

  ---
  Next steps per the brainstorming process:

  Ready to set up for implementation? This would involve:
  1. Creating a git worktree for development
  2. Writing a detailed implementation plan with phases

  Or do you want to let this design marinate first?
