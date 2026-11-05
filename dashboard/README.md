# prox dashboard

The web dashboard for [prox](../README.md). It is a React and TypeScript app
built with Vite into a single HTML file, which the `prox` binary embeds and
serves at `/dashboard`.

It is a fork of the CLIProxyAPI management center, MIT licensed. See
[LICENSE](LICENSE).

## Develop

Start `prox start` in another terminal, then:

```bash
bun install
bun run dev          # Vite dev server; API calls go to 127.0.0.1:8317
bun run type-check   # real typecheck
bun test
bun run build        # also writes internal/proxcli/webdist/management.html
```

Tests never compile the stylesheets, so run `bun run build` before calling a
change done.
