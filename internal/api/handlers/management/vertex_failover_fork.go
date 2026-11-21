package management

import (
	"crypto/hmac"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/MoeForge/moego/json"
	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/vertexlocations"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

var errVertexFailoverCredentialFork = errors.New("credential is not a locally managed Vertex service account")

func vertexFailoverEligibleFork(auth *coreauth.Auth) bool {
	if auth == nil || auth.Provider != "vertex" || strings.TrimSpace(auth.FileName) == "" ||
		coreauth.IsPluginVirtualAuth(auth) || auth.AuthKind() == coreauth.AuthKindAPIKey ||
		strings.TrimSpace(auth.Attributes[coreauth.AttributeAPIKey]) != "" ||
		vertexFailoverHasAccessTokenFork(auth) {
		return false
	}
	if auth.AuthSourceKind() != coreauth.AuthSourceFile || strings.TrimSpace(auth.Attributes[coreauth.AttributePath]) == "" {
		return false
	}
	account, ok := auth.Metadata["service_account"].(map[string]any)
	return ok && len(account) > 0
}

func vertexFailoverHasAccessTokenFork(auth *coreauth.Auth) bool {
	value, _ := auth.Metadata["access_token"].(string)
	return value != ""
}

func writeVertexFailoverFork(c *gin.Context, auth *coreauth.Auth) {
	mode, explicit := vertexlocations.CredentialModeFork(auth.Metadata)
	revision, err := vertexFailoverRevisionFork(auth)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read Vertex failover revision"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"mode": mode, "explicit": explicit, "revision": revision})
}

func (h *Handler) findVertexFailoverFork(c *gin.Context, name, authIndex string) *coreauth.Auth {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return nil
	}
	if strings.TrimSpace(name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return nil
	}
	name, authIndex = strings.TrimSpace(name), strings.TrimSpace(authIndex)
	auth, _ := h.authManager.GetByID(name)
	if !matchesAuthFileLookup(auth, name, authIndex) {
		auth = nil
		for _, candidate := range h.authManager.List() {
			if !matchesAuthFileLookup(candidate, name, authIndex) {
				continue
			}
			if auth != nil {
				c.JSON(http.StatusConflict, gin.H{"error": "credential name is ambiguous; specify auth_index"})
				return nil
			}
			auth = candidate
		}
	}
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return nil
	}
	if !vertexFailoverEligibleFork(auth) || !h.authManager.SupportsManagedFileAuthFork() {
		c.JSON(http.StatusConflict, gin.H{"error": errVertexFailoverCredentialFork.Error()})
		return nil
	}
	return auth
}

func (h *Handler) GetVertexFailoverFork(c *gin.Context) {
	if auth := h.findVertexFailoverFork(c, c.Query("name"), c.Query("auth_index")); auth != nil {
		writeVertexFailoverFork(c, auth)
	}
}

func (h *Handler) PatchVertexFailoverFork(c *gin.Context) {
	var patch struct {
		Name      string `json:"name"`
		AuthIndex string `json:"auth_index"`
		Mode      string `json:"mode"`
		Revision  string `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil || decoder.Decode(new(any)) != io.EOF ||
		(patch.Mode != vertexlocations.ModeOffFork && patch.Mode != vertexlocations.ModeAutomaticFork) ||
		!validVertexFailoverRevisionFork(patch.Revision) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid Vertex failover mode request"})
		return
	}
	// Share the v8 upload/delete disk-to-runtime fence so this edit cannot
	// race their file replacement or removal for the same credential.
	h.siwcCommitMu.Lock()
	defer h.siwcCommitMu.Unlock()
	target := h.findVertexFailoverFork(c, patch.Name, patch.AuthIndex)
	if target == nil {
		return
	}
	ctx := c.Request.Context()
	updated, committed, err := h.authManager.ModifyPersistedFork(ctx, target.ID, 0, func(current *coreauth.Auth) error {
		if !matchesAuthFileLookup(current, strings.TrimSpace(patch.Name), strings.TrimSpace(patch.AuthIndex)) || !vertexFailoverEligibleFork(current) {
			return errVertexFailoverCredentialFork
		}
		revision, errRevision := vertexFailoverRevisionFork(current)
		if errRevision != nil {
			return errRevision
		}
		if !hmac.Equal([]byte(revision), []byte(patch.Revision)) {
			return coreauth.ErrManagedFileAuthRevisionFork
		}
		current.Metadata[vertexlocations.ModeKeyFork] = patch.Mode
		return nil
	})
	switch {
	case errors.Is(err, coreauth.ErrManagedFileAuthRevisionFork):
		c.JSON(http.StatusConflict, gin.H{"error": "Vertex credential or policy changed; read the current revision before retrying"})
	case errors.Is(err, errVertexFailoverCredentialFork):
		c.JSON(http.StatusConflict, gin.H{"error": errVertexFailoverCredentialFork.Error()})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist Vertex failover mode"})
	case !committed || updated == nil:
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
	default:
		// Manager.Modify already updates the runtime and scheduler. This setting
		// changes neither model registration nor provider/executor identity.
		writeVertexFailoverFork(c, updated)
	}
}
