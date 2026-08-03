# FORK.md — cosmos-gc-teshy

**Forked from:** https://github.com/coinhall/cosmos-gc
**Upstream commit forked at:** `1683cc3` (feat: improve code quality and add concurrency)
**Fork date:** 2026-06-05
**Maintainer:** teshy / realio-validator-node fleet

## Scope — this is a modern-cosmos-stack build, NOT a Realio-specific tool

The tool's logic is **chain-agnostic** — there is no Realio-specific code in it (no hardcoded
chain-id, denom, bech32 prefix, or module import). It reads and rewrites a node's on-disk stores
(`application.db` via IAVL; `blockstore.db`/`state.db` via CometBFT), and those on-disk encodings
are tied to the **library versions**, not to any particular chain.

This fork exists to re-pin the tool from upstream's **legacy stack** (Cosmos SDK v0.47 / iavl v0.20
/ CometBFT v0.37) to the **modern stack** (SDK 0.50+ / iavl v1.2 / CometBFT v0.38). We built it
because the Realio mainnet fleet runs that modern stack — but **any chain on the same stack can use
it**. To target a different chain, re-pin `go.mod` to *that* chain's exact iavl / store / cometbft
versions (`go version -m <its-node-binary>`) and rebuild; nothing else changes.

## Why the pin is load-bearing

iavl v1 changed its **node-key encoding**, so a binary built against upstream's iavl v0.20 would
**silently corrupt `application.db`** if run against a v1.2-encoded store. The pruner *must* be
compiled against the same store/iavl/cometbft versions as the node whose data it operates on.

## Upstream contribution status

The two **chain-agnostic bug fixes** carried here (cap blockstore prune to the committed height;
copy all block parts, not just P:N:0) are being contributed back to upstream on branch
`upstream-pr/blockstore-committed-height-and-parts` (cherry-picked onto upstream's own pins — the
version bump below is deliberately NOT part of that PR, since it would break upstream's existing
legacy-stack users). If accepted, our fixes land upstream; this fork then carries only the pin set.

## Pin set (must match the target node binary's stack — currently Realio mainnet `realio-networkd`)

| Module | Pin |
|---|---|
| `github.com/cosmos/cosmos-sdk` | `v0.53.5-0.20251030204916-768cb210885c` |
| `github.com/cosmos/iavl` | `v1.2.2` |
| `github.com/cometbft/cometbft` | `v0.38.21` |
| `github.com/cometbft/cometbft-db` | `v0.14.1` |
| `github.com/cosmos/cosmos-db` | `v1.1.3` |
| `cosmossdk.io/store` | `v1.1.2` |
| `cosmossdk.io/log` | `v1.6.1` |
| `github.com/syndtr/goleveldb` | `v1.0.1-0.20220721030215-126854af5e6d` |

## API changes applied (SDK 0.47 → 0.50+ / cometbft 0.37 → 0.38)

- Removed `replace github.com/cosmos/iavl => github.com/cosmos/iavl v0.20.0`
- Import paths: `github.com/cosmos/cosmos-sdk/store/...` → `cosmossdk.io/store/...`
- DB adapter: `github.com/cometbft/cometbft-db` → `github.com/cosmos/cosmos-db` for application.go
  (cometbft-db v0.14.1 still used by state.go and blockstore.go for CometBFT stores)
- `log.NewNopLogger()` source: `github.com/cometbft/cometbft/libs/log` → `cosmossdk.io/log`

## Safety

This tool rewrites consensus-critical state. Before any production use:
1. Build: `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o cosmos-gc-teshy .`
2. Run the snapshot-correctness test (see main runbook `08-maintenance.md` §8.1)
3. Obtain explicit go-ahead from operator before running on production data
