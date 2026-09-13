# Testing DevStationOne

Two layers: automated Go tests, and a manual smoke run (real server + real
runtime + browser). The manual steps below are exactly what Slice 6 was
smoke-tested with.

## Automated tests

Requires the dev Postgres from `docker-compose.yml` (port 55432):

```bash
make db-up
export DEVSTATIONONE_DATABASE_URL='postgres://devstation:devstation@localhost:55432/devstation?sslmode=disable'
make test        # go test -p 1 ./... — -p 1 is required: packages share one Postgres
make lint        # gofmt + go vet
make openapi-lint
```

Note: without `DEVSTATIONONE_DATABASE_URL` the integration tests silently skip.
Check the `internal/api` timing — a 0.005s run means everything skipped.

## Manual smoke run

**Before starting**: this machine runs many projects. Port 8080 is usually
taken — check `ss -ltnp | grep 8080` and pick a free port (examples use
18080). Kill leftovers from earlier runs first:
`pkill -f 'devstationone-server|devstationone-runtime'`.

### 1. Build and start the Control Server

```bash
go build -o /tmp/devstationone-server ./cmd/devstationone-server
DEVSTATIONONE_DATABASE_URL='postgres://devstation:devstation@localhost:55432/devstation?sslmode=disable' \
DEVSTATIONONE_SERVER_ADDR=':18080' \
DEVSTATIONONE_ADMIN_EMAIL='admin@devstationone.local' \
DEVSTATIONONE_ADMIN_PASSWORD='smoke-admin-pass' \
/tmp/devstationone-server
```

`http://localhost:18080/healthz` and `/readyz` must answer `{"status":"ok"}`.

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
