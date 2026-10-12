# Install

`ado` is a single binary for Windows, macOS and Linux on amd64 and arm64.

Nothing is released yet, so build it from source with Go. Package managers
(Homebrew, Scoop, winget, `.deb` and `.rpm`), install scripts and `go install`
will be listed here with the first release.

## Build from source

```sh
git clone https://github.com/snake-in-prod/ado-gh.git
cd ado-gh
go build -o bin/ado ./cmd/ado
```

Put `bin/ado` on your `PATH` and check it runs:

```sh
ado version
```

Next: [sign in](auth.md) and [get started](getting-started.md).
