module github.com/openpak/website

go 1.26

require (
	github.com/jackc/pgx/v5 v5.7.6
	golang.org/x/image v0.30.0
	google.golang.org/grpc v1.75.1
	openpak/account v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/net v0.42.0 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250707201910-8d1bb00bc6a7 // indirect
	google.golang.org/protobuf v1.36.9 // indirect
)

// The account core is consumed strictly through its versioned contracts
// (generated protobuf stubs). The sibling checkout is pinned by CI; see
// README.md "Building".
replace openpak/account => ../account
