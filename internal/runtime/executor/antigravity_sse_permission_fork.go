package executor

import (
	"net/http"

	"github.com/MoeForge/moego/ojson"
)

// antigravitySSEStatusFork keeps canonical RPC permission failures on the
// ordinary Forbidden path so validation and ToS handling still run.
func antigravitySSEStatusFork(frame ojson.Result) int {
	codeResult := frame.Get("code")
	status := frame.Get("status")
	if codeResult.Type == ojson.Number && codeResult.Raw == "7" &&
		(!status.Exists() || (status.Type == ojson.String && status.String() == "PERMISSION_DENIED")) && ojson.Valid(frame.Raw) {
		return http.StatusForbidden
	}
	return antigravityInBandErrorStatusCode(frame)
}
