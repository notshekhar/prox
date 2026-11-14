# prox

Use all your AI accounts through one local endpoint. prox signs in to your
accounts, keeps the logins fresh and spreads requests across them.

prox is a fork of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)
and its management dashboard, both MIT licensed. See [LICENSE](LICENSE).

> **Read this first.** Anthropic's terms forbid using Claude subscription logins
> in third-party tools, and rotating accounts to get around usage limits can
> break a provider's terms. Accounts can be suspended. Only add accounts you can
> afford to lose. API keys are the supported route.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/notshekhar/prox/main/install.sh | bash
```

From a checkout, with Go and Bun installed:

```bash
./install.sh --from-source
```

## Use

```bash
prox login claude     # add an account, once per account
prox start            # run the proxy
prox claude           # open Claude Code through it
prox dashboard        # open the dashboard
```

| Command | What it does |
|---|---|
| `prox start` | Run the proxy in this terminal |
| `prox claude [args]` | Open Claude Code through the proxy |
| `prox login <provider>` | Add an account (`claude`, `codex`) |
| `prox accounts` | List your accounts |
| `prox status` | Show whether the proxy is running |
| `prox doctor` | Find out why routing or an account is not working |
| `prox dashboard` | Open the dashboard |
| `prox service on\|off` | Run in the background and start at login (macOS, Linux) |
| `prox upgrade [--force]` | Update to the latest release |
| `prox version` | Show the version |

`prox start` keeps the terminal quiet: one line per AI request, plus errors. The
full log is in `~/.prox/logs/main.log` and on the dashboard's Logs page.

Everything lives in `~/.prox`: `config.yaml`, your logins in
`auth/`, logs in `logs/` and the binary in `bin/`. Set `PROX_HOME` to use another folder. Logins
from `~/.cli-proxy-api` are copied in the first time you run `prox start`.

## Master password

`prox start` won't run without your master password. You choose it the first
time, and the same password opens the dashboard. Only a hash is stored, so if you
forget it, set `secret-key: ""` under `management:` in `~/.prox/config.yaml` and
start prox to choose a new one.

Where no terminal can ask, set `PROX_MASTER_PASSWORD`. So the background service can
start at login, `prox service on` keeps the password in your macOS Keychain, or on
Linux in `~/.prox/service.env`, a file only you can read.

The password guards starting prox and the dashboard. It doesn't encrypt your
logins in `~/.prox/auth`.

## How requests are spread

With `strategy: "least-used"`, each new conversation goes to the account with the
most quota left, and accounts within five points of each other take turns. A
conversation then stays on its account so its prompt cache keeps working. It moves
when the account passes 95% and another has room, or when a request fails. Usage
comes from provider responses, so Claude and Codex accounts report it and others
count as unused.

Older configs use `"round-robin"`, which ignores usage: each new conversation just
goes to the next account. To rotate on every request instead, set
`session-affinity: false` under `routing:`.

The terminal log names the account that served each request.

## Develop

```bash
scripts/build.sh                                    # dashboard + bin/prox
go vet ./... && go test ./...
(cd dashboard && bun run type-check && bun test)
(cd dashboard && bun run check:bundle ../bin/prox)  # the built dashboard in real Chrome
```

The dashboard is a Vite and React app in `dashboard/`. `bun run build` there
copies the bundle to `internal/proxcli/webdist/`, and `go:embed` compiles it into
the binary, so commit both sides. For hot reload, run `prox start`, then
`bun run dev` in `dashboard/`, which proxies API calls to it.

`internal/proxcli` is the command line. The rest of the Go code stays close to
upstream so its fixes can be merged in.
