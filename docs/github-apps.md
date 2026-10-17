# GitHub Apps

Meghify deploys from GitHub through a **GitHub App** — the same model Coolify and
Dokploy use. One app per panel unlocks:

- private repositories, cloned with short-lived installation tokens;
- a repository picker fed straight from GitHub (no typing URLs);
- automatic redeploys when you push to the tracked branch.

Everything below happens inside the panel: create the app, confirm it on GitHub,
install it on an account, pick a repository when you deploy.

---

## 1. Create the app from the panel

1. Open **Sources** in the sidebar and press **Add GitHub App**.
2. Give the app a name (must be globally unique on GitHub, max 34 characters).
   Leave the organization field empty to create it under your personal account,
   or type an organization you administer.
3. Keep the host on `github.com`, or point it at a self-hosted GitHub Enterprise
   server (see [GitHub Enterprise](#github-enterprise) below).
4. Press **Continue**. The panel writes a placeholder source and opens its setup
   page — nothing has been created on GitHub yet.

## 2. Register with GitHub

On the setup page, the **Automated installation (Recommended)** card shows the
webhook endpoint GitHub will call and the permissions the app needs:

| Permission | Access | Why |
|---|---|---|
| Contents | read | clone the repository, including private ones |
| Metadata | read | read basic repository information |
| Email addresses | read | attribute commits to their authors |

Press **Register With GitHub**. The panel POSTs a *manifest* (a description of
the app it wants) to GitHub, and GitHub shows you a confirmation page:

- review the name and permissions, then press **Create GitHub App**;
- GitHub creates the app under your account, generates its private key and
  webhook secret, and redirects you back to **Sources** with the credentials —
  the panel exchanges the one-time code and stores them sealed.

A push event is also subscribed automatically, so no webhook needs to be typed
in by hand.

### Manual installation (Advanced)

If you cannot use the manifest flow (locked-down org policies, for example),
create the app yourself on GitHub and paste its credentials into the **Manual
installation** card:

1. On GitHub, create a GitHub App with:
   - **Webhook URL**: the endpoint shown under *Automated installation*;
   - **Permissions**: Contents `read`, Metadata `read`, Email addresses `read`;
   - **Subscribe to events**: `push`.
2. Generate a private key and note the webhook secret.
3. Press **Continue manually** in the panel and fill in the App ID, the PEM
   private key and the webhook secret (client id/secret are optional). The panel
   verifies the key parses before storing anything.

## 3. Install the app on an account

Press **Install on GitHub** (Sources page or the setup page). GitHub asks which
account and which repositories the app may see — *all* or *selected* ones. Only
those repositories ever become visible to the panel. When you finish, GitHub
redirects back and the installation appears in the panel.

Installed the app directly from `github.com/settings/apps` instead? Press
**Refresh** on Sources (or in the repository picker) and the panel pulls the
installation list from GitHub.

## 4. Deploy from a repository

In a project, press **New resource** and pick **Git Repository (with GitHub
App)**:

1. **Choose GitHub App** — pick the account (user or organization).
2. **Choose a repository** — search the list GitHub returned, including private
   ones.
3. The application form opens prefilled: repository URL, default branch,
   Nixpacks as the build pack, and **Deploy automatically on every push**
   checked. Adjust what you like and press **Create**.

The application clones through the installation token, so private repositories
work without deploy keys or stored SSH credentials.

## Automatic deploys

With *Deploy on every push* enabled (toggle it on the application page under
**Source → Deploy on every push**):

- GitHub sends a `push` event to `POST /api/webhooks/github` on this panel;
- the panel verifies the `X-Hub-Signature-256` HMAC with the app's webhook
  secret (unauthenticated deliveries are rejected);
- it finds every application linked to that repository **whose branch matches
  the pushed branch** and queues a deployment, recording the commit SHA and
  message on the deployment row;
- if a build is already running for an application, that push is skipped — the
  next push redeploys.

Tag pushes and pushes to other branches are ignored. Uninstalling the app on
GitHub removes the installation from the panel; automatic deploys stop, but
applications keep their repository URL and history.

The panel must be reachable from GitHub at its public URL
(`PANEL_PUBLIC_URL`, shown in the panel as the webhook endpoint). GitHub
strongly recommends HTTPS for webhook endpoints; the signature check keeps the
payload verifiable either way.

## Credentials and security

- The app's private key, client secret and webhook secret are **sealed with the
  panel's encryption key** before they touch the database (the same key used for
  database passwords and environment secrets).
- Clone credentials are **minted fresh for every git operation** as GitHub App
  installation tokens (one hour lifetime); they are never written into the
  workspace's `.git/config`.
- Webhook deliveries are authenticated by the app's cryptographic signature,
  not by a session, so the endpoint can safely stay outside the login gate.

## GitHub Enterprise

For a self-hosted GitHub Enterprise Server, pick *Self-hosted GitHub* and enter
its base URL (for example `https://git.example.com`) when creating the app. The
panel:

- registers manifests against `<base>/settings/apps/new`;
- talks to the REST API at `<base>/api/v3`;
- verifies webhooks with the same signature scheme.

GitHub Enterprise must be able to reach the panel's webhook endpoint as well.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Source shows **Setup incomplete** | The manifest flow never finished. Open the source, press **Register With GitHub** again, or use the manual card. |
| "That setup link has expired" | The state token no longer matches a pending app — start the registration again from the source's setup page. |
| Repository list is empty | Press **Refresh** in the picker. If the installation is missing entirely, install the app on the account first. |
| "invalid webhook signature" in the panel log | The stored webhook secret does not match the app on GitHub (re-pasted credentials?). Re-register or paste the current secret. |
| Push does not redeploy | Check the branch matches the application's branch, auto-deploy is enabled, and the delivery shows up under **Advanced → Recent Deliveries** on the app's GitHub page. |
| Private clone fails with an authentication error | The installation was removed or lost access to the repo. Press **Refresh** on Sources and re-install / regrant access. |
| HTTP 502 when listing repositories | GitHub rejected the request — usually a deleted installation or a rotated key. Re-register the app or refresh its credentials. |
