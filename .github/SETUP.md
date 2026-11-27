# GitHub Actions setup

The release workflow requires **no manually configured repository secrets**.
GitHub provides the short-lived `GITHUB_TOKEN` automatically and the workflow
uses it to upload binaries to the release and publish the container to GitHub
Container Registry (GHCR).

The workflow declares the required `contents: write` and `packages: write`
permissions in `.github/workflows/release.yml`. If organization policy
overrides workflow permissions, allow these permissions for the workflow.

GHCR package visibility is managed separately from Actions secrets. Set the
package visibility in GitHub's package settings if the image should be public.
