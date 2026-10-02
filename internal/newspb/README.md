# newspb — vendored news service contract

A copy of the news repo's `news.v1` authoring contract: `gen/news/v1/news.pb.go`,
`gen/news/v1/news_grpc.pb.go` and the `proto/news/v1/news.proto` they are generated from,
taken unchanged from `github.com/openpak/news` at `a5e981e2eac03302dc82122afdcff388fc21f1d0`.
Same reason as `internal/accountpb`: the news service is a separate repo, and a `replace`
onto a sibling checkout would not build in CI.

The website uses it only for `/admin/news` (NEWS_GRPC_ADDR, NEWS_OPERATOR_KEY). To refresh:
re-copy the three files from the news repo and update the revision above.
