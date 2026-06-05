# FORK.md — cosmos-gc-teshy

**Forked from:** https://github.com/coinhall/cosmos-gc  
**Upstream commit forked at:** `1683cc3` (feat: improve code quality and add concurrency)  
**Fork date:** 2026-06-05  
**Maintainer:** teshy / realio-validator-node fleet

## Why

The upstream project is unmaintained and pinned to Cosmos SDK v0.47 / iavl v0.20 / CometBFT v0.37,
which are incompatible with the Realio mainnet binary (SDK v0.53.5 / iavl v1.2.2 / CometBFT v0.38).

The iavl version gap is **load-bearing**: iavl v1 changed node-key encoding, so the upstream binary
would silently corrupt `application.db` if run against a v1.2.2-encoded store.

## Pin set (must match the running `realio-networkd` v1.6.0 exactly)

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

## API changes applied (SDK 0.47 → 0.53 / cometbft 0.37 → 0.38)

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
