# GitHub CI/CD and Docker deployment

GitHub repository: [Soilorian/expensemother](https://github.com/Soilorian/expensemother). Deployment is connected to `mahdi@20.11.16.11` at `/home/mahdi/expensemother`. Pushes to `main` deploy automatically after tests and production-stack smoke checks pass.

## What runs

`.github/workflows/ci-cd.yml` runs on pull requests, pushes to `main`, and manual dispatches:

1. Check Go formatting and modules, run static analysis and race-enabled tests against PostgreSQL 17, and check the deployment scripts.
2. Build one Linux AMD64 image containing the backend, Telegram bot and Tesseract Persian/English OCR. Start the production Compose stack with temporary CI credentials, check readiness/language packs and exercise signup, group creation and expense persistence.
3. For `main` only, publish that exact tested image to `ghcr.io/<owner>/<repository>:sha-<commit>`. Deployment uses its immutable digest. PRs never publish or deploy.
4. When the repository variable `DEPLOY_ENABLED` is exactly `true`, deploy over SSH using the GitHub `production` environment. Without that variable, tests and publishing work and deployment skips.

The application already starts the API and Telegram polling together. The `app` service deploys both, with PostgreSQL as `db`; receipts and database data live in named volumes. Keep one app instance for this bot token. Deployment pulls the new image before stopping the old poller and replacing it, with a short interruption. The readiness check verifies the API/database, not Telegram network reachability or OCR accuracy.

## Connect GitHub later

Create or select the repository, use `main` as its default branch, and push this project including `.github`. The workflow derives the account/repository name automatically. GitHub supplies the publishing `GITHUB_TOKEN`; no personal publishing token belongs in a file or secret.

Allow Actions to run and publish packages in the repository/organization. If a GHCR package with this name already exists, grant the repository Actions access to that package. Keep deployment disabled until the host is provisioned.

## Prepare the deployment host

The supplied deployment targets a Linux AMD64 server with Bash, SSH, Docker Engine and a recent Compose v2 supporting `up --wait` and multiple `--env-file` options. The deploy user needs Docker access and write access to `/opt/expensemother` (or your chosen path).

Create that directory and a mode-600 `.env` file there. Use the keys in the root `.env.example`: at minimum a strong URL-safe `POSTGRES_PASSWORD`, random `AUTH_SECRET` of at least 32 bytes, and the real `TELEGRAM_BOT_TOKEN` to enable polling. Add browser settings when needed. Compose sets the internal database address; `DATABASE_URL` from the example is only for native local runs. Never commit the populated `.env`.

The workflow passes its short-lived, package-read `GITHUB_TOKEN` to the host through SSH standard input. The deployment script uses an isolated Docker credential directory and removes it on exit; no permanent registry token is required. Docker runs through passwordless sudo when the deployment user cannot access the daemon directly. A host lock prevents overlapping deployments.

The API binds to `127.0.0.1:7008`; put your HTTPS reverse proxy in front of it for remote API/web clients. PostgreSQL has no host port in production. Telegram long polling needs outbound HTTPS and no incoming webhook port. `HTTP_PORT` can change the host API port.

## GitHub settings

Create an environment called `production`. Optional environment reviewers can be configured in GitHub if wanted. Add these environment variables:

- `DEPLOY_HOST`: DNS hostname or IPv4 address.
- `DEPLOY_USER`: the SSH user with Docker access.
- `DEPLOY_PORT`: optional, defaults to `22`.
- `DEPLOY_PATH`: optional, defaults to `/opt/expensemother`; use a simple absolute path without spaces or dots.

Add these environment secrets:

- `DEPLOY_SSH_KEY`: the private deployment key whose public key is authorized on the server.
- `DEPLOY_KNOWN_HOSTS`: the server's SSH known-hosts entry, verified against a trusted server-console fingerprint. For a nonstandard port, include the `[hostname]:port` entry. The workflow enforces host-key checking.

Finally set **repository** variable `DEPLOY_ENABLED=true`. This switch must be repository-level because the job condition is evaluated before its environment variables are available. Run the workflow on `main` or push a commit to trigger deployment. Secrets never go in Docker build arguments or the application image.

## Operations and rollback

After successful deployment the server directory contains `compose.production.yaml` and `.release.env` recording the exact image digest. Inspect it with:

```sh
cd /opt/expensemother
docker compose -p expensemother --env-file .env --env-file .release.env -f compose.production.yaml ps
docker compose -p expensemother --env-file .env --env-file .release.env -f compose.production.yaml logs -f app
```

The script advances the saved release only after readiness succeeds and preserves the prior successful release and Compose file. A failed deployment leaves the last successful `.release.env` and `compose.production.yaml` in place; use those to restore that release. It does not silently roll back database changes. To undo a successful deployment, inspect the previous digest and configuration, back up data, then:

```sh
cd /opt/expensemother
docker compose -p expensemother --env-file .env --env-file .previous-release.env -f compose.previous.yaml pull
docker compose -p expensemother --env-file .env --env-file .previous-release.env -f compose.previous.yaml stop app
docker compose -p expensemother --env-file .env --env-file .previous-release.env -f compose.previous.yaml up -d --wait --wait-timeout 180
cp .previous-release.env .release.env
cp compose.previous.yaml compose.production.yaml
```

Keep the stable Compose project name `expensemother` so deployments reuse the same volumes. Back up PostgreSQL and receipt storage together. Deployments do not remove volumes. The CI smoke script deletes only its disposable `expensemother-ci` project volumes. Avoid running that test project with a real bot token.

The supplied server deployment starts a fresh stack. If migrating an existing local Compose installation, arrange its data/volume migration before enabling deployment; changing project names does not move data. Changing the PostgreSQL password in `.env` does not rotate the password of an already initialized database.

## Validation and references

Local checks passed: actionlint workflow validation, both Compose configurations, shell/Python syntax, and mocked deployment scenarios for missing configuration, failed pulls, stop/start ordering, saved releases and failed startup. Full image startup, Linux race tests and SSH deployment run in GitHub/server environments; they have not yet run on your account. The local Docker engine was unavailable during preparation.

The workflow follows [GitHub's container publishing instructions](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images?learn=continuous_deployment) and uses [Compose readiness waiting](https://docs.docker.com/reference/cli/docker/compose/up/). Go CI and the Docker build both select version 1.27.1 from the [official Go release feed](https://go.dev/dl/?mode=json).

## This server

The API listens on `127.0.0.1:7008`. PostgreSQL is private to the Compose network. The service's secrets live in `/home/mahdi/expensemother/.env`, mode 600. Database and receipt volumes are separate from the personal website's data.

Planned public API hostname: `expensemother.mahdialinejad.me`, pointing to `20.11.16.11`. Add its DNS A record before enabling the Caddy site. Telegram long polling works without a public hostname and needs no webhook or extra inbound port.

To enable the bot, populate `TELEGRAM_BOT_TOKEN` in the server `.env`, then recreate the app using both `.env` and `.release.env`. The initial configuration leaves Telegram disabled until the real token is supplied. Do not share the token in chat or commit it. Before moving a bot that already runs elsewhere, stop the previous polling instance and remove any configured webhook. Add it as a group administrator and run `/setup`; members then use `/join`.

`TELEGRAM_CLIENT_ID`, `ALLOWED_ORIGINS`, and `WEB_APP_URL` are optional settings for a future browser/Mini App client; they are not necessary for the Telegram group bot. No web frontend is included in this repository.
