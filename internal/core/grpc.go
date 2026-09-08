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
const authMetadataKey = "authorization"

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
			CreatedAt: time.Unix(e.GetCreatedAtUnix(), 0).UTC(),
		})
	}
	return out, resp.GetMaxVersion(), nil
}

// --- InvitationsView (CR-1 pending) ---

// ListPending is stubbed until the core ships Invitations.ListForAccount
// (CR-1). PollInvitations has delivery side effects and is adapter-scoped,
// so the website must not call it for a read view.
func (g *GRPC) ListPending(ctx context.Context, accountID string) ([]PendingInvitation, error) {
	return nil, ErrContractPending
}
