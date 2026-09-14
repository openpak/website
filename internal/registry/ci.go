package registry

import (
	"context"
	"errors"
)

// CIAssertion is what a verified CI identity claims about the job it runs
// (AUTH-001). A supplied client hash, repository name, User-Agent or binary
// hash alone never authorizes a download: the verified assertion is what
// counts, matched against the immutable repository identity of the
// registration.
type CIAssertion struct {
	Issuer      string // e.g. https://token.actions.githubusercontent.com
	Subject     string // the token's sub claim, recorded for the audit trail
	Repository  string // immutable identity, "owner/repo"
	Workflow    string // the approved workflow path/ref
	Ref         string // the protected ref the job ran on
	Commit      string
	RunID       string
	Environment string // the protected environment the job targets
}

// ErrAssertion is the generic failure every identity problem maps to:
// enumeration of registrations through the session endpoint is not a thing.
var ErrAssertion = errors.New("registry: CI identity rejected")

// Authorize checks the assertion against the registration: the immutable
// repository identity must match, the workflow must be one review approved,
// and a production registration only accepts a production-environment
// assertion (and vice versa for staging).
func (a *CIAssertion) Authorize(c Client) error {
	if a == nil {
		return ErrAssertion
	}
	if a.Issuer == "" || a.Repository == "" || a.Commit == "" {
		return ErrAssertion
	}
	if a.Repository != c.Repository {
		return ErrAssertion
	}
	// A fork copying the upstream workflow filename produces a different
	// workflow identity here, and is refused by this one check.
	if len(c.Workflows) > 0 && !containsString(c.Workflows, a.Workflow) {
		return ErrAssertion
	}
	switch c.Environment {
	case EnvProduction:
		if a.Environment != EnvProduction {
			return ErrAssertion
		}
	case EnvNonproduction:
		if a.Environment != EnvNonproduction {
			return ErrAssertion
		}
	default:
		return ErrAssertion
	}
	return nil
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// CIVerifier turns a bearer presented to /build-sessions into a verified
// assertion. The production implementation verifies GitHub OIDC tokens
// (02, AUTH-M1); StaticCIVerifier exists for staging and tests and is never
// wired in production.
type CIVerifier interface {
	VerifyAssertion(ctx context.Context, token string) (*CIAssertion, error)
}

// StaticCIVerifier answers one fixed token with one fixed assertion. It
// exists so staging credentials can be provisioned before OIDC lands, and so
// tests need no token endpoint. Production wiring must use a real OIDC
// verifier; the type is deliberately unusable for more than one identity.
type StaticCIVerifier struct {
	Token     string
	Assertion CIAssertion
}

// VerifyAssertion implements CIVerifier with constant-time comparison.
func (v StaticCIVerifier) VerifyAssertion(_ context.Context, token string) (*CIAssertion, error) {
	a := v.Assertion
	if token == "" || len(token) != len(v.Token) {
		return nil, ErrAssertion
	}
	if !constantTimeEqual(token, v.Token) {
		return nil, ErrAssertion
	}
	return &a, nil
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	v := 0
	for i := 0; i < len(a); i++ {
		v |= int(a[i]) ^ int(b[i])
	}
	return v == 0
}
