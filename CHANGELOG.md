# Changelog

## v0.1.0

- Validates the overlay before publication and retains its committed identity.
- Verifies clean source and overlay contents at each stage and rechecks source
  and artifact identities after validation.
- Resumes matching tags, rendered ebuilds, release objects, assets, and overlay
  pushes without replacing immutable content.
- Uses isolated staging and distfile directories, validates local unpublished
  vendor/binary bundles, and keeps one previous overlay fallback release.
- Propagates Go test failures from the embedded source ebuild template.
