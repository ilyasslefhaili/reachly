# Reachly

Give it a **full name** and a **company name or website**, and it returns the person's most likely work email. Each guess is checked live against the company's mail server over SMTP, whichever provider runs it. No email is ever sent: the conversation stops at `RCPT TO`.

## Install

### 1. Requirements

- **Go 1.25 or newer.** Check with `go version`.
  - macOS: `brew install go`
  - Linux: `sudo apt install golang-go` (if your distro's version is older than 1.25, use the installer from [go.dev/dl](https://go.dev/dl/))
  - Windows: the installer from [go.dev/dl](https://go.dev/dl/)
- **Outbound port 25 open.** This is the port mail servers talk on, and many home ISPs and cloud providers block it. Check it with:
  ```bash
  nc -z -w5 gmail-smtp-in.l.google.com 25 && echo "port 25 OK" || echo "port 25 blocked"
  ```
  If it is blocked, Reachly still runs, but every result will be `unknown`.

### 2. Get the code and install

```bash
git clone https://github.com/ilyasslefhaili/reachly.git
cd reachly
make install
```

`make install` builds Reachly and puts the `reachly` command in your Go bin folder (`~/go/bin`). If that folder isn't on your `PATH` yet, it adds it to your shell config (`~/.zshrc` for zsh, `~/.bash_profile` for bash) exactly once.

**Open a new terminal** (or run `source ~/.zshrc`) and check that it works from any folder:

```bash
reachly --help
```

<details>
<summary>One-line install without cloning (needs Go)</summary>

```bash
go install github.com/ilyasslefhaili/reachly/cmd/reachly@latest
```

This puts `reachly` in `$(go env GOPATH)/bin`. Make sure that folder is on your PATH (see below).

</details>

<details>
<summary>Without <code>make</code> (Windows, or if you prefer)</summary>

```bash
go install ./cmd/reachly
```

Then add the Go bin folder to your PATH yourself:

- **macOS/Linux:** add `export PATH="$PATH:$(go env GOPATH)/bin"` to `~/.zshrc` or `~/.bashrc`
- **Windows:** add `%USERPROFILE%\go\bin` to *Environment Variables → Path*, then open a new terminal

</details>

### 3. Update or uninstall

```bash
git pull && make install   # update to the latest code
make uninstall             # remove the reachly command
```

### Troubleshooting

| Problem | Fix |
|---|---|
| `reachly: command not found` | Open a new terminal. If it still fails, run `echo $PATH` and check that it contains `$(go env GOPATH)/bin`. |
| `go: command not found` | Go isn't installed, or isn't on your PATH. See step 1. |
| Every result is `unknown` with "could not connect…" | Port 25 is blocked on your network. Try another network, or a server that allows port 25 (see *Moving to the cloud*). |
| "server refused sender" / "rejected the sender address" | Use a sender on a real domain you own: `--mail-from you@yourdomain.com` |

## Quick start

```bash
reachly find "Jean Dupont" acme.com   # look up one person in the terminal
reachly serve                         # or use the web page at http://localhost:8080
```

## Use it from the terminal (CLI)

```bash
reachly find "Jean Dupont" acme.com            # one lookup
reachly find Jean Dupont https://acme.com --all  # also show every pattern checked
reachly find "Jean Dupont, Acme" --json        # full result as JSON (pipe into jq)

reachly bulk people.csv -o results.csv         # many lookups, in parallel
cat people.txt | reachly bulk --format jsonl   # read stdin, write JSON lines
reachly bulk people.csv --workers 20 --mail-from verify@yourdomain.com

reachly serve                                  # web UI (also the default with no command)
```

- **Input for `bulk`**: one `full name, company or website` per line. The separator can be a comma, tab or semicolon. A header line and `#` comments are skipped.
- **Output**: CSV columns are `name,company,email,status,confidence,domain,provider,catch_all,note,error`, in the same order as the input. Each row is written as soon as it is ready, so Ctrl-C keeps the partial results.
- **Progress and colors** go to stderr, so `> out.csv` stays clean. Set `NO_COLOR=1` to turn colors off.
- **Exit codes**: `0` an email was found, `1` lookup error or nothing found, `2` bad usage, `130` interrupted.
- All `REACHLY_*` env vars work in the CLI too. Flags (`--mail-from`, `--helo`, `--timeout`, `--per-domain`, `--workers`) override them.

## How a lookup works

1. **Name**: parsed into first/middle/last; accents, hyphens, apostrophes and titles are handled.
2. **Domain**: taken from the website if one is given. For a company name, ~11 domain guesses are checked **in parallel** (MX + HTTP).
3. **MX**: finds the domain's mail servers (any provider; falls back to the A record) and labels the provider.
4. **Patterns**: ~15 candidates (`first.last`, `first`, `flast`, …) ranked by how common they are.
5. **SMTP check (parallel)**: a catch-all probe (a random address) runs in its own goroutine while the candidates are split across several concurrent sessions. The number of sessions per mail server is capped. Greylisting (4xx) is retried once. If the server rejects the sender, the check retries with the null sender `<>`.
6. **Score**: each candidate gets a status and a confidence, and the best one is returned.

Bulk lookups go through a worker pool (`REACHLY_WORKERS` goroutines).

| Status | Meaning |
|---|---|
| `valid` | The server accepted this mailbox and rejected a random one, so it exists. |
| `catch_all` | The server accepts every address, so it can't confirm the mailbox. The top pattern is a best guess. |
| `invalid` | The server said the mailbox does not exist. |
| `unknown` | The server timed out, blocked us, or kept deferring. |

## API

```
POST /api/lookup        {"name":"Jean Dupont","company":"acme.com"}   → {"job_id":"…"}
POST /api/lookup/bulk   {"lines":"Jean Dupont, acme.com\nJane Doe, Globex"} → {"jobs":[…]}
GET  /api/jobs/{id}     → job state + result
GET  /healthz
```

## Config (env vars)

| Var | Default | |
|---|---|---|
| `REACHLY_ADDR` | `:8080` | HTTP listen address |
| `REACHLY_HELO` | `reachly.local` | EHLO hostname. In the cloud, use a real hostname matching your server's reverse DNS |
| `REACHLY_MAIL_FROM` | `verify@reachly.local` | Envelope sender. An address on a real domain you own works best |
| `REACHLY_WORKERS` | `10` | Parallel lookups |
| `REACHLY_PER_DOMAIN` | `3` | Max concurrent SMTP sessions per mail server (1 catch-all + N-1 candidate sessions) |
| `REACHLY_SMTP_TIMEOUT` | `10s` | Per-command timeout |
| `REACHLY_PROBE_DELAY` | `300ms` | Pause between RCPTs in one session |
| `REACHLY_GREYLIST_WAIT` | `8s` | Wait before retrying a 4xx |

## Limits

- Many large companies (on Google Workspace, Microsoft 365, security gateways, or self-hosted servers) are set up as **catch-all**. SMTP cannot confirm a mailbox there, so Reachly returns a best guess. This is a limit of the protocol.
- Home IPs are often on blocklists. Some servers will refuse the connection and you'll see `unknown` with a note.
- Keep the rate limits conservative. Probing too fast gets your IP blocklisted.

## Moving to the cloud later

- AWS, GCP and Azure block outbound port 25 by default. Use a VPS that allows it (e.g. Hetzner or OVH, or request an unblock).
- Set a PTR (reverse DNS) record for the server IP, and set `REACHLY_HELO` to that hostname and `REACHLY_MAIL_FROM` to an address on your domain.
- `docker build -t reachly . && docker run -p 8080:8080 reachly`
- Swap `jobs.MemStore` for a SQLite/Postgres implementation of `jobs.Store` to keep jobs across restarts. Swap `domain.Heuristic` for an enrichment API that implements `domain.Resolver` to get better company→domain matching.

## Development

```bash
make run     # start the web UI from source (go run ./cmd/reachly serve)
make test    # unit tests + fake SMTP server tests, with the race detector
make build   # local binary at bin/reachly (not installed on PATH)
make fmt     # gofmt
```
