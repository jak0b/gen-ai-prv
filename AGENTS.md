# Contribution guidelines

## Commit messages

Use the [Conventional Commits](https://www.conventionalcommits.org/) format:

```text
<type>(<optional scope>): <short description>
```

Use a lowercase type such as `feat`, `fix`, `docs`, `test`, `refactor`, `build`, or `chore`. Keep the description concise and imperative. Mark breaking changes with `!` after the type or scope (for example, `feat(cli)!: change output format`) and explain them in the commit body.

## Verification

Before completing changes, run the relevant checks:

```sh
node --check web/app.js
go test ./...
```

After the tests pass, build the Go application:

```sh
go build ./...
```
