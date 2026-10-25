# Evidence: website

## Testing

`go vet ./...` and `go test -race ./...` are the definition of done and run
in CI. The W1/W2 acceptance journeys (register → verify → login → friend
code → link revoke; request → accept → visible on both sides; block →
unblock → remove) are covered end to end in `internal/pages/journey_test.go`
and `internal/api/api_test.go` against contract-faithful fakes.
