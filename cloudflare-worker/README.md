# OneKVM download Worker

This Cloudflare Worker serves the `onekvm.sh` bootstrap script from the latest
published installer Release:

- requests whose `User-Agent` contains `curl` or `wget` receive the script;
- every other User-Agent receives `301 https://github.com/onekvm`;
- a missing embedded script returns `503` instead of serving an incomplete
  installer.

Script responses use the Cloudflare Cache API. The cache key contains the
script's SHA-256, so a release can use a one-year edge TTL without serving the
previous release after deployment. Browser redirects are kept out of this
cache because the response depends on the request's User-Agent.

The Worker source is deployed by
[`../.github/workflows/deploy-worker.yml`](../.github/workflows/deploy-worker.yml)
after the repository's successful `Build and release` workflow completes for a
version tag. It downloads `onekvm.sh` and `SHA256SUMS` from the selected
Release, verifies the asset, embeds it in `src/installer-script.js`, and
deploys the Worker with Wrangler. A manual workflow run can deploy an existing
Release created outside that workflow.

## Cloudflare setup

Create a Cloudflare API token with the account-level **Workers Scripts: Edit**
permission and enough zone or custom-domain permission to attach the Worker to
the selected hostname. Add these GitHub repository secrets:

- `CLOUDFLARE_API_TOKEN`
- `CLOUDFLARE_ACCOUNT_ID`

The default Worker name is `onekvm-sh`. To use another name, create the GitHub
repository variable `CLOUDFLARE_WORKER_NAME`, or provide `worker_name` in a
manual workflow run. The name must contain only lowercase letters, numbers,
hyphens, and underscores.

The account must manage the configured domain's zone. The default custom
domain is `onekvm.sh`. To use another hostname, create the GitHub repository
variable `CLOUDFLARE_WORKER_DOMAIN`; the deployment workflow reads it for
automatic releases. A manual workflow run can override it with the `domain`
input. The value must be a hostname without `https://` or a path.

[`wrangler.toml.template`](wrangler.toml.template) is rendered with that
name and domain for each deployment. [`wrangler.toml`](wrangler.toml) remains
a local configuration for the default `onekvm-sh` Worker and `onekvm.sh`
domain.

The workflow can be started manually with a release tag. Without a tag it uses
the latest non-draft, non-prerelease release; if the repository has no stable
Release yet, it uses `onekvm.sh` from the checked-out commit.

## Local checks

The tracked `src/installer-script.js` is an empty safety placeholder. The
release workflow replaces it before deployment. To test a real script locally,
write a release `onekvm.sh` as a JavaScript string export, then run Wrangler
from this directory:

```sh
npx wrangler dev
```
