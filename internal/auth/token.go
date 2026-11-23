package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidToken = errors.New("invalid or expired token")

const tokenIssuer = "ai-style-backend"

type tokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type tokenClaims struct {
	UserID int64  `json:"user_id"`
	Issued int64  `json:"iat"`
	Expiry int64  `json:"exp"`
	Issuer string `json:"iss"`
}

// Sign 为用户签发 JWT。
func Sign(userID int64, secret string, ttl time.Duration) (string, error) {
	if userID <= 0 {
		return "", errors.New("invalid user ID")
	}
	if len(secret) < 32 {
		return "", errors.New("token secret must contain at least 32 bytes")
	}
	if ttl <= 0 {
		return "", errors.New("token lifetime must be positive")
	}

	now := time.Now()

	header := tokenHeader{
		Algorithm: "HS256",
		Type:      "JWT",
	}

	claims := tokenClaims{
		UserID: userID,
		Issued: now.Unix(),
		Expiry: now.Add(ttl).Unix(),
		Issuer: tokenIssuer,
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimsJSON)

	unsignedToken := encodedHeader + "." + encodedClaims
	signature := sign(unsignedToken, secret)

	return unsignedToken + "." + signature, nil
}

// Verify 验证 JWT 签名和有效期，成功后返回用户 ID。
func Verify(token string, secret string) (int64, error) {
	if len(secret) < 32 {
		return 0, ErrInvalidToken
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, ErrInvalidToken
	}

	unsignedToken := parts[0] + "." + parts[1]
	expectedSignature := sign(unsignedToken, secret)

	if !hmac.Equal(
		[]byte(parts[2]),
		[]byte(expectedSignature),
	) {
		return 0, ErrInvalidToken
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, ErrInvalidToken
	}

	var header tokenHeader
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return 0, ErrInvalidToken
	}

	if header.Algorithm != "HS256" || header.Type != "JWT" {
		return 0, ErrInvalidToken
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, ErrInvalidToken
	}

	var claims tokenClaims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return 0, ErrInvalidToken
	}

	now := time.Now().Unix()

	if claims.UserID <= 0 ||
		claims.Issued <= 0 ||
		claims.Issued > now ||
		claims.Expiry <= now ||
		claims.Issuer != tokenIssuer {
		return 0, ErrInvalidToken
	}

	return claims.UserID, nil
}

func sign(value string, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))

	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ParseUserID 可在需要时将用户 ID 转成字符串。
func ParseUserID(userID int64) string {
	return strconv.FormatInt(userID, 10)
}

// FormatUserID 用于检查字符串形式的用户 ID。
func FormatUserID(value string) (int64, error) {
	userID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || userID <= 0 {
		return 0, fmt.Errorf("invalid user ID")
	}
	return userID, nil
}
