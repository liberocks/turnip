package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"
)

// Claims represents the JWT token claims
type Claims struct {
	UserID               string `json:"user_id"`     // Unique identifier for the user
	Email                string `json:"email"`       // User's email address
	Username             string `json:"username"`    // User's username
	Role                 string `json:"role"`        // User's assigned role for authorization
	IsVerified           string `json:"is_verified"` // Verification status ("true" or "false")
	Type                 string `json:"type"`        // Token type (e.g., "ACCESS_TOKEN")
	Realm                string `json:"realm"`       // Authentication realm, used for multi-tenant environments
	jwt.RegisteredClaims        // Standard JWT claims (iat, exp, etc.)
}

// AuthenticateWebSocket validates JWT token for WebSocket connection
func AuthenticateWebSocket(r *http.Request) (string, string, string, error) {
	start := time.Now()
	
	// Get token from query parameter for WebSocket (since headers are limited)
	tokenString := r.URL.Query().Get("token")
	if tokenString == "" {
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "token_missing")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "token_missing")
		return "", "", "", jwt.ErrTokenMalformed
	}

	// Parse and validate token
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(Conf.AccessSecret), nil
	})

	if err != nil {
		log.Error().Err(err).Msg("Failed to parse JWT token")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "parse_error")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "parse_error")
		return "", "", "", err
	}

	if !token.Valid {
		log.Error().Msg("Invalid token")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "invalid_token")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "invalid_token")
		return "", "", "", jwt.ErrSignatureInvalid
	}

	// Extract claims
	claims, ok := token.Claims.(*Claims)
	if !ok {
		log.Error().Msg("Invalid token claims")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "invalid_claims")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "invalid_claims")
		return "", "", "", jwt.ErrInvalidKey
	}

	// Check if user is verified
	// This ensures only verified users can use the token
	if claims.IsVerified == "" {
		log.Error().Msgf("Invalid token [Reason: is_verified not found]")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "unverified_user")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "unverified_user")
		return "", "", "", jwt.ErrInvalidKey
	}
	if claims.IsVerified != "true" {
		log.Error().Msgf("Invalid token [Reason: is_verified not true]")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "unverified_user")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "unverified_user")
		return "", "", "", jwt.ErrInvalidKey
	}

	// Validate token realm matches server realm
	// This prevents tokens from one environment being used in another
	if claims.Realm == "" {
		log.Error().Msgf("Invalid token [Reason: realm not found]")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "realm_missing")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "realm_missing")
		return "", "", "", jwt.ErrInvalidKey
	}
	if claims.Realm != Conf.Realm {
		log.Error().Msgf("Invalid token [Reason: realm mismatch]")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "realm_mismatch")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "realm_mismatch")
		return "", "", "", jwt.ErrInvalidKey
	}

	// Ensure token type is ACCESS_TOKEN
	// This prevents refresh tokens or other token types from being used for access
	if claims.Type == "" {
		log.Error().Msgf("Invalid token [Reason: type not found]")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "invalid_token_type")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "invalid_token_type")
		return "", "", "", jwt.ErrInvalidKey
	}
	if claims.Type != "ACCESS_TOKEN" {
		log.Error().Msgf("Invalid token [Reason: type not access]")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "invalid_token_type")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "invalid_token_type")
		return "", "", "", jwt.ErrInvalidKey
	}

	// Ensure user role is present
	if claims.Role == "" {
		log.Error().Msgf("Invalid token [Reason: role not found]")
		RecordAuthAttempt(Conf.Realm, "failed")
		RecordAuthFailure(Conf.Realm, "role_missing")
		RecordAuthDuration(Conf.Realm, "failed", time.Since(start))
		RecordTokenValidation("failed", "role_missing")
		return "", "", "", jwt.ErrInvalidKey
	}

	// Record successful authentication
	RecordAuthAttempt(Conf.Realm, "success")
	RecordAuthSuccess(Conf.Realm, claims.UserID)
	RecordAuthDuration(Conf.Realm, "success", time.Since(start))
	RecordTokenValidation("success", "valid")

	return claims.Username, claims.UserID, claims.Email, nil
}

// GenerateToken generates a JWT token for testing purposes
func GenerateToken(username, userID, email string) (string, error) {
	claims := &Claims{
		UserID:     userID,
		Email:      email,
		Username:   username,
		Role:       "user", // Set default role
		IsVerified: "true",
		Type:       "ACCESS_TOKEN",
		Realm:      Conf.Realm,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "turnip-signaling",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(Conf.AccessSecret))
}

// TokenHandler handles token generation for testing (remove in production)
func TokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		UserID   string `json:"user_id,omitempty"`
		Email    string `json:"email,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if req.Username == "" {
		http.Error(w, "Username is required", http.StatusBadRequest)
		return
	}

	// Set defaults if not provided
	if req.UserID == "" {
		req.UserID = req.Username
	}
	if req.Email == "" {
		req.Email = req.Username + "@example.com"
	}

	token, err := GenerateToken(req.Username, req.UserID, req.Email)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	response := map[string]string{
		"token": token,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error().Err(err).Msg("Failed to encode JSON response")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}
