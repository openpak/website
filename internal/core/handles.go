package core

import (
	"context"
	"errors"
	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	"time"
)

func (g *GRPC) CheckHandle(ctx context.Context, handle string) (HandleCheck, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := accountv1.NewIdentityClient(g.conn).CheckHandle(ctx, &accountv1.CheckHandleRequest{Handle: handle})
	if err != nil {
		return HandleCheck{}, mapGRPC(err)
	}
	return HandleCheck{Valid: r.Valid, Available: r.Available, Reason: r.Reason}, nil
}
func (g *GRPC) ResolveHandle(ctx context.Context, handle string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := accountv1.NewIdentityClient(g.conn).ResolveHandle(ctx, &accountv1.ResolveHandleRequest{Handle: handle})
	if err != nil {
		return "", mapGRPC(err)
	}
	return r.AccountId, nil
}
func HandleErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrHandleTaken):
		return "That handle is unavailable. Choose another."
	case errors.Is(err, ErrHandleInvalid):
		return "Use 6–15 letters and digits, starting with a letter."
	case errors.Is(err, ErrHandleReserved):
		return "That handle is reserved. Choose another."
	case errors.Is(err, ErrHandleProfane):
		return "That handle is not allowed. Choose another."
	default:
		return ""
	}
}
