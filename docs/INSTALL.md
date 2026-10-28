# Installation

PeerPages has two parts, and **you only have to install the agent**:

- **The agent** (`peerpages`) — a single binary that runs on your machine.
- **The web app** — a static page sitting on GitHub Pages. The visitor
  installs nothing, they just open the URL.

## 1. Download a binary (recommended)

Download the archive for your platform from the releases page:
<https://github.com/aliosmanmho/PeerPages/releases>

| Operating system | File |
|---|---|
| Windows | `peerpages_<version>_windows_amd64.zip` (`windows_arm64` for ARM) |
| macOS (Apple Silicon) | `peerpages_<version>_darwin_arm64.tar.gz` |
| macOS (Intel) | `peerpages_<version>_darwin_amd64.tar.gz` |
| Linux | `peerpages_<version>_linux_amd64.tar.gz` (`linux_arm64` for ARM) |

Unpack the archive and put `peerpages` (`peerpages.exe` on Windows) into a
directory on your PATH. You can check the integrity of what you downloaded
against `checksums.txt`:

```sh
sha256sum -c checksums.txt --ignore-missing
```

On macOS the first run may trip Gatekeeper (the binary is not signed):
open **System Settings → Privacy & Security** and choose "open anyway".

## 2. With `go install`

Go 1.27 or newer is required:

```sh
go install github.com/aliosmanmho/PeerPages/agent/cmd/peerpages@latest
```

The binary lands under `$(go env GOPATH)/bin`; make sure that directory is
on your PATH. A binary installed this way knows its version too (through
`runtime/debug`), so `peerpages version` prints the right tag.

> **Why `cmd/peerpages` and not just `…/agent`?** `go install` names the
> binary after the last element of the path, so `…/PeerPages/agent@latest`
> installs it as **`agent`**, while every document and every release
> archive calls it `peerpages`. That older path still works — you just have
> to run it as `agent`, or rename it.

> **Note:** the module path is `…/PeerPages/agent` and it was **settled**
> in phase 7: the project name stays `peerpages`, so this path will not
> change.

## 3. Scoop / Homebrew (not published yet)

The `scoop-bucket` and `homebrew-tap` repositories are not ready; they sit
in the goreleaser configuration as a draft and uploading is off
(`.goreleaser.yaml` → `skip_upload: true`). Once they are published:

```sh
# Windows
scoop bucket add aliosmanmho https://github.com/aliosmanmho/scoop-bucket
scoop install peerpages

# macOS / Linux
brew install --cask aliosmanmho/tap/peerpages
```

## 4. Build from source

```sh
git clone https://github.com/aliosmanmho/PeerPages.git
cd PeerPages/agent
go build -o peerpages .
```

For the whole development environment (the web app included):
[DEV.md](DEV.md).

## Check the installation

```sh
peerpages version
peerpages doctor
```

`version` prints the version, the commit, the date and the platform.
`doctor` tests reaching the relays, STUN and the NAT type — if something
is wrong you see it here, before trying to connect.

## The first run

```sh
peerpages serve --target http://localhost:3000
```

The agent prints a visitor address for every target. For detailed usage,
several targets and the shared secret see [DEV.md](DEV.md); for the manual
trial steps see [MANUAL-TEST.md](MANUAL-TEST.md).

## Running it as a service on Windows

The agent is **not** a service: it is an ordinary foreground process and
it stops when you close the terminal. If you want it to start on login
there are two ways. Both are documentation only: PeerPages installs no
service unit.

> The agent recovers from a relay going down and from sleep/wake on its
> own (phase 4); the paths below are for the process **crashing** or the
> machine restarting.

### Task Scheduler (no extra tool needed)

```powershell
$action  = New-ScheduledTaskAction -Execute "C:\tools\peerpages.exe" `
           -Argument "serve --target http://localhost:3000"
$trigger = New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName "PeerPages" -Action $action -Trigger $trigger -Settings $settings
```

The logs sit in the task history; to see the agent's own output use a
redirect (`cmd /c peerpages … >> log.txt 2>&1`).

### NSSM (a real Windows service)

```powershell
nssm install PeerPages "C:\tools\peerpages.exe" "serve --target http://localhost:3000"
nssm set PeerPages AppStdout "C:\tools\peerpages.log"
nssm set PeerPages AppStderr "C:\tools\peerpages.log"
nssm start PeerPages
```

**Careful:** an agent running as a service uses the service account's
`~/.peerpages/key` file — so **its peer-id is different**. If you want to
keep the same address, pass the key explicitly with `--key`.

### macOS / Linux

On macOS `launchd` (`~/Library/LaunchAgents/*.plist`) and on Linux a user
service (`systemctl --user`) do the same job. In both cases run the agent
**in the user session**: the target (`localhost:3000`) is usually a
development server that belongs to the user.
