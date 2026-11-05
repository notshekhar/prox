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
prox dashboard        # open the dashboard, with its key on your clipboard
```

| Command | What it does |
|---|---|
| `prox start` | Run the proxy in this terminal |
| `prox claude [args]` | Open Claude Code through the proxy |
| `prox login <provider>` | Add an account (`claude`, `codex`) |
| `prox accounts` | List your accounts |
| `prox status` | Show whether the proxy is running |
| `prox dashboard` | Open the dashboard and copy its key |
| `prox service on\|off` | Run in the background and start at login (macOS) |
| `prox version` | Show the version |

`prox start` keeps the terminal quiet: one line per AI request, plus errors. The
full log is in `~/.prox/logs/main.log` and on the dashboard's Logs page.

Everything lives in `~/.prox`: `config.yaml`, `management.key`, your logins in
`auth/`, logs in `logs/` and the binary in `bin/`. Set `PROX_HOME` to use another folder. Logins
from `~/.cli-proxy-api` are copied in the first time you run `prox start`.

## Develop

```bash
scripts/build.sh                                    # dashboard + bin/prox
go vet ./... && go test ./...
(cd dashboard && bun run type-check && bun test)
```

The dashboard is a Vite and React app in `dashboard/`. `bun run build` there
copies the bundle to `internal/proxcli/webdist/`, and `go:embed` compiles it into
the binary, so commit both sides. For hot reload, run `prox start`, then
`bun run dev` in `dashboard/`, which proxies API calls to it.

`internal/proxcli` is the command line. The rest of the Go code stays close to
upstream so its fixes can be merged in.
