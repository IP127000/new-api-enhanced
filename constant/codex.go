package constant

var codexClientPassThroughHeaders = []string{
	"Originator",
	"Session-Id",
	"Session_id",
	"Thread-Id",
	"Thread_id",
	"User-Agent",
	"Version",
	"X-Client-Request-Id",
	"X-Codex-Beta-Features",
	"X-Codex-Installation-Id",
	"X-Codex-Parent-Thread-Id",
	"X-Codex-Turn-State",
	"X-Codex-Turn-Metadata",
	"X-Codex-Window-Id",
	"X-OAI-Attestation",
	"X-OpenAI-Memgen-Request",
	"X-OpenAI-Internal-Codex-Responses-Lite",
	"X-OpenAI-Subagent",
	"X-ResponsesAPI-Include-Timing-Metrics",
}

// CodexClientPassThroughHeaders returns the non-credential request headers
// that must follow a Codex client request to the ChatGPT subscription backend.
// A clone is returned so callers cannot mutate the process-wide defaults.
func CodexClientPassThroughHeaders() []string {
	headers := make([]string, len(codexClientPassThroughHeaders))
	copy(headers, codexClientPassThroughHeaders)
	return headers
}
