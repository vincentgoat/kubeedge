package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/square/go-jose.v2/jwt"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/serviceaccount"

	"github.com/kubeedge/kubeedge/edge/pkg/metamanager/constants"
)

type jwtTokenAuthenticator struct {
	indexer      cache.Indexer
	issuers      map[string]bool
	validator    serviceaccount.Validator
	implicitAuds authenticator.Audiences
}

func JWTTokenAuthenticator(issuers []string, implicitAuds authenticator.Audiences, validator serviceaccount.Validator) authenticator.Token {
	issuersMap := make(map[string]bool)
	for _, issuer := range issuers {
		issuersMap[issuer] = true
	}
	return &jwtTokenAuthenticator{
		issuers:      issuersMap,
		implicitAuds: implicitAuds,
		validator:    validator,
	}
}

func (j *jwtTokenAuthenticator) hasCorrectIssuer(tokenData string) bool {
	parts := strings.Split(tokenData, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	claims := struct {
		// WARNING: this JWT is not verified. Do not trust these claims.
		Issuer string `json:"iss"`
	}{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	return j.issuers[claims.Issuer]
}

func (j *jwtTokenAuthenticator) AuthenticateToken(ctx context.Context, tokenData string) (*authenticator.Response, bool, error) {
	if !j.hasCorrectIssuer(tokenData) {
		return nil, false, nil
	}
	public := &jwt.Claims{}
	private := j.validator.NewPrivateClaims()
	if err := parseSigned(tokenData, public, private); err != nil {
		return nil, false, err
	}

	tokenReqs, err := j.indexer.ByIndex(constants.TokenRequestIndexer, tokenData)
	if err != nil || len(tokenReqs) != 1 {
		return nil, false, fmt.Errorf("tokenData not found when authenticating")
	}

	tokenAudiences := authenticator.Audiences(public.Audience)
	if len(tokenAudiences) == 0 {
		tokenAudiences = j.implicitAuds
	}

	requestedAudiences, ok := authenticator.AudiencesFrom(ctx)
	if !ok {
		// default to apiserver audiences
		requestedAudiences = j.implicitAuds
	}

	auds := authenticator.Audiences(tokenAudiences).Intersect(requestedAudiences)
	if len(auds) == 0 && len(j.implicitAuds) != 0 {
		return nil, false, fmt.Errorf("tokenData audiences %q is invalid for the target audiences %q", tokenAudiences, requestedAudiences)
	}

	// If we get here, we have a tokenData with a recognized signature and
	// issuer string.
	sa, err := j.validator.Validate(ctx, tokenData, public, private)
	if err != nil {
		return nil, false, err
	}

	return &authenticator.Response{
		User:      sa.UserInfo(),
		Audiences: auds,
	}, true, nil
}
