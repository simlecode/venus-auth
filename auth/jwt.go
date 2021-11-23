package auth

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/filecoin-project/venus-auth/log"

	"github.com/filecoin-project/go-address"
	"github.com/gbrlsnchs/jwt/v3"
	"github.com/google/uuid"
	"golang.org/x/xerrors"

	"github.com/filecoin-project/venus-auth/config"
	"github.com/filecoin-project/venus-auth/core"
	"github.com/filecoin-project/venus-auth/storage"
	"github.com/filecoin-project/venus-auth/util"
)

var (
	ErrorParamsEmpty        = xerrors.New("The mail or password in customParams is empty")
	ErrorRemoveFailed       = xerrors.New("Remove token failed")
	ErrorNonRegisteredToken = xerrors.New("A non-registered token")
	ErrorVerificationFailed = xerrors.New("Verification Failed")
)

var jwtOAuthInstance *jwtOAuth

type VerifyAop interface {
	Verify(ctx context.Context, token string) (*JWTPayload, error)
}

type OAuthService interface {
	GenerateToken(ctx context.Context, cp *JWTPayload) (string, error)
	Verify(ctx context.Context, token string) (*JWTPayload, error)
	RemoveToken(ctx context.Context, token string) error
	Tokens(ctx context.Context, skip, limit int64) ([]*TokenInfo, error)
	GetToken(c context.Context, token string) (*TokenInfo, error)
	GetTokenByName(c context.Context, name string) ([]*TokenInfo, error)

	CreateUser(ctx context.Context, req *CreateUserRequest) (*CreateUserResponse, error)
	UpdateUser(ctx context.Context, req *UpdateUserRequest) error
	ListUsers(ctx context.Context, req *ListUsersRequest) (ListUsersResponse, error)
	GetUser(ctx context.Context, req *GetUserRequest) (*OutputUser, error)
	HasUser(ctx context.Context, req *HasUserRequest) (bool, error)
	DeleteUser(ctx *gin.Context, req *DeleteUserRequest) error

	GetMiner(ctx context.Context, req *GetMinerRequest) (*OutputUser, error)
	HasMiner(ctx context.Context, req *HasMinerRequest) (bool, error)

	GetUserRateLimits(ctx context.Context, req *GetUserRateLimitsReq) (GetUserRateLimitResponse, error)
	UpsertUserRateLimit(ctx context.Context, req *UpsertUserRateLimitReq) (string, error)
	DelUserRateLimit(ctx context.Context, req *DelUserRateLimitReq) error
}

type jwtOAuth struct {
	secret *jwt.HMACSHA
	store  storage.Store
	mp     Mapper
}

type JWTPayload struct {
	Name  string          `json:"name"`
	Perm  core.Permission `json:"perm"`
	Extra string          `json:"ext"`
}

func NewOAuthService(secret string, dbPath string, cnf *config.DBConfig) (OAuthService, error) {
	sec, err := hex.DecodeString(secret)
	if err != nil {
		return nil, err
	}
	store, err := storage.NewStore(cnf, dbPath)
	if err != nil {
		return nil, err
	}

	// TODO: remove it next version
	skip, limit := int64(0), int64(20)
	for {
		kps, err := store.List(skip, limit)
		if err != nil {
			return nil, xerrors.Errorf("list token %v", err)
		}
		for _, kp := range kps {
			if len(kp.Secret) == 0 {
				kp.Secret = secret
				log.Infof("update token %s secret %s", kp.Token, secret)
				if err := store.UpdateToken(kp); err != nil {
					return nil, xerrors.Errorf("update token(%s) %v", kp.Token, err)
				}
			}
		}
		if len(kps) == 0 {
			break
		}

		skip += limit
	}

	jwtOAuthInstance = &jwtOAuth{
		secret: jwt.NewHS256(sec),
		store:  store,
		mp:     newMapper(),
	}
	return jwtOAuthInstance, nil
}

func (o *jwtOAuth) GenerateToken(ctx context.Context, pl *JWTPayload) (string, error) {
	// one token, one secret
	secret, err := config.RandSecret()
	if err != nil {
		return "", xerrors.Errorf("rand secret %v", err)
	}
	tk, err := jwt.Sign(pl, jwt.NewHS256(secret))
	if err != nil {
		return core.EmptyString, xerrors.Errorf("gen token failed :%s", err)
	}
	token := storage.Token(tk)
	has, err := o.store.Has(token)
	if err != nil {
		return core.EmptyString, err
	}
	if has {
		return token.String(), nil
	}

	err = o.store.Put(&storage.KeyPair{Token: token, Secret: hex.EncodeToString(secret), CreateTime: time.Now(),
		Name: pl.Name, Perm: pl.Perm, Extra: pl.Extra, IsDeleted: core.NotDelete})
	if err != nil {
		return core.EmptyString, xerrors.Errorf("store token failed :%s", err)
	}
	return token.String(), nil
}

func (o *jwtOAuth) Verify(ctx context.Context, token string) (*JWTPayload, error) {
	p := new(JWTPayload)
	tk := []byte(token)

	kp, err := o.store.Get(storage.Token(token))
	if err != nil {
		return nil, xerrors.Errorf("get token: %v", err)
	}
	secret, err := hex.DecodeString(kp.Secret)
	if err != nil {
		return nil, xerrors.Errorf("decode secret %v", err)
	}
	if _, err := jwt.Verify(tk, jwt.NewHS256(secret), p); err != nil {
		return nil, ErrorVerificationFailed
	}
	return p, nil
}

type TokenInfo struct {
	Token      string    `json:"token"`
	Name       string    `json:"name"`
	Perm       string    `json:"perm"`
	Custom     string    `json:"custom"`
	CreateTime time.Time `json:"createTime"`
	IsDeleted  bool      `json:"isDeleted"`
}

func toTokenInfo(kp *storage.KeyPair) (*TokenInfo, error) {
	jwtPayload, err := util.JWTPayloadMap(string(kp.Token))
	if err != nil {
		return nil, err
	}
	return &TokenInfo{
		Token:      kp.Token.String(),
		CreateTime: kp.CreateTime,
		Name:       jwtPayload["name"].(string),
		Perm:       jwtPayload["perm"].(string),
		IsDeleted:  kp.IsDeleted,
	}, nil
}

func (o *jwtOAuth) GetToken(c context.Context, token string) (*TokenInfo, error) {
	pair, err := o.store.Get(storage.Token(token))
	if err != nil {
		return nil, err
	}
	return toTokenInfo(pair)
}

func (o *jwtOAuth) GetTokenByName(c context.Context, name string) ([]*TokenInfo, error) {
	pairs, err := o.store.ByName(name)
	if err != nil {
		return nil, err
	}
	tokenInfos := make([]*TokenInfo, 0, len(pairs))
	for _, pair := range pairs {
		tokenInfo, err := toTokenInfo(pair)
		if err != nil {
			return nil, err
		}
		tokenInfos = append(tokenInfos, tokenInfo)
	}
	return tokenInfos, nil
}

func (o *jwtOAuth) Tokens(ctx context.Context, skip, limit int64) ([]*TokenInfo, error) {
	pairs, err := o.store.List(skip, limit)
	if err != nil {
		return nil, err
	}
	tks := make([]*TokenInfo, 0, limit)
	for _, pair := range pairs {
		tokenInfo, err := toTokenInfo(pair)
		if err != nil {
			return nil, err
		}
		tks = append(tks, tokenInfo)
	}
	return tks, nil
}

func (o *jwtOAuth) RemoveToken(ctx context.Context, token string) error {
	tk := []byte(token)
	err := o.store.Delete(storage.Token(tk))
	if err != nil {
		return ErrorRemoveFailed
	}
	return nil
}

func (o *jwtOAuth) CreateUser(ctx context.Context, req *CreateUserRequest) (*CreateUserResponse, error) {
	exist, err := o.store.HasUser(req.Name)
	if err != nil {
		return nil, err
	}
	if exist {
		return nil, errors.New("user already exists")
	}
	uid, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	mAddr, err := address.NewFromString(req.Miner) // convert address type to local
	if err != nil {
		return nil, err
	}
	userNew := &storage.User{
		Id:         uid.String(),
		Name:       req.Name,
		Miner:      mAddr.String(),
		Comment:    req.Comment,
		SourceType: req.SourceType,
		State:      req.State,
		CreateTime: time.Now().Local(),
		UpdateTime: time.Now().Local(),
		IsDeleted:  core.NotDelete,
	}
	err = o.store.PutUser(userNew)
	if err != nil {
		return nil, err
	}
	return o.mp.ToOutPutUser(userNew), nil
}

func (o *jwtOAuth) UpdateUser(ctx context.Context, req *UpdateUserRequest) error {
	user, err := o.store.GetUser(req.Name)
	if err != nil {
		return err
	}
	user.UpdateTime = time.Now().Local()
	if req.KeySum&1 == 1 {
		mAddr, err := address.NewFromString(req.Miner)
		if err != nil {
			return err
		}
		user.Miner = mAddr.String()
	}
	if req.KeySum&2 == 2 {
		user.Comment = req.Comment
	}
	if req.KeySum&4 == 4 {
		user.State = req.State
	}
	if req.KeySum&8 == 8 {
		user.SourceType = req.SourceType
	}
	return o.store.UpdateUser(user)
}

func (o *jwtOAuth) ListUsers(ctx context.Context, req *ListUsersRequest) (ListUsersResponse, error) {
	users, err := o.store.ListUsers(req.GetSkip(), req.GetLimit(), req.State, req.SourceType, req.KeySum)
	if err != nil {
		return nil, err
	}
	return o.mp.ToOutPutUsers(users), nil
}

func (o *jwtOAuth) HasUser(ctx context.Context, req *HasUserRequest) (bool, error) {
	return o.store.HasUser(req.Name)
}

func (o *jwtOAuth) DeleteUser(ctx *gin.Context, req *DeleteUserRequest) error {
	return o.store.DeleteUser(req.Name)
}

func (o *jwtOAuth) GetMiner(ctx context.Context, req *GetMinerRequest) (*OutputUser, error) {
	mAddr, err := address.NewFromString(req.Miner)
	if err != nil {
		return nil, err
	}
	user, err := o.store.GetMiner(mAddr)
	if err != nil {
		return nil, err
	}
	return o.mp.ToOutPutUser(user), nil
}

func (o *jwtOAuth) HasMiner(ctx context.Context, req *HasMinerRequest) (bool, error) {
	mAddr, err := address.NewFromString(req.Miner)
	if err != nil {
		return false, err
	}
	has, err := o.store.HasMiner(mAddr)
	if err != nil {
		return false, err
	}
	return has, nil
}

func (o *jwtOAuth) GetUser(ctx context.Context, req *GetUserRequest) (*OutputUser, error) {
	user, err := o.store.GetUser(req.Name)
	if err != nil {
		return nil, err
	}
	return o.mp.ToOutPutUser(user), nil
}

func (o jwtOAuth) GetUserRateLimits(ctx context.Context, req *GetUserRateLimitsReq) (GetUserRateLimitResponse, error) {
	return o.store.GetRateLimits(req.Name, req.Id)
}

func (o *jwtOAuth) UpsertUserRateLimit(ctx context.Context, req *UpsertUserRateLimitReq) (string, error) {
	return o.store.PutRateLimit((*storage.UserRateLimit)(req))
}

func (o jwtOAuth) DelUserRateLimit(ctx context.Context, req *DelUserRateLimitReq) error {
	return o.store.DelRateLimit(req.Name, req.Id)
}

func DecodeToBytes(enc []byte) ([]byte, error) {
	encoding := base64.RawURLEncoding
	dec := make([]byte, encoding.DecodedLen(len(enc)))
	if _, err := encoding.Decode(dec, enc); err != nil {
		return nil, err
	}
	return dec, nil
}

func JwtUserFromToken(token string) (string, error) {
	sks := strings.Split(token, ".")
	if len(sks) < 1 {
		return "", fmt.Errorf("can't parse user from input token")

	}
	dec, err := DecodeToBytes([]byte(sks[1]))
	if err != nil {
		return "", err
	}
	payload := &JWTPayload{}
	err = json.Unmarshal(dec, payload)

	return payload.Name, err
}
