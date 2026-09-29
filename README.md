# Arise release automation

This repository owns cross-repository release orchestration for
[Arise](https://github.com/airencracken/arise), its Gentoo overlay, and the
immutable packaging-assets repository.

```sh
go run ./cmd/arise-release prepare 0.0.38
go run ./cmd/arise-release verify 0.0.38
go run ./cmd/arise-release publish 0.0.38
```

By default the tool expects sibling `arise` and `arise-overlay` repositories.
Override them with `--arise` and `--overlay`. State is stored atomically in
`.release/arise-VERSION.json`; every later stage verifies clean repository
contents, pinned commits, and both artifact digests. Push the committed source
to a branch before verification so GitHub can serve its pinned source archive.

`prepare` requires a clean, committed source version bump and creates both the
vendor artifact and the reproducible static `linux-amd64` binary bundle with an
embedded provenance manifest. `verify` runs static, correctness, vet, race,
benchmark, and reproducible offline gates. It renders and validates the overlay
in a private copy, retains one previous fallback version, and creates a pinned
commit on `release/arise-VERSION` without changing the caller's overlay checkout.
Local vendor and binary assets are used during validation; no GitHub release
needs to exist yet.

`publish` requires these gates before any release publication, then pushes the
pinned source/tag atomically, reconciles existing releases and immutable assets,
and pushes the already validated overlay commit. Identical existing tags and
assets are accepted on retry; conflicting identities stop publication. Network
and authentication errors are reported rather than treated as missing releases.
The ledger records the overlay commit before its push, so an interrupted push
can resume. Calling publish again after completion is harmless.

The final live-system install is deliberately outside this tool.
