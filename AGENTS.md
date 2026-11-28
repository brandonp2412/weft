# Wrapping up work

- Whenever you finish a bug/feature, `make` and restart any instances of weft
with the newly built binary.
- Before handing back changes, format the Go files you changed with `gofmt -w`
(or use `make fmt` for the whole tree), then run `make check`. It checks formatting,
runs `go vet`, and runs the Go tests with the race detector. Fix failures caused
by your changes and report any remaining failures.
- Once everything looks good, git commit
