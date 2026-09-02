package council

import "net/http"

// ShutdownHandler acknowledges the caller before stopping the service, so the
// local desktop controller receives a deterministic response on Windows.
func ShutdownHandler(shutdown func()) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"shutting_down"}`))
		go shutdown()
	}
}
