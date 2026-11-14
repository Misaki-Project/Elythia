# Misaki production image releases

The production release workflow is manual and only accepts `Misaki-develop`.
It requires a new stable `vMAJOR.MINOR.PATCH` tag. It runs the pinned production
plugin compatibility workflow first, then builds the same checked-out commit
with the exact production plugin pins. FxTwitter is explicitly included even
though its plugin manifest is disabled by default.

Before pushing an image, CI verifies the generated registrations for role-level,
Genshin, Fedwatch, HSR, FxTwitter preview, and the tracking cleaner metadata
plugin. The cleaner has an intentional Go registration and no migrations. CI
also inventories the concrete migration definitions and SQL hashes from both
core migration tracks (`migration/*.up.sql` and `migration/local/*.up.sql`) and
the five migration-bearing plugins. Existing core/local/plugin migration IDs,
versions, and hashes must match
`.github/release/production-migration-baseline.json`; external plugin source
refs are fetched at their fixed commits and verified before the source is used.
The verified clone metadata is removed before plugin compilation. Additional migrations are accepted only
when their ordered version and SQL hash are added to
`.github/release/approved-production-migration-additions.json` and the checker
pin is reviewed with that change. This CI approval does not itself establish
that a separate production updater can safely apply the new schema.

After both validation and build report the same source revision and inventory
hash, CI pulls the immutable OCI digest and checks its platform, source revision,
and production profile label before creating a Release draft. The
`elythia-image-manifest.json` asset preserves the updater's five-field contract:
`schemaVersion`, `tag`, immutable `image`, source `revision`, and `platform`.
CI checks the uploaded asset digest and that the tag points to the built commit,
publishes it as latest, then checks `/releases/latest` and its asset digest.
Images are built for `linux/amd64`; other
platforms are not declared compatible by this workflow.

This workflow has not been run for the candidate and no compatible Release
image has been produced or published. The generic updater must continue to
refuse updates until its separately reviewed target compatibility profile is
ready.
