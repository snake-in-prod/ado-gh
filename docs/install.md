# Install

`ado` is a single binary for Windows, macOS and Linux on amd64 and arm64.
Releases live at <https://github.com/snake-in-prod/ado-gh/releases>; the
examples use version `1.2.3`, replace it with the release you want.

| Channel | Install | Upgrade | Uninstall |
| --- | --- | --- | --- |
| Homebrew (macOS) | `brew install --cask snake-in-prod/tap/ado` | `brew upgrade --cask ado` | `brew uninstall --cask ado` |
| Scoop (Windows) | [Scoop](#scoop-windows) | `scoop update ado` | `scoop uninstall ado` |
| winget (Windows) | `winget install SnakeInProd.Ado` | `winget upgrade --id SnakeInProd.Ado` | `winget uninstall --id SnakeInProd.Ado` |
| `.deb` / `.rpm` (Linux) | [Linux packages](#linux-packages) | install the newer package | `sudo apt remove ado` / `sudo dnf remove ado` |
| `install.sh` (Linux, macOS) | [Install script](#install-script-linux-macos) | run it again | `rm ~/.local/bin/ado` |
| `install.ps1` (Windows) | [PowerShell script](#powershell-script-windows) | run it again | [PowerShell script](#powershell-script-windows) |
| `go install` | [Go](#go) | run it again | `rm "$(go env GOPATH)/bin/ado"` |

`ado` checks GitHub for a newer release once a day and prints the upgrade
command for the way you installed it. Set `ADO_NO_UPDATE_NOTIFIER=1` to turn
that off.

## Homebrew (macOS)

```sh
brew install --cask snake-in-prod/tap/ado
```

The cask installs the man page and bash, zsh, fish and PowerShell completions.
Prereleases (`-rc.N`) are not published to Homebrew, Scoop or winget.

`ado` is not signed or notarized by Apple. Homebrew quarantines every cask
download and macOS would refuse to run the binary ("cannot be opened" or "is
damaged"), so the cask runs `xattr -dr com.apple.quarantine` on `ado` after
installing it.

**Security note:** that hook turns off the Gatekeeper check for `ado` (and only
`ado`). You trust the tap and the GitHub release instead; [verify the
download](#verifying-downloads) for proof of where it came from. Apple may
disable this bypass in a future macOS. If macOS still blocks `ado`, run:

```sh
xattr -dr com.apple.quarantine "$(brew --caskroom)/ado"
```

A tarball downloaded with a browser is quarantined too: run
`xattr -d com.apple.quarantine ado` after extracting it. `curl` and
`install.sh` don't set the attribute.

## Scoop (Windows)

```powershell
scoop bucket add snake-in-prod https://github.com/snake-in-prod/scoop-bucket
scoop install snake-in-prod/ado
```

## winget (Windows)

```powershell
winget install SnakeInProd.Ado
```

New versions reach winget after Microsoft reviews the `winget-pkgs` pull
request, which can take a few days. Scoop, the PowerShell script and the zip on
the release page have them on release day.

## Linux packages

Download the `.deb` or `.rpm` for your architecture (`amd64` or `arm64`) from
the release and install it. It puts `ado` in `/usr/bin`, with the man page and
bash, zsh and fish completions.

```sh
VERSION=1.2.3
ARCH=amd64
# Debian, Ubuntu
curl -fLO "https://github.com/snake-in-prod/ado-gh/releases/download/v$VERSION/ado_${VERSION}_linux_$ARCH.deb"
sudo apt install "./ado_${VERSION}_linux_$ARCH.deb"
# Fedora, RHEL
curl -fLO "https://github.com/snake-in-prod/ado-gh/releases/download/v$VERSION/ado_${VERSION}_linux_$ARCH.rpm"
sudo dnf install "./ado_${VERSION}_linux_$ARCH.rpm"
```

## Install script (Linux, macOS)

```sh
curl -fsSL https://snake-in-prod.github.io/ado-gh/install.sh | sh
```

It installs the latest release to `~/.local/bin` without sudo, checks the
archive against `checksums.txt` and, when `cosign` is installed, checks
`checksums.txt` against its signature. Pick a version or directory with:

```sh
curl -fsSL https://snake-in-prod.github.io/ado-gh/install.sh | sh -s -- --version 1.2.3 --bin-dir /usr/local/bin
```

Run it again to upgrade. To uninstall, delete `ado` from `~/.local/bin` (or the
`--bin-dir` you chose).

## PowerShell script (Windows)

```powershell
irm https://snake-in-prod.github.io/ado-gh/install.ps1 | iex
```

It installs the latest release to `%LocalAppData%\Programs\ado`, adds that
directory to your user `PATH` and verifies the download like `install.sh`.
`irm | iex` can't pass parameters, so pick a version or directory with
environment variables:

```powershell
$env:ADO_VERSION = '1.2.3'; $env:ADO_INSTALL_DIR = 'C:\tools\ado'
irm https://snake-in-prod.github.io/ado-gh/install.ps1 | iex
```

or with the scriptblock form, which also takes `-NoModifyPath` to leave `PATH`
alone:

```powershell
& ([scriptblock]::Create((irm https://snake-in-prod.github.io/ado-gh/install.ps1))) -Version 1.2.3 -BinDir C:\tools\ado -NoModifyPath
```

Run it again to upgrade. To uninstall, delete the directory and remove it from
your user `PATH` (Settings > System > About > Advanced system settings >
Environment Variables):

```powershell
Remove-Item -Recurse "$env:LOCALAPPDATA\Programs\ado"
```

## Go

With Go 1.25.8 or later:

```sh
go install github.com/snake-in-prod/ado-gh/cmd/ado@latest
```

Run it again to upgrade. The binary lands in `$(go env GOPATH)/bin` (or
`GOBIN`) without a man page or completions; see
[Shell completions](#shell-completions).

## Manual download

Each release has `ado_<version>_<os>_<arch>.tar.gz` archives (`.zip` on
Windows) with the binary, `LICENSE`, `README.md`, the man page in `manpages/`
and completion scripts in `completions/`. Put `ado` (`ado.exe`) on your `PATH`.

## Shell completions

Homebrew, `.deb` and `.rpm` install completions for you. Otherwise generate
them with `ado completion <shell>`:

```sh
# bash (needs the bash-completion package)
mkdir -p ~/.local/share/bash-completion/completions
ado completion bash > ~/.local/share/bash-completion/completions/ado
# zsh: also add `fpath=(~/.zfunc $fpath)` before `compinit` in ~/.zshrc
mkdir -p ~/.zfunc
ado completion zsh > ~/.zfunc/_ado
# fish
ado completion fish > ~/.config/fish/completions/ado.fish
```

```powershell
# PowerShell: load them in every session
Add-Content $PROFILE 'ado completion powershell | Out-String | Invoke-Expression'
```

## Verifying downloads

Each release has `checksums.txt` (SHA-256 of every archive and package), signed
two ways: a keyless [cosign](https://github.com/sigstore/cosign) bundle,
`checksums.txt.sigstore.json`, made by the release workflow, and a GPG
signature, `checksums.txt.sig`. GitHub build provenance covers every archive,
package and `checksums.txt`; its bundle is the `attestations.jsonl` asset.

Download what you need:

```sh
VERSION=1.2.3
FILE=ado_${VERSION}_linux_amd64.tar.gz
for f in "$FILE" checksums.txt checksums.txt.sigstore.json checksums.txt.sig attestations.jsonl; do
  curl -fsSLO "https://github.com/snake-in-prod/ado-gh/releases/download/v$VERSION/$f"
done
```

**cosign** (3.1.3 or later: older versions have a verification bypass). Pin the
exact tag:

```sh
cosign verify-blob \
  --certificate-identity "https://github.com/snake-in-prod/ado-gh/.github/workflows/release.yml@refs/tags/v$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt
```

or accept any release tag with an anchored regular expression:

```sh
cosign verify-blob \
  --certificate-identity-regexp '^https://github\.com/snake-in-prod/ado-gh/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --bundle checksums.txt.sigstore.json checksums.txt
```

**GPG.** The release key, `ado release signing <f.dziewiatowski@gmail.com>`, is
on [keys.openpgp.org](https://keys.openpgp.org). Its primary key fingerprint is

```text
E8CC 041B D0BE BDC1 3ADB  967E EC1E F53E 7EF8 CA48
```

and releases are signed with its subkey
`9236 1482 2D64 5B37 06B7  56E7 F621 5CEA 43F8 A59C`.

```sh
gpg --keyserver hkps://keys.openpgp.org --recv-keys E8CC041BD0BEBDC13ADB967EEC1EF53E7EF8CA48
gpg --verify checksums.txt.sig checksums.txt
```

Expect `Good signature` and the primary key fingerprint above.

**Checksum.** Once `checksums.txt` checks out, check your download against it:

```sh
sha256sum --check --ignore-missing checksums.txt       # Linux
shasum -a 256 --check --ignore-missing checksums.txt   # macOS
```

```powershell
$want = ((Select-String -Path checksums.txt -SimpleMatch 'ado_1.2.3_windows_amd64.zip').Line -split '\s+')[0]
(Get-FileHash ado_1.2.3_windows_amd64.zip -Algorithm SHA256).Hash -eq $want
```

**Build provenance** with the [GitHub CLI](https://cli.github.com) 2.93.0 or
later (older versions send your token to third-party mirrors). Without logging
in, use the bundle from the release:

```sh
gh attestation verify "$FILE" --repo snake-in-prod/ado-gh --bundle attestations.jsonl
```

Logged in (`gh auth login`), gh fetches the attestation from GitHub instead:

```sh
gh attestation verify "$FILE" --repo snake-in-prod/ado-gh
```

Next: [sign in](auth.md) and [get started](getting-started.md).
