package pluginhost

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Raw patching replaces the legacy full-body call; omit-body opts into metadata interception.
func TestRequestPatcherMetadataOptInAndLegacyFallbackFork(t *testing.T) {
	for _, version := range []uint32{pluginabi.SchemaVersionRequestInterceptorOmitBody, pluginabi.SchemaVersionRequestInterceptorBodyPatch} {
		for _, before := range []bool{true, false} {
			for _, omit := range []bool{false, true} {
				for _, terminate := range []bool{false, true} {
					t.Run(fmt.Sprintf("schema=%d/before=%v/omit=%v/terminate=%v", version, before, omit, terminate), func(t *testing.T) {
						patches, calls, laterCalls := 0, 0, 0
						modern := version >= pluginabi.SchemaVersionRequestInterceptorBodyPatch
						wantCalls := 1
						if modern && !omit {
							wantCalls = 0
						}
						wantPatches := 0
						currentBody := `{"v":1}`
						if modern {
							wantPatches = 1
							currentBody = `{"v":2}`
						}
						wantHeader, wantRemoved, wantPath := "", "old", ""
						if wantCalls == 1 {
							wantHeader, wantRemoved, wantPath = "kept", "", "/fork-route"
						}
						wantTerminate := wantCalls == 1 && terminate
						patcher := funcRequestBodyPatcher(func(_ context.Context, body []byte) (pluginapi.RequestBodyPatchResponse, error) {
							patches++
							if string(body) != `{"v":1}` {
								t.Fatalf("patch input=%q", body)
							}
							return pluginapi.RequestBodyPatchResponse{Edits: []pluginapi.RequestBodyEdit{{Start: 5, End: 6, Replacement: "2"}}}, nil
						})
						interceptor := requestInterceptorFunc(func(_ context.Context, req pluginapi.RequestInterceptRequest) (pluginapi.RequestInterceptResponse, error) {
							calls++
							want := currentBody
							if omit {
								want = ""
							}
							if string(req.Body) != want {
								t.Errorf("interceptor body=%q want=%q", req.Body, want)
							}
							return pluginapi.RequestInterceptResponse{
								Headers: http.Header{"X-Fork": []string{"kept"}}, ClearHeaders: []string{"X-Remove"}, Path: "/fork-route",
								Terminate: terminate, StatusCode: http.StatusForbidden, ResponseHeaders: http.Header{"X-Abort": []string{"kept"}}, ResponseBody: []byte("denied"),
							}, nil
						})
						caps := pluginapi.Capabilities{RequestInterceptor: interceptor, RequestInterceptorOmitBodyBefore: omit, RequestInterceptorOmitBodyAfter: omit}
						if before {
							caps.RequestBodyPatcherBefore = patcher
						} else {
							caps.RequestBodyPatcherAfter = patcher
						}
						first := capabilityRecord{id: "01-combined", plugin: pluginapi.Plugin{SchemaVersion: version, Capabilities: caps}}
						later := capabilityRecord{id: "02-later", plugin: pluginapi.Plugin{Capabilities: pluginapi.Capabilities{RequestInterceptor: requestInterceptorFunc(func(_ context.Context, req pluginapi.RequestInterceptRequest) (pluginapi.RequestInterceptResponse, error) {
							laterCalls++
							if req.Headers.Get("X-Fork") != wantHeader || req.Headers.Get("X-Remove") != wantRemoved || string(req.Body) != currentBody {
								t.Errorf("later request lost changes: %#v", req)
							}
							return pluginapi.RequestInterceptResponse{}, nil
						})}}}
						host := newHostWithRecords(first, later)
						req := pluginapi.RequestInterceptRequest{Body: []byte(`{"v":1}`), Headers: http.Header{"X-Remove": []string{"old"}}}
						var got pluginapi.RequestInterceptResponse
						if before {
							got = host.InterceptRequestBeforeAuth(context.Background(), req)
						} else {
							got = host.InterceptRequestAfterAuth(context.Background(), req)
						}
						if patches != wantPatches || calls != wantCalls {
							t.Fatalf("patches/interceptors=%d/%d want=%d/%d", patches, calls, wantPatches, wantCalls)
						}
						wantResponseBody := ""
						if modern {
							wantResponseBody = currentBody
						}
						if got.Headers.Get("X-Fork") != wantHeader || got.Headers.Get("X-Remove") != wantRemoved || got.Path != wantPath || string(got.Body) != wantResponseBody {
							t.Errorf("controls/body lost: %#v", got)
						}
						if got.Terminate != wantTerminate {
							t.Errorf("Terminate=%v want=%v", got.Terminate, wantTerminate)
						}
						if wantTerminate {
							if laterCalls != 0 || got.StatusCode != http.StatusForbidden || got.ResponseHeaders.Get("X-Abort") != "kept" || string(got.ResponseBody) != "denied" {
								t.Errorf("abort lost: later=%d response=%#v", laterCalls, got)
							}
						} else if laterCalls != 1 {
							t.Errorf("later calls=%d want=1", laterCalls)
						}
					})
				}
			}
		}
	}
}
