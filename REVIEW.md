# Review rules

WALL-D and GitHub Copilot code review apply these rules on top of the stack's review criteria.

## Release workflow

- Flag an action version update in `.github/workflows/` whose SHA does not resolve to the stated version: the release can execute different code from the version the change claims to install. Resolve the SHA rather than guessing it: `gh api repos/OWNER/REPO/commits/vX.Y.Z --jq .sha`. This applies to the four `danielmundi/upload-packagecloud` uses in `.github/workflows/release.yaml`—the only action receiving `PACKAGECLOUD_TOKEN`—as well.
- Flag permissions added to the top level of `.github/workflows/release.yaml`, or permissions a release job does not use: every inheriting job gains access intended for one publisher. Keep that workflow's top-level `permissions: {}` and grant each permission only to the job that needs it.

## Exit codes

- Flag a caller-supplied ID that fails `api.RequireID` or a blank name that fails `api.RequireName` but still sends a request or exits with a code other than 2, or an unusable server-response ID (`api.RequireResponseID`) that exits with a code other than 1: scripts, CI, and agents mistake invalid input for an operation failure or malformed server data for a usage error.
