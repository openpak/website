package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
)

// The account core's interceptor reads the internal credential from the
// gRPC "authorization" metadata as a Bearer value.
const (
	authMetadataKey    = "authorization"
	nodeKeyMetadataKey = "x-internal-key"
)

// GRPC is the website's client for the core's internal gRPC contracts:
// Identity, Links, Social, Events (and the Invitations read view, pending
// CR-1). Calls carry the shared internal credential.
type GRPC struct {
	conn *grpc.ClientConn
}

// DialGRPC connects to the account core.
func DialGRPC(addr, internalKey string) (*GRPC, error) {
	conn, err := grpc.NewClient(addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
			// A call that already carries a credential (a node's announce) keeps it.
			if md, ok := metadata.FromOutgoingContext(ctx); ok && len(md.Get(nodeKeyMetadataKey)) > 0 {
				return invoker(ctx, method, req, reply, cc, opts...)
			}
			return invoker(metadata.AppendToOutgoingContext(ctx, authMetadataKey, "Bearer "+internalKey), method, req, reply, cc, opts...)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("core: grpc dial: %w", err)
	}
	return &GRPC{conn: conn}, nil
}

// Close tears down the connection.
func (g *GRPC) Close() error { return g.conn.Close() }

func mapGRPC(err error) error {
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.NotFound:
		return ErrNotFound
	case codes.AlreadyExists, codes.FailedPrecondition:
		return ErrConflict
	case codes.PermissionDenied:
		return ErrBlocked
	case codes.InvalidArgument, codes.OutOfRange:
		return fmt.Errorf("%w: %s", ErrConflict, status.Convert(err).Message())
	case codes.Unauthenticated:
		return fmt.Errorf("core: internal credentials rejected: %s", status.Convert(err).Message())
	default:
		return fmt.Errorf("core: rpc failed: %s", status.Convert(err).Message())
	}
}

// --- Identity ---

// mapPasswordErr maps a failed password check. The core's interceptor uses
// Unauthenticated for a rejected internal key too; never mask a wiring failure
// as "bad password".
func mapPasswordErr(err error) error {
	if st, ok := status.FromError(err); ok && st.Code() == codes.Unauthenticated {
		if strings.Contains(st.Message(), "internal credentials") {
			return fmt.Errorf("core: internal credentials rejected (check CORE_INTERNAL_KEY)")
		}
		return ErrInvalidCredentials
	}
	return mapGRPC(err)
}

// VerifyPasswordByID checks the password of an account the website already
// knows by id. Only an active account passes: a banned or already deleting one
// has nothing left to confirm. The website can ask for a second factor, so it
// says so (second_factor_supported) and learns whether one is owed.
func (g *GRPC) VerifyPasswordByID(ctx context.Context, accountID, password string) (bool, error) {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.VerifyPasswordByID(ctx, &accountv1.VerifyPasswordByIDRequest{
		CallerNamespace:       "website",
		Purpose:               "confirm_account_action",
		AccountId:             accountID,
		Password:              password,
		SecondFactorSupported: true,
	})
	if err != nil {
		if status.Code(err) == codes.FailedPrecondition {
			// Only if the flag were lost on the way: fail closed.
			return true, ErrSecondFactorRequired
		}
		return false, mapPasswordErr(err)
	}
	switch resp.GetStatus() {
	case accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return resp.GetTwoFactorRequired(), nil
	case accountv1.AccountStatus_ACCOUNT_STATUS_BANNED:
		return false, ErrBanned
	default:
		return false, ErrInvalidCredentials
	}
}

// VerifySecondFactor is the second half of a step-up for a 2FA account.
func (g *GRPC) VerifySecondFactor(ctx context.Context, accountID, code, recoveryCode string) error {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := c.VerifySecondFactor(ctx, &accountv1.VerifySecondFactorRequest{
		AccountId: accountID, Code: code, RecoveryCode: recoveryCode,
	})
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.ResourceExhausted:
		return ErrTwoFactorLocked
	case codes.InvalidArgument:
		return ErrInvalidCode
	case codes.Unauthenticated:
		if strings.Contains(status.Convert(err).Message(), "internal credentials") {
			return fmt.Errorf("core: internal credentials rejected (check CORE_INTERNAL_KEY)")
		}
		return ErrInvalidCode
	default:
		return mapGRPC(err)
	}
}

// statusName is the account status as the HTTP contract spells it.
func statusName(s accountv1.AccountStatus) string {
	switch s {
	case accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE:
		return "active"
	case accountv1.AccountStatus_ACCOUNT_STATUS_BANNED:
		return "banned"
	case accountv1.AccountStatus_ACCOUNT_STATUS_DELETION_PENDING:
		return "deletion-pending"
	case accountv1.AccountStatus_ACCOUNT_STATUS_DELETED:
		return "deleted"
	default:
		return "unknown"
	}
}

// RequestDeletion starts account deletion (Identity.RequestAccountDeletion).
func (g *GRPC) RequestDeletion(ctx context.Context, accountID string) error {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := c.RequestAccountDeletion(ctx, &accountv1.RequestAccountDeletionRequest{
		CallerNamespace: "website",
		Purpose:         "account_deletion",
		AccountId:       accountID,
	})
	return mapGRPC(err)
}

// GetAccount resolves a minimal projection by id.
func (g *GRPC) GetAccount(ctx context.Context, accountID string) (Account, error) {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.GetAccount(ctx, &accountv1.GetAccountRequest{AccountId: accountID})
	if err != nil {
		return Account{}, mapGRPC(err)
	}
	return Account{ID: resp.GetAccountId(), DisplayName: resp.GetDisplayName(), Handle: resp.GetHandle(),
		Country: resp.GetCountry(), Birthday: resp.GetBirthday(), Bio: resp.GetBio(), Motto: resp.GetMotto(),
		Status: statusName(resp.GetStatus()), EmailVerified: resp.GetEmailVerified(),
		TwoFactorEnabled: resp.GetTwoFactorEnabled(), EraseAfter: unixTime(resp.GetEraseAfterUnix())}, nil
}

// DueDeletions lists closed accounts whose grace period is over (home only).
func (g *GRPC) DueDeletions(ctx context.Context, limit int) ([]string, error) {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.ListDueAccountDeletions(ctx, &accountv1.ListDueAccountDeletionsRequest{Limit: int32(limit)})
	if err != nil {
		return nil, mapGRPC(err)
	}
	return resp.GetAccountIds(), nil
}

// FinalizeDeletion erases the core's part of a closed account.
func (g *GRPC) FinalizeDeletion(ctx context.Context, accountID string) error {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := c.FinalizeAccountDeletion(ctx, &accountv1.RequestAccountDeletionRequest{
		CallerNamespace: "website", Purpose: "account_erasure", AccountId: accountID})
	return mapGRPC(err)
}

// RestoreDeletion is the admin restore within the grace period.
func (g *GRPC) RestoreDeletion(ctx context.Context, accountID string) error {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := c.RestoreAccountDeletion(ctx, &accountv1.RequestAccountDeletionRequest{
		CallerNamespace: "website", Purpose: "admin_restore", AccountId: accountID})
	if status.Code(err) == codes.OutOfRange {
		return ErrErasureStarted
	}
	return mapGRPC(err)
}

// --- Social ---

func (g *GRPC) RequestFriend(ctx context.Context, requesterID, addresseeID string) error {
	c := accountv1.NewSocialClient(g.conn)
	_, err := c.RequestFriend(ctx, &accountv1.RequestFriendRequest{RequesterId: requesterID, AddresseeId: addresseeID})
	return mapGRPC(err)
}

func (g *GRPC) AcceptFriend(ctx context.Context, addresseeID, requesterID string) error {
	c := accountv1.NewSocialClient(g.conn)
	_, err := c.AcceptFriend(ctx, &accountv1.AcceptFriendRequest{AddresseeId: addresseeID, RequesterId: requesterID})
	return mapGRPC(err)
}

func (g *GRPC) RemoveFriend(ctx context.Context, accountID, otherID string) error {
	c := accountv1.NewSocialClient(g.conn)
	_, err := c.RemoveFriend(ctx, &accountv1.RemoveFriendRequest{AccountId: accountID, OtherId: otherID})
	return mapGRPC(err)
}

func (g *GRPC) ListFriends(ctx context.Context, accountID string) ([]Friend, error) {
	c := accountv1.NewSocialClient(g.conn)
	resp, err := c.ListFriends(ctx, &accountv1.ListFriendsRequest{AccountId: accountID})
	if err != nil {
		return nil, mapGRPC(err)
	}
	out := make([]Friend, 0, len(resp.GetFriends()))
	for _, f := range resp.GetFriends() {
		out = append(out, Friend{AccountID: f.GetAccountId(), DisplayName: f.GetDisplayName()})
	}
	return out, nil
}

func (g *GRPC) ListIncoming(ctx context.Context, accountID string) ([]Friend, error) {
	c := accountv1.NewSocialClient(g.conn)
	resp, err := c.ListIncomingRequests(ctx, &accountv1.ListIncomingRequestsRequest{AccountId: accountID})
	if err != nil {
		return nil, mapGRPC(err)
	}
	out := make([]Friend, 0, len(resp.GetRequesters()))
	for _, f := range resp.GetRequesters() {
		out = append(out, Friend{AccountID: f.GetAccountId(), DisplayName: f.GetDisplayName()})
	}
	return out, nil
}

func (g *GRPC) ListOutgoing(ctx context.Context, accountID string) ([]Friend, error) {
	c := accountv1.NewSocialClient(g.conn)
	resp, err := c.ListOutgoingRequests(ctx, &accountv1.ListOutgoingRequestsRequest{AccountId: accountID})
	if err != nil {
		return nil, mapGRPC(err)
	}
	out := make([]Friend, 0, len(resp.GetAddressees()))
	for _, f := range resp.GetAddressees() {
		out = append(out, Friend{AccountID: f.GetAccountId(), DisplayName: f.GetDisplayName()})
	}
	return out, nil
}

func (g *GRPC) Relationship(ctx context.Context, accountID, otherID string) (Relationship, error) {
	c := accountv1.NewSocialClient(g.conn)
	resp, err := c.GetRelationship(ctx, &accountv1.GetRelationshipRequest{AccountId: accountID, OtherId: otherID})
	if err != nil {
		return RelNone, mapGRPC(err)
	}
	switch resp.GetRelationship() {
	case accountv1.Relationship_RELATIONSHIP_PENDING_INCOMING:
		return RelPendingIncoming, nil
	case accountv1.Relationship_RELATIONSHIP_PENDING_OUTGOING:
		return RelPendingOutgoing, nil
	case accountv1.Relationship_RELATIONSHIP_FRIENDS:
		return RelFriends, nil
	case accountv1.Relationship_RELATIONSHIP_BLOCKED_BY_SELF:
		return RelBlockedBySelf, nil
	case accountv1.Relationship_RELATIONSHIP_BLOCKED_BY_OTHER:
		return RelBlockedByOther, nil
	default:
		return RelNone, nil
	}
}

func (g *GRPC) Block(ctx context.Context, blockerID, blockedID string) error {
	c := accountv1.NewSocialClient(g.conn)
	_, err := c.Block(ctx, &accountv1.BlockRequest{BlockerId: blockerID, BlockedId: blockedID})
	return mapGRPC(err)
}

func (g *GRPC) Unblock(ctx context.Context, blockerID, blockedID string) error {
	c := accountv1.NewSocialClient(g.conn)
	_, err := c.Unblock(ctx, &accountv1.UnblockRequest{BlockerId: blockerID, BlockedId: blockedID})
	return mapGRPC(err)
}

// ListBlocks is who the account has blocked.
func (g *GRPC) ListBlocks(ctx context.Context, accountID string) ([]Blocked, error) {
	resp, err := accountv1.NewSocialClient(g.conn).ListBlocks(ctx, &accountv1.ListBlocksRequest{AccountId: accountID})
	if err != nil {
		return nil, mapGRPC(err)
	}
	out := make([]Blocked, 0, len(resp.GetBlocked()))
	for _, b := range resp.GetBlocked() {
		out = append(out, Blocked{AccountID: b.GetAccountId(), DisplayName: b.GetDisplayName(),
			BlockedAt: time.Unix(b.GetBlockedAtUnix(), 0).UTC()})
	}
	return out, nil
}

func (g *GRPC) CanMessage(ctx context.Context, actorID, targetID string) error {
	c := accountv1.NewSocialClient(g.conn)
	resp, err := c.CheckInteraction(ctx, &accountv1.CheckInteractionRequest{
		ActorId: actorID, TargetId: targetID, Kind: accountv1.InteractionKind_INTERACTION_KIND_MESSAGE})
	switch {
	case err != nil:
		return mapGRPC(err)
	case resp.GetAllowed():
		return nil
	case resp.GetReason() == accountv1.DenialReason_DENIAL_REASON_BLOCKED:
		return ErrBlocked
	default:
		return ErrNotAllowed
	}
}

// --- Links ---

// switchNamespace is the adapter namespace of the Switch link whose subject
// doubles as the public friend code (CR-3).
const switchNamespace = "switch"

func (g *GRPC) ActiveLinkByAccount(ctx context.Context, namespace, accountID string) (Link, error) {
	c := accountv1.NewLinksClient(g.conn)
	resp, err := c.GetActiveLinkByAccount(ctx, &accountv1.GetActiveLinkByAccountRequest{Namespace: namespace, AccountId: accountID})
	if err != nil {
		return Link{}, mapGRPC(err)
	}
	return Link{LinkID: resp.GetLinkId(), Namespace: namespace, SubjectID: resp.GetSubjectId(),
		PublicCode: resp.GetPublicCode()}, nil
}

// AccountByPublicCode resolves a published code (Links.GetLinkByPublicCode).
func (g *GRPC) AccountByPublicCode(ctx context.Context, namespace, code string) (string, error) {
	resp, err := accountv1.NewLinksClient(g.conn).GetLinkByPublicCode(ctx,
		&accountv1.GetLinkByPublicCodeRequest{Namespace: namespace, PublicCode: code})
	if err != nil {
		return "", mapGRPC(err)
	}
	return resp.GetAccountId(), nil
}

func (g *GRPC) AccountBySubject(ctx context.Context, namespace, subjectID string) (string, error) {
	c := accountv1.NewLinksClient(g.conn)
	resp, err := c.GetLinkBySubject(ctx, &accountv1.GetLinkBySubjectRequest{Namespace: namespace, SubjectId: subjectID})
	if err != nil {
		return "", mapGRPC(err)
	}
	return resp.GetAccountId(), nil
}

// Unlink starts revocation of a console link (Links.UnlinkLink).
func (g *GRPC) Unlink(ctx context.Context, linkID, idempotencyKey string) error {
	c := accountv1.NewLinksClient(g.conn)
	_, err := c.UnlinkLink(ctx, &accountv1.UnlinkLinkRequest{LinkId: linkID, IdempotencyKey: idempotencyKey})
	return mapGRPC(err)
}

// Link reserves and activates a subject for an account (Links.ReserveLink + ActivateLink).
func (g *GRPC) Link(ctx context.Context, accountID, namespace, subjectID, idempotencyKey string) error {
	c := accountv1.NewLinksClient(g.conn)
	res, err := c.ReserveLink(ctx, &accountv1.ReserveLinkRequest{
		AccountId: accountID, Namespace: namespace, SubjectId: subjectID, IdempotencyKey: idempotencyKey})
	if err != nil {
		return mapGRPC(err)
	}
	_, err = c.ActivateLink(ctx, &accountv1.ActivateLinkRequest{LinkId: res.GetLinkId(), IdempotencyKey: "activate:" + res.GetLinkId()})
	return mapGRPC(err)
}

// --- Events ---

func (g *GRPC) Poll(ctx context.Context, sinceVersion uint64, limit int32) ([]Event, uint64, error) {
	c := accountv1.NewEventsClient(g.conn)
	resp, err := c.PollEvents(ctx, &accountv1.PollEventsRequest{SinceVersion: sinceVersion, Limit: limit})
	if err != nil {
		return nil, 0, mapGRPC(err)
	}
	out := make([]Event, 0, len(resp.GetEvents()))
	for _, e := range resp.GetEvents() {
		out = append(out, Event{
			Version:   e.GetVersion(),
			EventID:   e.GetEventId(),
			Type:      e.GetType(),
			AccountID: e.GetAccountId(),
			Namespace: e.GetNamespace(),
			SubjectID: e.GetSubjectId(),
			CreatedAt: time.Unix(e.GetCreatedAtUnix(), 0).UTC(),
		})
	}
	return out, resp.GetMaxVersion(), nil
}

// PlayerCounts reports live players per title, for the status page.
func (g *GRPC) PlayerCounts(ctx context.Context) ([]TitlePlayers, map[string]int, int, error) {
	c := accountv1.NewSessionsClient(g.conn)
	resp, err := c.GetPlayerCounts(ctx, &accountv1.GetPlayerCountsRequest{})
	if err != nil {
		return nil, nil, 0, err
	}
	out := make([]TitlePlayers, 0, len(resp.GetTitles()))
	for _, t := range resp.GetTitles() {
		out = append(out, TitlePlayers{
			TitleID: t.GetTitleId(), Namespace: t.GetNamespace(), Players: int(t.GetPlayers()),
		})
	}
	nets := make(map[string]int, len(resp.GetNetworks()))
	for _, n := range resp.GetNetworks() {
		nets[n.GetNamespace()] = int(n.GetPlayers())
	}
	return out, nets, int(resp.GetTotalPlayers()), nil
}

// Playtime reads the core's per title, client and OS playtime.
func (g *GRPC) Playtime(ctx context.Context) ([]PlayTally, error) {
	resp, err := accountv1.NewSessionsClient(g.conn).GetPlaytime(ctx, &accountv1.GetPlaytimeRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]PlayTally, 0, len(resp.GetTallies()))
	for _, t := range resp.GetTallies() {
		out = append(out, PlayTally{
			Namespace: t.GetNamespace(), TitleID: t.GetTitleId(), Client: t.GetClient(), OS: t.GetOs(),
			Total: time.Duration(t.GetTotalSeconds()) * time.Second, Longest: time.Duration(t.GetLongestSeconds()) * time.Second,
		})
	}
	return out, nil
}

// --- InvitationsView ---

// ListPending reads the invitations waiting for an account. It calls
// ListInvitations, never PollInvitations: the latter is adapter-scoped, starts
// a redelivery window and is consumed by AckInvitations, so a website or phone
// read of it would take invitations away from the console that has to receive
// them.
func (g *GRPC) ListPending(ctx context.Context, accountID string) ([]PendingInvitation, error) {
	c := accountv1.NewInvitationsClient(g.conn)
	resp, err := c.ListInvitations(ctx, &accountv1.ListInvitationsRequest{AccountId: accountID})
	if err != nil {
		return nil, err
	}
	out := make([]PendingInvitation, 0, len(resp.GetInvitations()))
	for _, in := range resp.GetInvitations() {
		out = append(out, PendingInvitation{
			ID:        in.GetInvitationId(),
			FromName:  in.GetFromDisplayName(),
			TitleID:   in.GetTitleId(),
			Namespace: in.GetNamespace(),
			ExpiresAt: time.Unix(in.GetExpiresAtUnix(), 0).UTC(),
		})
	}
	return out, nil
}

// Decline rejects one invitation on behalf of its recipient. A false return is
// not an error: the invitation was already gone, or was never theirs.
func (g *GRPC) Decline(ctx context.Context, accountID, invitationID string) (bool, error) {
	c := accountv1.NewInvitationsClient(g.conn)
	resp, err := c.RejectInvitation(ctx, &accountv1.RejectInvitationRequest{
		AccountId: accountID, InvitationId: invitationID})
	if err != nil {
		return false, err
	}
	return resp.GetRejected(), nil
}

// Presence reports which of the given accounts are online and in which title.
// Accounts without a live session are absent from the map rather than present
// and false: "offline" and "we have never heard of them" are the same fact here.
func (g *GRPC) Presence(ctx context.Context, accountIDs []string) (map[string]Presence, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	c := accountv1.NewSessionsClient(g.conn)
	resp, err := c.GetPresence(ctx, &accountv1.GetPresenceRequest{AccountIds: accountIDs})
	if err != nil {
		return nil, err
	}
	out := make(map[string]Presence, len(resp.GetPresence()))
	for _, p := range resp.GetPresence() {
		out[p.GetAccountId()] = Presence{
			NodeID:    p.GetNodeId(),
			Namespace: p.GetNamespace(),
			TitleID:   p.GetTitleId(),
			Since:     time.Unix(p.GetSinceUnix(), 0).UTC(),
			Client:    p.GetClient(),
		}
	}
	return out, nil
}

// --- Federation ---

// mapFed keeps the core's wording where a person needs it: an approval
// refusal names what is short, a refused self-test says when the next is due.
func mapFed(err error) error {
	switch status.Code(err) {
	case codes.FailedPrecondition:
		return fmt.Errorf("%w: %s", ErrConflict, status.Convert(err).Message())
	case codes.ResourceExhausted:
		return fmt.Errorf("%w: %s", ErrTooSoon, status.Convert(err).Message())
	case codes.PermissionDenied:
		return fmt.Errorf("%w: %s", ErrBlocked, status.Convert(err).Message())
	}
	return mapGRPC(err)
}

func (g *GRPC) ListNodes(ctx context.Context) ([]Node, error) {
	resp, err := accountv1.NewFederationClient(g.conn).ListNodes(ctx, &accountv1.ListNodesRequest{})
	if err != nil {
		return nil, mapFed(err)
	}
	nodes := toNodes(resp.GetNodes())
	counts, err := accountv1.NewSessionsClient(g.conn).GetPlayerCounts(ctx, &accountv1.GetPlayerCountsRequest{})
	if err != nil {
		return nil, mapFed(err)
	}
	byNode := map[string]int{}
	for _, n := range counts.GetNodes() {
		byNode[n.GetNodeId()] = int(n.GetPlayers())
	}
	for i := range nodes {
		nodes[i].Players = byNode[nodes[i].ID]
	}
	return nodes, nil
}

func (g *GRPC) ListAllNodes(ctx context.Context) ([]Node, error) {
	resp, err := accountv1.NewFederationClient(g.conn).ListAllNodes(ctx, &accountv1.ListNodesRequest{})
	if err != nil {
		return nil, mapFed(err)
	}
	return toNodes(resp.GetNodes()), nil
}

func (g *GRPC) CreateNode(ctx context.Context, id, name, country, region string) (string, error) {
	resp, err := accountv1.NewFederationClient(g.conn).CreateNode(ctx, &accountv1.CreateNodeRequest{NodeId: id, Name: name, Country: country, Region: region})
	if err != nil {
		return "", mapFed(err)
	}
	return resp.GetKey(), nil
}

func (g *GRPC) RotateNodeKey(ctx context.Context, id string) (string, error) {
	resp, err := accountv1.NewFederationClient(g.conn).RotateNodeKey(ctx, &accountv1.RotateNodeKeyRequest{NodeId: id})
	if err != nil {
		return "", mapFed(err)
	}
	return resp.GetKey(), nil
}

func (g *GRPC) SetNodeApproved(ctx context.Context, id string, approved bool) error {
	_, err := accountv1.NewFederationClient(g.conn).SetNodeApproved(ctx, &accountv1.SetNodeApprovedRequest{NodeId: id, Approved: approved})
	return mapFed(err)
}

func (g *GRPC) SetNodeWaiver(ctx context.Context, id, reason string, until time.Time) error {
	var u int64
	if !until.IsZero() {
		u = until.Unix()
	}
	_, err := accountv1.NewFederationClient(g.conn).SetNodeWaiver(ctx, &accountv1.SetNodeWaiverRequest{NodeId: id, Reason: reason, UntilUnix: u})
	return mapFed(err)
}

func (g *GRPC) RevokeServer(ctx context.Context, serverID string) error {
	_, err := accountv1.NewFederationClient(g.conn).RevokeServer(ctx, &accountv1.RevokeServerRequest{ServerId: serverID})
	return mapFed(err)
}

func (g *GRPC) NodeRequirements(ctx context.Context) (NodeRequirements, error) {
	r, err := accountv1.NewFederationClient(g.conn).GetRequirements(ctx, &accountv1.GetRequirementsRequest{})
	if err != nil {
		return NodeRequirements{}, mapFed(err)
	}
	return NodeRequirements{
		NodeCores: int(r.GetNodeCores()), NodeRAMGB: int(r.GetNodeRamGb()), NodeDiskGB: int(r.GetNodeDiskGb()),
		ServerCores: int(r.GetServerCores()), ServerRAMGB: int(r.GetServerRamGb()), ServerBandwidthMbps: int(r.GetServerBandwidthMbps()),
		MeasuredMbps: int(r.GetMeasuredMbps()), SlackPct: int(r.GetSlackPct()), AvailabilityPct: r.GetAvailabilityPct(),
		ReportStale:      time.Duration(r.GetReportStaleSeconds()) * time.Second,
		SelfTestInterval: time.Duration(r.GetSelftestIntervalSeconds()) * time.Second,
		SelfTestMaxAge:   time.Duration(r.GetSelftestMaxAgeSeconds()) * time.Second,
		SelfTestBytes:    r.GetSelftestBytes(), Roles: r.GetRoles(),
	}, nil
}

func (g *GRPC) ListLocations(ctx context.Context) ([]NodeLocation, error) {
	resp, err := accountv1.NewFederationClient(g.conn).ListLocations(ctx, &accountv1.ListLocationsRequest{})
	if err != nil {
		return nil, mapFed(err)
	}
	out := make([]NodeLocation, 0, len(resp.GetLocations()))
	for _, l := range resp.GetLocations() {
		out = append(out, NodeLocation{Code: l.GetCode(), Country: l.GetCountry(), City: l.GetCity(), Wanted: int(l.GetWanted()),
			Status: l.GetStatus(), Approved: int(l.GetApproved()), Accepting: l.GetAccepting()})
	}
	return out, nil
}

func (g *GRPC) PutLocation(ctx context.Context, l NodeLocation) error {
	_, err := accountv1.NewFederationClient(g.conn).PutLocation(ctx, &accountv1.Location{Code: l.Code, Country: l.Country, City: l.City,
		Wanted: int32(l.Wanted), Status: l.Status})
	return mapFed(err)
}

func (g *GRPC) DeleteLocation(ctx context.Context, code string) error {
	_, err := accountv1.NewFederationClient(g.conn).DeleteLocation(ctx, &accountv1.DeleteLocationRequest{Code: code})
	return mapFed(err)
}

func (g *GRPC) JoinServerAs(ctx context.Context, nodeKey, name string) (string, string, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, nodeKeyMetadataKey, nodeKey)
	resp, err := accountv1.NewFederationClient(g.conn).JoinServer(ctx, &accountv1.JoinServerRequest{Name: name})
	if err != nil {
		return "", "", mapFed(err)
	}
	return resp.GetServerId(), resp.GetKey(), nil
}

func (g *GRPC) AnnounceAs(ctx context.Context, serverKey string, services map[string]string, rep ServerReport, observedIP string) (AnnounceResult, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, nodeKeyMetadataKey, serverKey)
	resp, err := accountv1.NewFederationClient(g.conn).Announce(ctx, &accountv1.AnnounceRequest{
		Services: services, ObservedIp: observedIP,
		Report: &accountv1.ServerReport{Cores: int32(rep.Cores), RamMb: rep.RAMMB, DiskGb: rep.DiskGB, DiskSsd: rep.DiskSSD,
			Ipv4: rep.IPv4, Ipv6: rep.IPv6, PortMbps: int32(rep.PortMbps)},
	})
	if err != nil {
		return AnnounceResult{}, mapFed(err)
	}
	return AnnounceResult{NodeID: resp.GetNodeId(), ServerID: resp.GetServerId(), Approved: resp.GetApproved(),
		InRotation: resp.GetInRotation(), Problems: resp.GetProblems(), SelfTestDue: resp.GetSelftestDue()}, nil
}

func (g *GRPC) StartSelfTestAs(ctx context.Context, serverKey, direction string) (string, int64, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, nodeKeyMetadataKey, serverKey)
	resp, err := accountv1.NewFederationClient(g.conn).StartSelfTest(ctx, &accountv1.StartSelfTestRequest{Direction: direction})
	if err != nil {
		return "", 0, mapFed(err)
	}
	return resp.GetServerId(), resp.GetBytes(), nil
}

func (g *GRPC) RecordSelfTest(ctx context.Context, serverID, direction string, mbps float64, rttMs int) error {
	_, err := accountv1.NewFederationClient(g.conn).RecordSelfTest(ctx, &accountv1.RecordSelfTestRequest{ServerId: serverID, Direction: direction, Mbps: mbps, RttMs: int32(rttMs)})
	return mapFed(err)
}

func unixTime(u int64) time.Time {
	if u <= 0 {
		return time.Time{}
	}
	return time.Unix(u, 0)
}

func toNodes(in []*accountv1.Node) []Node {
	out := make([]Node, 0, len(in))
	for _, n := range in {
		node := Node{ID: n.GetNodeId(), Name: n.GetName(), Country: n.GetCountry(), Region: n.GetRegion(), Services: n.GetServices(),
			LastSeen: unixTime(n.GetLastSeenUnix()), Approved: n.GetApproved(), InRotation: n.GetInRotation(), Problems: n.GetProblems(),
			WaiverReason: n.GetWaiverReason(), WaiverUntil: unixTime(n.GetWaiverUntilUnix()), Waived: n.GetWaived(),
			TotalCores: int(n.GetTotalCores()), TotalRAMMB: n.GetTotalRamMb(), TotalDiskGB: n.GetTotalDiskGb()}
		for _, v := range n.GetServers() {
			r := v.GetReport()
			node.Servers = append(node.Servers, NodeServer{ID: v.GetServerId(), Name: v.GetName(), Services: v.GetServices(),
				Report: ServerReport{Cores: int(r.GetCores()), RAMMB: r.GetRamMb(), DiskGB: r.GetDiskGb(), DiskSSD: r.GetDiskSsd(),
					IPv4: r.GetIpv4(), IPv6: r.GetIpv6(), PortMbps: int(r.GetPortMbps())},
				ObservedIP: v.GetObservedIp(), LastSeen: unixTime(v.GetLastSeenUnix()),
				DownMbps: v.GetDownMbps(), DownAt: unixTime(v.GetDownUnix()), UpMbps: v.GetUpMbps(), UpAt: unixTime(v.GetUpUnix()),
				RTTMs: int(v.GetRttMs()), Problems: v.GetProblems()})
		}
		out = append(out, node)
	}
	return out
}
