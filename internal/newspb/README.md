# newspb — vendored news service contract

A copy of the news repo's `news.v1` authoring contract: `gen/news/v1/news.pb.go`,
`gen/news/v1/news_grpc.pb.go` and the `proto/news/v1/news.proto` they are generated from,
taken unchanged from `github.com/openpak/news` at `5c57daabb24edbc48f46c12b09ffe92b0cfbceaa`.
Same reason as `internal/accountpb`: the news service is a separate repo, and a `replace`
onto a sibling checkout would not build in CI.

The website uses it only for `/admin/news` (NEWS_GRPC_ADDR, NEWS_OPERATOR_KEY). To refresh:
re-copy the three files from the news repo and update the revision above.
