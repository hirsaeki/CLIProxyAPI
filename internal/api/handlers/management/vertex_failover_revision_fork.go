package management

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"github.com/MoeForge/moego/json"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

var vertexFailoverRevisionSecretFork = func() [32]byte {
	var secret [32]byte
	_, _ = rand.Read(secret[:]) // crypto/rand terminates the process on failure in supported Go.
	return secret
}()

// Use a process-keyed MAC, so revision values reveal neither credential
// fields nor a reusable digest of the credential. A restart invalidates
// outstanding confirmations, which the client can refresh.
func vertexFailoverRevisionFork(auth *coreauth.Auth) (string, error) {
	payload, err := json.Marshal([]any{
		auth.ID, auth.FileName, auth.Provider, auth.Attributes[coreauth.AttributePath],
		lockedAuthIndex(auth), auth.RegistrationEpoch,
		vertexFailoverRevisionValueFork(auth.Metadata, "service_account"),
		vertexFailoverRevisionValueFork(auth.Metadata, "project_id"),
		vertexFailoverRevisionValueFork(auth.Metadata, "project"),
		vertexFailoverRevisionValueFork(auth.Metadata, "location"),
		vertexFailoverRevisionValueFork(auth.Metadata, vertexlocations.PolicyKeyFork),
		vertexFailoverRevisionValueFork(auth.Metadata, vertexlocations.ModeKeyFork),
	})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, vertexFailoverRevisionSecretFork[:])
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func vertexFailoverRevisionValueFork(metadata map[string]any, key string) [2]any {
	value, present := metadata[key]
	return [2]any{present, value}
}

func validVertexFailoverRevisionFork(revision string) bool {
	if len(revision) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(revision)
	return err == nil
}
