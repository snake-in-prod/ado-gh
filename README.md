# ado

`ado` is a command-line tool for Azure DevOps, modeled on the GitHub CLI (`gh`).
It brings pull requests, repositories and other Azure DevOps concepts to the terminal.

> Status: early development. Nothing is released yet.

## Build

```sh
go build -o bin/ado ./cmd/ado
```

## Development

```sh
gofmt -l . && go vet ./... && golangci-lint run && go test ./... -race -cover
```

## License

[MIT](LICENSE)
