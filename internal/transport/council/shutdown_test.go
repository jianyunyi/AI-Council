package council

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestShutdownHandlerAcceptsRequestBeforeStoppingService(t *testing.T) {
	called := make(chan struct{}, 1)
	handler := ShutdownHandler(func() { called <- struct{}{} })

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/shutdown", nil))

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", recorder.Code)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("shutdown callback was not invoked")
	}
}
