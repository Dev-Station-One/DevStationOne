# Testing DevStationOne

Two layers: automated Go tests, and a manual smoke run (real server + real
runtime + browser). The manual steps below are exactly what Slice 6 was
smoke-tested with.

## Automated tests

Requires the *test* Postgres from `docker-compose.yml` (port 55433) — a
separate container from the persistent dev Control Server's database on
55432, so `make test` can `TRUNCATE ... CASCADE` freely without wiping a
real logged-in session (ADR-031):

```bash
make dbtest-postgres-up
export DEVSTATIONONE_TEST_DATABASE_URL='postgres://devstation:devstation@localhost:55433/devstation_test?sslmode=disable'
make test        # go test -p 1 ./... — -p 1 is required: packages share one Postgres
make lint        # gofmt + go vet
make openapi-lint
```

Migrations run automatically inside each package's `testPool`/`testDB`
helper (`database.Migrate`, idempotent) — no separate migrate step.

Note: without `DEVSTATIONONE_TEST_DATABASE_URL` the integration tests
silently skip. Check the `internal/api` timing — a 0.005s run means
everything skipped.

## Browser end-to-end test (Slice 12)

`internal/api/e2e_test.go` drives a real headless Chrome/Chromium through
the actual static HTML/JS (`web/static/`) against a real router+Postgres —
proof the login form, session cookie, sidebar client-side render, and a
live API fetch (the Activity page, Slice 11) all work together in an
actual browser JS engine, which the rest of `internal/api`'s httptest-based
tests structurally cannot check (they never execute JavaScript).

Needs a `google-chrome`/`chromium-browser`/`chromium` binary on `PATH` (in
that preference order — a bare `chromium` that resolves to a snap wrapper
is unreliable to drive over CDP; see the comment in `findChromium`) in
addition to `DEVSTATIONONE_TEST_DATABASE_URL`; it `t.Skip`s, not fails, if
none is found. `make test` already runs it as part of `internal/api`'s normal
suite — no separate command.

## Mobile QA

No separate suite — the responsive/mobile-first requirement
(docs/WEB_UI_DESIGN.md) is checked by hand at a phone-width viewport
(resize to ~390×844) whenever a page changes: no page-level horizontal
scroll, the sidebar folds to a hamburger toggle, and any data table stays
inside its own `.table-responsive` scroll container rather than the whole
page (the same convention every existing data table in the app already
follows).

## Manual smoke run

**Before starting**: this machine runs many projects. Port 8080 is usually
taken — check `ss -ltnp | grep 8080` and pick a free port (examples use
18080). Kill leftovers from earlier runs first:
`pkill -f 'devstationone-server|devstationone-runtime'`.

### 1. Build and start the Control Server

```bash
go build -o /tmp/devstationone-server ./cmd/devstationone-server
go build -o /tmp/devstationone-admin ./cmd/devstationone-admin
export DEVSTATIONONE_DATABASE_URL='postgres://devstation:devstation@localhost:55432/devstation?sslmode=disable'
DEVSTATIONONE_SERVER_ADDR=':18080' /tmp/devstationone-server &
echo 'smoke-admin-pass' | /tmp/devstationone-admin --email admin@devstationone.local
```

`http://localhost:18080/healthz` must answer `{"status":"ok"}`, `/readyz` must answer `{"status":"ready"}`.

### 2. Enroll a runtime and create a Project

Browser path: log in at `/login.html` with the admin credentials, go to
**Runtimes**, create one, **copy the token it shows once**, then create a
Project pointing at it:

- name: anything
- runtime: the one just created
- OS user: your username
- working directory: a scratch dir with a few files in it, e.g. `/tmp/devstationone-files`
- shell: `/bin/bash`

Or with curl:

```bash
curl -s -c /tmp/ds.jar -H 'Content-Type: application/json' \
  -d '{"email":"admin@devstationone.local","password":"smoke-admin-pass"}' \
  http://localhost:18080/api/v1/auth/login
curl -s -b /tmp/ds.jar -H 'Content-Type: application/json' -d '{"name":"rt1"}' \
  http://localhost:18080/api/v1/runtimes   # save id + token from the response
```

### 3. Start the runtime

```bash
go build -o /tmp/devstationone-runtime ./cmd/devstationone-runtime
DEVSTATIONONE_SERVER_URL='http://localhost:18080' \
DEVSTATIONONE_RUNTIME_TOKEN='<paste token>' \
/tmp/devstationone-runtime
```

The runtime must show **online** under Runtimes within a couple of seconds.

### 4. Exercise the File Manager (Slice 6)

Open the Project → **Files** tab:

1. The Project's working directory lists with breadcrumbs; entries show
   type/size/modified/mode.
2. **New file** → a name → it appears (0 B).
3. Click the file → CodeMirror opens → type → status flips to *Modified*
   → **Save** → *Saved*, and the content is on disk in the scratch dir.
4. **Preview** on a `.md` file renders it; image files open as image
   previews; a binary file shows the *Binary* state; a >1 MB file shows
   *Too large*.
5. **Upload** → pick a local file → it lands in the current directory.
6. **Download** → the browser downloads the real bytes.
7. **Search** "…" by name substring → matching paths, click to jump.
8. **Rename / Copy / Move / Delete** (with confirm) on a scratch copy.
9. Select checkboxes → **Delete selected**.

### 5. Prove containment (acceptance)

```bash
# traversal — expect 400 {"error":"path \"../\" escapes the project root"}
curl -s -b /tmp/ds.jar 'http://localhost:18080/api/v1/projects/<projectID>/files?path=../'
# absolute — expect 400
curl -s -b /tmp/ds.jar 'http://localhost:18080/api/v1/projects/<projectID>/files?path=/etc/passwd'
```

For the symlink attack (needs direct disk access to the scratch dir):

```bash
ln -s /etc/passwd /tmp/devstationone-files/escape
# expect 502, and /etc/passwd untouched:
curl -s -b /tmp/ds.jar 'http://localhost:18080/api/v1/projects/<projectID>/files/content?path=escape'
```

Runtime-side containment is what rejects the symlink — the same request
path a stolen session would take.

### 6. Other tabs while you are in there

- **Terminal**: "+ New" → real bash in the Project cwd; open two, refresh
  the browser, the process keeps running and reattaches.
- **Services**: lists real `systemctl --user` units; Start/Stop a harmless
  one of your own.
