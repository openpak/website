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

	accountv1 "github.com/openpak/website/internal/accountpb"
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

// ResolveAccount verifies email+password through the core Identity contract
// and returns the account projection. This is the interim whoami for the
// website (CR-4): the core's HTTP login does not return the account id, and
// the website must know it for Social/Links calls.
func (g *GRPC) ResolveAccount(ctx context.Context, email, password string) (Account, error) {
	c := accountv1.NewIdentityClient(g.conn)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.VerifyPassword(ctx, &accountv1.VerifyPasswordRequest{
		CallerNamespace: "website",
		Purpose:         "web_session_identity",
		Email:           email,
		Password:        password,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.Unauthenticated {
			// The core's interceptor uses the same code for a rejected
			// internal key; never mask a wiring failure as "bad password".
			if strings.Contains(st.Message(), "internal credentials") {
				return Account{}, fmt.Errorf("core: internal credentials rejected (check CORE_INTERNAL_KEY)")
			}
			return Account{}, ErrInvalidCredentials
		}
		return Account{}, mapGRPC(err)
	}
	if resp.GetStatus() == accountv1.AccountStatus_ACCOUNT_STATUS_BANNED {
		return Account{}, ErrBanned
	}
	return Account{ID: resp.GetAccountId()}, nil
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
	return Account{ID: resp.GetAccountId(), DisplayName: resp.GetDisplayName(),
		Country: resp.GetCountry(), Birthday: resp.GetBirthday()}, nil
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
	return Link{LinkID: resp.GetLinkId(), Namespace: namespace, SubjectID: resp.GetSubjectId()}, nil
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
		}
	}
	return out, nil
}

// --- Federation ---

func (g *GRPC) ListNodes(ctx context.Context) ([]Node, error) {
	resp, err := accountv1.NewFederationClient(g.conn).ListNodes(ctx, &accountv1.ListNodesRequest{})
	if err != nil {
		return nil, mapGRPC(err)
	}
	nodes := toNodes(resp.GetNodes())
	counts, err := accountv1.NewSessionsClient(g.conn).GetPlayerCounts(ctx, &accountv1.GetPlayerCountsRequest{})
	if err != nil {
		return nil, mapGRPC(err)
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
		return nil, mapGRPC(err)
	}
	return toNodes(resp.GetNodes()), nil
}

func (g *GRPC) CreateNode(ctx context.Context, id, name, country string) (string, error) {
	resp, err := accountv1.NewFederationClient(g.conn).CreateNode(ctx, &accountv1.CreateNodeRequest{NodeId: id, Name: name, Country: country})
	if err != nil {
		return "", mapGRPC(err)
	}
	return resp.GetKey(), nil
}

func (g *GRPC) SetNodeApproved(ctx context.Context, id string, approved bool) error {
	_, err := accountv1.NewFederationClient(g.conn).SetNodeApproved(ctx, &accountv1.SetNodeApprovedRequest{NodeId: id, Approved: approved})
	return mapGRPC(err)
}

func (g *GRPC) AnnounceAs(ctx context.Context, nodeKey string, services map[string]string) (string, bool, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, nodeKeyMetadataKey, nodeKey)
	resp, err := accountv1.NewFederationClient(g.conn).Announce(ctx, &accountv1.AnnounceRequest{Services: services})
	if err != nil {
		return "", false, mapGRPC(err)
	}
	return resp.GetNodeId(), resp.GetApproved(), nil
}

func toNodes(in []*accountv1.Node) []Node {
	out := make([]Node, 0, len(in))
	for _, n := range in {
		var seen time.Time
		if n.GetLastSeenUnix() > 0 {
			seen = time.Unix(n.GetLastSeenUnix(), 0)
		}
		out = append(out, Node{ID: n.GetNodeId(), Name: n.GetName(), Country: n.GetCountry(), Services: n.GetServices(), LastSeen: seen, Approved: n.GetApproved()})
	}
	return out
}
