package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type lifecycleRequest struct {
	ConfigYAML   []byte   `json:"config_yaml"`
	HostFeatures []string `json:"host_features,omitempty"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelProvider            bool     `json:"model_provider"`
	ModelProviderIdentifiers []string `json:"model_provider_identifiers,omitempty"`
}

type hostHTTPRequest struct {
	HostCallbackID string      `json:"host_callback_id,omitempty"`
	Method         string      `json:"method"`
	URL            string      `json:"url"`
	Headers        http.Header `json:"headers,omitempty"`
}

type hostLogRequest struct {
	HostCallbackID string         `json:"host_callback_id,omitempty"`
	Level          string         `json:"level,omitempty"`
	Message        string         `json:"message,omitempty"`
	Fields         map[string]any `json:"fields,omitempty"`
}

var (
	runtimeConfig atomic.Value
	regionModels  = &discoveryService{cache: newMatrixCache(callHostHTTP, logCachedMatrixFallback), log: callHostLog}
)

func init() {
	cfg, _ := decodePluginConfig(nil)
	runtimeConfig.Store(cfg)
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = len
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		nativeCandidates, errConfigure := configure(request)
		if errConfigure != nil {
			return nil, errConfigure
		}
		return okEnvelope(pluginRegistration(nativeCandidates))
	case pluginabi.MethodModelStatic:
		return okEnvelope(pluginapi.ModelResponse{Provider: "vertex"})
	case pluginabi.MethodModelForAuth:
		var req authModelRPCRequest
		if errUnmarshal := json.Unmarshal(request, &req); errUnmarshal != nil {
			return nil, fmt.Errorf("decode model discovery request: %w", errUnmarshal)
		}
		resp, errModels := regionModels.modelsForAuth(req, loadedConfig())
		if errModels != nil {
			return nil, errModels
		}
		return okEnvelope(resp)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) (bool, error) {
	var req lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return false, fmt.Errorf("decode lifecycle request: %w", errUnmarshal)
		}
	}
	cfg, errDecode := decodePluginConfig(req.ConfigYAML)
	if errDecode != nil {
		return false, errDecode
	}
	previous := loadedConfig()
	runtimeConfig.Store(cfg)
	if previous.DocsURL != cfg.DocsURL {
		regionModels.cache.reset()
	}
	return hasHostFeature(req.HostFeatures, pluginabi.HostFeatureModelProviderNativeCandidates), nil
}

func loadedConfig() pluginConfig {
	if cfg, okConfig := runtimeConfig.Load().(pluginConfig); okConfig {
		return cfg
	}
	cfg, _ := decodePluginConfig(nil)
	return cfg
}

func pluginRegistration(nativeCandidates bool) registration {
	capabilities := registrationCapability{}
	if nativeCandidates {
		capabilities.ModelProvider = true
		capabilities.ModelProviderIdentifiers = []string{"vertex"}
	}
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "vertex-region-models",
			Version:          "0.2.0",
			Author:           "router-for-me",
			GitHubRepository: "https://github.com/router-for-me/CLIProxyAPI",
			Logo:             "https://raw.githubusercontent.com/router-for-me/CLIProxyAPI/main/docs/logo.png",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "docs_url", Type: pluginapi.ConfigFieldTypeString, Description: "Google locations matrix URL."},
				{Name: "cache_ttl_seconds", Type: pluginapi.ConfigFieldTypeInteger, Description: "Successful location matrix cache lifetime in seconds."},
				{Name: "fail_open", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Keep unfiltered Vertex candidates when location discovery is unavailable."},
			},
		},
		Capabilities: capabilities,
	}
}

func hasHostFeature(features []string, target string) bool {
	for _, feature := range features {
		if feature == target {
			return true
		}
	}
	return false
}

func callHostHTTP(callbackID, rawURL string, headers http.Header) (pluginapi.HTTPResponse, error) {
	result, errCall := callHost(pluginabi.MethodHostHTTPDo, hostHTTPRequest{
		HostCallbackID: callbackID,
		Method:         http.MethodGet,
		URL:            rawURL,
		Headers:        headers,
	})
	if errCall != nil {
		return pluginapi.HTTPResponse{}, errCall
	}
	var resp pluginapi.HTTPResponse
	if errUnmarshal := json.Unmarshal(result, &resp); errUnmarshal != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host HTTP response: %w", errUnmarshal)
	}
	return resp, nil
}

func callHostLog(callbackID, level, message string, fields map[string]any) {
	if fields == nil {
		fields = make(map[string]any)
	}
	fields["plugin"] = "vertex-region-models"
	_, _ = callHost(pluginabi.MethodHostLog, hostLogRequest{
		HostCallbackID: callbackID,
		Level:          level,
		Message:        message,
		Fields:         fields,
	})
}

func logCachedMatrixFallback(callbackID string, err error) {
	fields := map[string]any{}
	if err != nil {
		fields["error"] = err.Error()
	}
	callHostLog(callbackID, "warn", "vertex region model catalog refresh failed; using cached catalog", fields)
}

func callHost(method string, payload any) (json.RawMessage, error) {
	rawPayload, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal host callback %s: %w", method, errMarshal)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))

	var response C.cliproxy_buffer
	var requestPtr *C.uint8_t
	if len(rawPayload) > 0 {
		cPayload := C.CBytes(rawPayload)
		if cPayload == nil {
			return nil, fmt.Errorf("allocate host callback %s", method)
		}
		defer C.free(cPayload)
		requestPtr = (*C.uint8_t)(cPayload)
	}
	callCode := C.call_host_api(cMethod, requestPtr, C.size_t(len(rawPayload)), &response)
	var rawResponse []byte
	if response.ptr != nil && response.len > 0 {
		rawResponse = C.GoBytes(response.ptr, C.int(response.len))
	}
	if response.ptr != nil {
		C.free_host_buffer(response.ptr, response.len)
	}
	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("host callback %s returned no response, code=%d", method, int(callCode))
	}

	var env envelope
	if errUnmarshal := json.Unmarshal(rawResponse, &env); errUnmarshal != nil {
		return nil, fmt.Errorf("decode host callback envelope %s: %w", method, errUnmarshal)
	}
	if !env.OK {
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
		}
		return nil, fmt.Errorf("host callback %s failed", method)
	}
	if callCode != 0 {
		return nil, fmt.Errorf("host callback %s returned code=%d", method, int(callCode))
	}
	return append(json.RawMessage(nil), env.Result...), nil
}

func okEnvelope(value any) ([]byte, error) {
	result, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
