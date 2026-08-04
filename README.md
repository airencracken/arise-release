# Arise release automation

This repository owns cross-repository release orchestration for
[Arise](https://github.com/airencracken/arise), its Gentoo overlay, and the
immutable packaging-assets repository.

```sh
go run ./cmd/arise-release prepare 0.0.10
go run ./cmd/arise-release verify 0.0.10
go run ./cmd/arise-release publish 0.0.10
```

By default the tool expects sibling `arise` and `arise-overlay` repositories.
Override them with `--arise` and `--overlay`. State is stored atomically in
`.release/arise-VERSION.json`; every later stage verifies that repository
commits and the artifact digest still match.

`prepare` requires a clean, committed source version bump and creates the
vendor artifact. `verify` runs static, correctness, vet, race, benchmark, and
reproducible offline gates. `publish` tags and publishes the source and asset,
renders this repository's embedded ebuild template, runs Portage validation, and
commits and pushes the overlay. Completed publication steps are recorded so a
retry resumes rather than replacing an immutable input.

The final live-system install is deliberately outside this tool.
