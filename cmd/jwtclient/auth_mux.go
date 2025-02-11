package jwtclient

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/filecoin-project/go-jsonrpc/auth"
	auth2 "github.com/filecoin-project/venus-auth/auth"
	ipfsHttp "github.com/ipfs/go-ipfs-cmds/http"
)

type CtxKey int

const (
	accountKey CtxKey = iota
	tokenLocationKey
)

// AuthMux used with jsonrpc library to verify whether the request is legal
type AuthMux struct {
	Logger
	handler       http.Handler
	local, remote IJwtAuthClient

	trustHandle map[string]http.Handler
}

func NewAuthMux(local, remote IJwtAuthClient, handler http.Handler, logger Logger) *AuthMux {
	return &AuthMux{
		handler:     handler,
		local:       local,
		remote:      remote,
		trustHandle: make(map[string]http.Handler),
		Logger:      logger,
	}
}

// TrustHandle for requests that can be accessed directly
// if 'pattern' with '/' as suffix, 'TrustHandler' treat it as a root path,
// that it's all sub-path will be trusted.
// if 'pattern' have no '/' with suffix,
// only the URI exactly matches the 'pattern' would be treat as trusted.
func (authMux *AuthMux) TrustHandle(pattern string, handler http.Handler) {
	authMux.trustHandle[pattern] = handler
}

func (AuthMux *AuthMux) trustedHandler(uri string) http.Handler {
	// todo: we don't consider the situation that 'trustHandle' is changed in parallelly,
	//  cause we assume trusted handler is static after application initialized
	for trustedURI, handler := range AuthMux.trustHandle {
		if trustedURI == uri || (trustedURI[len(trustedURI)-1] == '/' && strings.HasPrefix(uri, trustedURI)) {
			return handler
		}
	}
	return nil
}

func (authMux *AuthMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h := authMux.trustedHandler(r.RequestURI); h != nil {
		h.ServeHTTP(w, r)
		return
	}

	ctx := r.Context()
	token := r.Header.Get("Authorization")

	if token == "" {
		token = r.FormValue("token")
		if token != "" {
			token = "Bearer " + token
		}
	}

	if !strings.HasPrefix(token, "Bearer ") {
		authMux.Warnf("missing Bearer prefix in venusauth header")
		w.WriteHeader(401)
		return
	}

	token = strings.TrimPrefix(token, "Bearer ")

	var perms []auth.Permission
	var err error
	var host = r.RemoteAddr

	ctx = CtxWithTokenLocation(ctx, host)

	if !isNil(authMux.local) {
		if perms, err = authMux.local.Verify(ctx, token); err != nil {
			if !isNil(authMux.remote) {
				if perms, err = authMux.remote.Verify(ctx, token); err != nil {
					authMux.Warnf("JWT Verification failed (originating from %s): %s", r.RemoteAddr, err)
					w.WriteHeader(401)
					return
				}
			} else {
				authMux.Warnf("JWT Verification failed (originating from %s): %s", r.RemoteAddr, err)
				w.WriteHeader(401)
				return
			}
		}
	} else {
		if !isNil(authMux.remote) {
			if perms, err = authMux.remote.Verify(ctx, token); err != nil {
				authMux.Warnf("JWT Verification failed (originating from %s): %s", r.RemoteAddr, err)
				w.WriteHeader(401)
				return
			}
		}
	}

	ctx = auth.WithPerm(ctx, perms)
	ctx = ipfsHttp.WithPerm(ctx, perms)

	if name, _ := auth2.JwtUserFromToken(token); len(name) != 0 {
		ctx = CtxWithName(ctx, name)
	}

	*r = *(r.WithContext(ctx))

	authMux.handler.ServeHTTP(w, r)
}

func isNil(ac IJwtAuthClient) bool {
	if ac != nil && !reflect.ValueOf(ac).IsNil() {
		return false
	}
	return true
}

func (authMux *AuthMux) Warnf(template string, args ...interface{}) {
	if authMux.Logger == nil {
		fmt.Printf("auth-middware warning:%s\n", fmt.Sprintf(template, args...))
		return
	}
	authMux.Logger.Warnf(template, args...)
}

func (authMux *AuthMux) Infof(template string, args ...interface{}) {
	if authMux.Logger == nil {
		fmt.Printf("auth-midware info:%s\n", fmt.Sprintf(template, args...))
		return
	}
	authMux.Logger.Infof(template, args...)
}

func (authMux *AuthMux) Errorf(template string, args ...interface{}) {
	if authMux.Logger == nil {
		fmt.Printf("auth-midware error:%s\n", fmt.Sprintf(template, args...))
		return
	}
	authMux.Logger.Errorf(template, args...)
}

func ctxWithString(ctx context.Context, k CtxKey, v string) context.Context {
	return context.WithValue(ctx, k, v)
}

func ctxGetString(ctx context.Context, k CtxKey) (v string, exists bool) {
	v, exists = ctx.Value(k).(string)
	return
}

func CtxWithName(ctx context.Context, v string) context.Context {
	return ctxWithString(ctx, accountKey, v)
}

func CtxGetName(ctx context.Context) (name string, exists bool) {
	return ctxGetString(ctx, accountKey)
}

func CtxWithTokenLocation(ctx context.Context, v string) context.Context {
	return ctxWithString(ctx, tokenLocationKey, v)
}

func CtxGetTokenLocation(ctx context.Context) (location string, exists bool) {
	return ctxGetString(ctx, tokenLocationKey)
}

type ValueFromCtx struct{}

func (vfc *ValueFromCtx) AccFromCtx(ctx context.Context) (string, bool) {
	return CtxGetName(ctx)
}

func (vfc *ValueFromCtx) HostFromCtx(ctx context.Context) (string, bool) {
	return CtxGetTokenLocation(ctx)
}
