package core

import (
	"context"
	"time"

	accountv1 "github.com/openpak/website/internal/accountpb"
)

// --- Moderation (CR-5, CR-6) ---

func (g *GRPC) SearchAccounts(ctx context.Context, query string, limit int) ([]ModeratedAccount, error) {
	resp, err := accountv1.NewModerationClient(g.conn).SearchAccounts(ctx,
		&accountv1.SearchAccountsRequest{Query: query, Limit: int32(limit)})
	if err != nil {
		return nil, mapGRPC(err)
	}
	out := make([]ModeratedAccount, 0, len(resp.GetAccounts()))
	for _, a := range resp.GetAccounts() {
		out = append(out, ModeratedAccount{
			ID: a.GetAccountId(), Email: a.GetEmail(), DisplayName: a.GetDisplayName(),
			Status: statusName(a.GetStatus()), EmailVerified: a.GetEmailVerified(),
			CreatedAt: unixOrZero(a.GetCreatedAtUnix()), BanReason: a.GetBanReason(),
			MatchedSubjects: a.GetMatchedSubjects(),
		})
	}
	return out, nil
}

func (g *GRPC) SetBanned(ctx context.Context, accountID string, banned bool, reason, actor string) error {
	_, err := accountv1.NewModerationClient(g.conn).SetAccountBanned(ctx, &accountv1.SetAccountBannedRequest{
		AccountId: accountID, Banned: banned, Reason: reason, Actor: actor,
	})
	return mapGRPC(err)
}

func (g *GRPC) ListReports(ctx context.Context, status string, limit int) ([]Report, error) {
	st := accountv1.ReportStatus_REPORT_STATUS_UNSPECIFIED
	switch status {
	case "open":
		st = accountv1.ReportStatus_REPORT_STATUS_OPEN
	case "resolved":
		st = accountv1.ReportStatus_REPORT_STATUS_RESOLVED
	}
	resp, err := accountv1.NewModerationClient(g.conn).ListReports(ctx,
		&accountv1.ListReportsRequest{Status: st, Limit: int32(limit)})
	if err != nil {
		return nil, mapGRPC(err)
	}
	out := make([]Report, 0, len(resp.GetReports()))
	for _, r := range resp.GetReports() {
		rep := Report{
			ID: r.GetReportId(), ReporterID: r.GetReporterId(), ReporterName: r.GetReporterDisplayName(),
			SubjectID: r.GetSubjectAccountId(), SubjectName: r.GetSubjectDisplayName(),
			Kind: r.GetKind(), Detail: r.GetDetail(), Status: "open",
			Resolution: r.GetResolution(), ResolvedBy: r.GetResolvedBy(),
			CreatedAt: unixOrZero(r.GetCreatedAtUnix()), ResolvedAt: unixOrZero(r.GetResolvedAtUnix()),
		}
		if r.GetStatus() == accountv1.ReportStatus_REPORT_STATUS_RESOLVED {
			rep.Status = "resolved"
		}
		out = append(out, rep)
	}
	return out, nil
}

func (g *GRPC) ResolveReport(ctx context.Context, reportID, resolution, actor string) error {
	_, err := accountv1.NewModerationClient(g.conn).ResolveReport(ctx, &accountv1.ResolveReportRequest{
		ReportId: reportID, Resolution: resolution, Actor: actor,
	})
	return mapGRPC(err)
}

// --- Media (CR-2) ---

// PutAvatar publishes both avatar sizes to the core, where adapters fetch them
// to serve consoles.
func (g *GRPC) PutAvatar(ctx context.Context, accountID string, png256, png512 []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := accountv1.NewMediaClient(g.conn).PutAvatar(ctx, &accountv1.PutAvatarRequest{
		AccountId: accountID, Png_256: png256, Png_512: png512,
	})
	return mapGRPC(err)
}

func unixOrZero(s int64) time.Time {
	if s <= 0 {
		return time.Time{}
	}
	return time.Unix(s, 0).UTC()
}
