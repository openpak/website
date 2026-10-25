module github.com/openpak/website

go 1.27.0

require (
	github.com/coder/websocket v1.8.15
	github.com/jackc/pgx/v5 v5.11.0
	github.com/openpak/account-api v0.1.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	github.com/sigstore/sigstore v1.10.10
	github.com/theupdateframework/go-tuf/v2 v2.4.2
	golang.org/x/image v0.46.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/go-containerregistry v0.22.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/secure-systems-lab/go-securesystemslib v0.11.1 // indirect
	github.com/sigstore/protobuf-specs v0.5.2 // indirect
	github.com/youmark/pkcs8 v0.0.0-20240726163527-a2c0da244d78 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260727163830-6c54dddc4772 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260720155508-bb71a54f79dc // indirect
)

// The account core is consumed strictly through its versioned contracts: the
// generated protobuf stubs come from github.com/openpak/account-api.
